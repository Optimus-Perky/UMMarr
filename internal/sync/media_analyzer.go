package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	gosync "sync"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// MediaAnalyzer fills in media information for library files, as Sonarr,
// Radarr and Lidarr do: codecs, resolution, HDR, audio tracks and subtitles
// for movies and episodes when Analyze Video Files is on, codec, bitrate and
// bit depth for music when Analyze Audio Files is on. With Use Plex Media
// Info on it takes what Plex has already analyzed and opens only the files
// Plex doesn't have.
type MediaAnalyzer struct {
	DB *sql.DB
	// Probe reads one file with FFprobe; nil when FFprobe isn't installed.
	Probe func(ctx context.Context, path string) (mediainfo.Info, error)
	// PlexHTTP is the client used for Plex; nil uses a default.
	PlexHTTP *http.Client
	// Workers is how many files are read at once (default 2).
	Workers int
	// BatchSize is how many waiting files are fetched at a time (default 500).
	BatchSize int

	mu      gosync.Mutex
	running bool
	again   bool
	status  AnalysisStatus
}

// AnalysisStatus is what the current or last run did.
type AnalysisStatus struct {
	Running                bool
	StartedAt, FinishedAt  time.Time
	Done, FromPlex, Failed int
	// Note says why a run did less than it could, e.g. Plex couldn't be used.
	Note string
}

// ErrFFprobeMissing means there is nothing to read files with.
var ErrFFprobeMissing = errors.New("FFprobe isn't installed, so files can't be analyzed")

// FFprobeAvailable reports whether files can be read with FFprobe.
func (a *MediaAnalyzer) FFprobeAvailable() bool { return a != nil && a.Probe != nil }

// Status is the current or last run.
func (a *MediaAnalyzer) Status() AnalysisStatus {
	if a == nil {
		return AnalysisStatus{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// Kick starts a run in the background, or asks the running one to look
// again when it finishes.
func (a *MediaAnalyzer) Kick() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.running {
		a.again = true
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	go func() {
		if err := a.Run(context.Background()); err != nil && !errors.Is(err, ErrFFprobeMissing) {
			log.Printf("analyze media files: %v", err)
		}
	}()
}

// OnEvent analyzes new files as soon as they're imported.
func (a *MediaAnalyzer) OnEvent(ctx context.Context, e store.Event) {
	switch e.Event {
	case store.EventImported, store.EventUpgraded:
		a.Kick()
	}
}

// Reanalyze reads a movie's, series' or album's files again (Refresh & Scan).
func (a *MediaAnalyzer) Reanalyze(ctx context.Context, owner string, id int64) error {
	if a == nil {
		return nil
	}
	if _, err := store.ResetMediaInfo(ctx, a.DB, owner, id); err != nil {
		return err
	}
	a.Kick()
	return nil
}

// Run analyzes every file waiting for it - the Analyze media files task.
// A run already in progress makes this return at once.
func (a *MediaAnalyzer) Run(ctx context.Context) error {
	a.mu.Lock()
	if a.running {
		a.again = true
		a.mu.Unlock()
		return nil
	}
	a.running = true
	a.status = AnalysisStatus{Running: true, StartedAt: time.Now()}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.running, a.again = false, false
		a.status.Running, a.status.FinishedAt = false, time.Now()
		a.mu.Unlock()
	}()
	for {
		a.mu.Lock()
		a.again = false
		a.mu.Unlock()
		if err := a.pass(ctx); err != nil {
			return err
		}
		a.mu.Lock()
		again := a.again
		a.mu.Unlock()
		if !again {
			return nil
		}
	}
}

func (a *MediaAnalyzer) setNote(note string) {
	a.mu.Lock()
	a.status.Note = note
	a.mu.Unlock()
}

func (a *MediaAnalyzer) pass(ctx context.Context) error {
	ms, err := store.GetMediaSettings(ctx, a.DB)
	if err != nil {
		return err
	}
	video, audio := ms.AnalyzeVideoFiles, ms.AnalyzeAudioFiles
	if !video && !audio {
		a.setNote("Analyze Video Files and Analyze Audio Files are both off.")
		return nil
	}
	var plexFiles map[string]mediainfo.Info
	if ms.PlexMediaInfo {
		if plexFiles, err = a.plexLibrary(ctx); err != nil {
			plexFiles = nil
			a.setNote("Plex wasn't used: " + err.Error())
		}
	}
	if a.Probe == nil && plexFiles == nil {
		return ErrFFprobeMissing
	}
	workers := a.Workers
	if workers <= 0 {
		workers = 2
	}
	limit := a.BatchSize
	if limit <= 0 {
		limit = 500
	}
	if a.Probe == nil {
		// Files Plex doesn't have stay waiting, so page through them all at once.
		limit = 0
	}
	seen := map[string]bool{}
	for {
		files, err := store.ListFilesToAnalyze(ctx, a.DB, video, audio, limit)
		if err != nil {
			return err
		}
		var todo []store.MediaFile
		for _, f := range files {
			k := f.Kind + ":" + strconv.FormatInt(f.ID, 10)
			if !seen[k] {
				seen[k] = true
				todo = append(todo, f)
			}
		}
		if len(todo) == 0 {
			return nil
		}
		cache := &probeCache{entries: map[string]*probeEntry{}}
		jobs := make(chan store.MediaFile)
		var wg gosync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for f := range jobs {
					a.analyze(ctx, f, plexFiles, cache)
				}
			}()
		}
	feed:
		for _, f := range todo {
			select {
			case jobs <- f:
			case <-ctx.Done():
				break feed
			}
		}
		close(jobs)
		wg.Wait()
		if err := ctx.Err(); err != nil {
			return err
		}
		if limit == 0 {
			return nil
		}
	}
}

// probeCache reads each path once per batch: a multi-episode file has a
// row per episode.
type probeCache struct {
	mu      gosync.Mutex
	entries map[string]*probeEntry
}

type probeEntry struct {
	once gosync.Once
	info mediainfo.Info
}

func (c *probeCache) get(path string, read func() mediainfo.Info) mediainfo.Info {
	c.mu.Lock()
	e := c.entries[path]
	if e == nil {
		e = &probeEntry{}
		c.entries[path] = e
	}
	c.mu.Unlock()
	e.once.Do(func() { e.info = read() })
	return e.info
}

func (a *MediaAnalyzer) analyze(ctx context.Context, f store.MediaFile, plexFiles map[string]mediainfo.Info, cache *probeCache) {
	path := filepath.Clean(f.Path)
	info, fromPlex := plexFiles[path]
	if !fromPlex {
		if a.Probe == nil {
			return
		}
		info = cache.get(path, func() mediainfo.Info {
			got, err := a.Probe(ctx, path)
			if err != nil {
				return mediainfo.Failed(mediainfo.SourceFFprobe, err, time.Now().UTC())
			}
			got.AnalyzedAt = time.Now().UTC()
			return got
		})
		if info.Error != "" && ctx.Err() != nil {
			return // stopped, not unreadable
		}
	}
	if err := store.SaveMediaInfo(ctx, a.DB, f.Kind, f.ID, info); err != nil {
		log.Printf("analyze media files: save %s: %v", path, err)
		return
	}
	a.mu.Lock()
	switch {
	case info.Error != "":
		a.status.Failed++
	case fromPlex:
		a.status.Done++
		a.status.FromPlex++
	default:
		a.status.Done++
	}
	a.mu.Unlock()
}

func (a *MediaAnalyzer) plex(ctx context.Context) (*mediainfo.Plex, error) {
	url, token, found, err := store.PlexConnection(ctx, a.DB)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("there's no Plex connection with a server URL and token under Settings → Connect")
	}
	return &mediainfo.Plex{BaseURL: url, Token: token, HTTP: a.PlexHTTP}, nil
}

func (a *MediaAnalyzer) plexLibrary(ctx context.Context) (map[string]mediainfo.Info, error) {
	p, err := a.plex(ctx)
	if err != nil {
		return nil, err
	}
	files, _, err := p.Library(ctx)
	return files, err
}

// PlexCheck is what Test Plex found.
type PlexCheck struct {
	Libraries                        []string
	PlexFiles, LibraryFiles, Matched int
}

// Summary is the sentence Settings shows.
func (c PlexCheck) Summary() string {
	word := "libraries"
	if len(c.Libraries) == 1 {
		word = "library"
	}
	return fmt.Sprintf("Plex answered with %d %s (%s) holding %s files. %s of UMMarr's %s files are in Plex at the same path.",
		len(c.Libraries), word, strings.Join(c.Libraries, ", "), groupDigits(c.PlexFiles), groupDigits(c.Matched), groupDigits(c.LibraryFiles))
}

// CheckPlex reads Plex's libraries and counts how many of UMMarr's files
// Plex can describe, without saving anything.
func (a *MediaAnalyzer) CheckPlex(ctx context.Context) (PlexCheck, error) {
	p, err := a.plex(ctx)
	if err != nil {
		return PlexCheck{}, err
	}
	files, sections, err := p.Library(ctx)
	if err != nil {
		return PlexCheck{}, err
	}
	paths, err := store.ListMediaFilePaths(ctx, a.DB)
	if err != nil {
		return PlexCheck{}, err
	}
	c := PlexCheck{PlexFiles: len(files), LibraryFiles: len(paths)}
	for _, s := range sections {
		c.Libraries = append(c.Libraries, s.Title)
	}
	for path := range paths {
		if _, ok := files[path]; ok {
			c.Matched++
		}
	}
	return c, nil
}

// groupDigits writes 39423 as 39,423.
func groupDigits(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
