package sync

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

type fakeProbe struct {
	mu    gosync.Mutex
	calls map[string]int
}

func (p *fakeProbe) probe(ctx context.Context, path string) (mediainfo.Info, error) {
	p.mu.Lock()
	p.calls[path]++
	p.mu.Unlock()
	if strings.Contains(path, "S01E03") {
		return mediainfo.Info{}, errors.New("ffprobe: No such file or directory")
	}
	return mediainfo.Info{Schema: mediainfo.Schema, Source: mediainfo.SourceFFprobe, VideoCodec: "x265", Width: 1920, Height: 1080, AudioCodec: "EAC3", AudioChannels: 5.1}, nil
}

func (p *fakeProbe) count(path string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[path]
}

func (p *fakeProbe) total() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.calls {
		n += c
	}
	return n
}

type analyzerLibrary struct {
	db                                *sql.DB
	movieID, seriesID                 int64
	moviePath, multiPath, missingPath string
}

// seedAnalyzerLibrary is a movie file, a two-episode file and an episode
// file that isn't on disk.
func seedAnalyzerLibrary(t *testing.T) analyzerLibrary {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	lib := analyzerLibrary{db: db, movieID: seedMovie(t, db)}
	md, _, _ := store.GetMovieDetail(ctx, db, lib.movieID)
	store.InsertMovieFile(ctx, db, lib.movieID, "Movie.mkv", 1)
	lib.moviePath = md.Path.String + "/Movie.mkv"

	var profile int64
	db.QueryRow(`SELECT quality_profile_id FROM movies WHERE id = ?`, lib.movieID).Scan(&profile)
	meta, err := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{Title: metadata.Field[string]{Value: "Show", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if lib.seriesID, err = store.UpsertSeries(ctx, db, meta, profile, seedRootFolder(t, db, "series"), true); err != nil {
		t.Fatal(err)
	}
	seasonID, _ := store.UpsertSeason(ctx, db, lib.seriesID, metadata.SeasonMetadata{SeasonNumber: 1})
	var episodes []int64
	for n := 1; n <= 3; n++ {
		id, _ := store.UpsertEpisode(ctx, db, lib.seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: n, Title: metadata.Field[string]{Value: "Ep", Provider: "tmdb"}})
		episodes = append(episodes, id)
	}
	sd, _, _ := store.GetSeriesDetail(ctx, db, lib.seriesID)
	store.AttachEpisodeFile(ctx, db, episodes[0], "Season 01/Show - S01E01-E02.mkv", 1)
	store.AttachEpisodeFile(ctx, db, episodes[1], "Season 01/Show - S01E01-E02.mkv", 1)
	store.AttachEpisodeFile(ctx, db, episodes[2], "Season 01/Show - S01E03.mkv", 1)
	lib.multiPath = sd.Path.String + "/Season 01/Show - S01E01-E02.mkv"
	lib.missingPath = sd.Path.String + "/Season 01/Show - S01E03.mkv"
	return lib
}

func TestMediaAnalyzer_ReadsWaitingFilesOnce(t *testing.T) {
	lib := seedAnalyzerLibrary(t)
	ctx := context.Background()
	p := &fakeProbe{calls: map[string]int{}}
	a := &MediaAnalyzer{DB: lib.db, Probe: p.probe, Workers: 3, BatchSize: 10}
	if err := a.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if p.count(lib.moviePath) != 1 || p.count(lib.multiPath) != 1 || p.count(lib.missingPath) != 1 {
		t.Fatalf("want each file read once, a two-episode file too, got %v", p.calls)
	}
	if st := a.Status(); st.Running || st.Done != 3 || st.Failed != 1 || st.FinishedAt.IsZero() {
		t.Fatalf("status: %+v", st)
	}
	if c, _ := store.CountMediaInfo(ctx, lib.db); c.Total != 4 || c.Analyzed != 3 || c.Failed != 1 {
		t.Fatalf("counts: %+v", c)
	}
	md, _, _ := store.GetMovieDetail(ctx, lib.db, lib.movieID)
	if md.File.MediaInfo.VideoCodec != "x265" || md.File.Quality.Resolution != "1080p" {
		t.Fatalf("want the movie's info saved and its resolution filled in, got %+v", md.File)
	}

	before := p.total()
	if err := a.Run(ctx); err != nil || p.total() != before {
		t.Fatalf("a second run must read nothing, analyzed and unreadable files wait for a reset: %d -> %d (%v)", before, p.total(), err)
	}
	store.ResetMediaInfo(ctx, lib.db, "series", lib.seriesID)
	a.Run(ctx)
	if p.count(lib.multiPath) != 2 || p.count(lib.moviePath) != 1 {
		t.Fatalf("want only the reset series read again, got %v", p.calls)
	}

	// One file per batch still reaches every file.
	store.ResetMediaInfo(ctx, lib.db, "movie", lib.movieID)
	store.ResetMediaInfo(ctx, lib.db, "series", lib.seriesID)
	if err := (&MediaAnalyzer{DB: lib.db, Probe: p.probe, BatchSize: 1}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if c, _ := store.CountMediaInfo(ctx, lib.db); c.Analyzed != 3 || c.Failed != 1 {
		t.Fatalf("paged counts: %+v", c)
	}
}

func TestMediaAnalyzer_SettingsAndPlex(t *testing.T) {
	lib := seedAnalyzerLibrary(t)
	ctx := context.Background()
	p := &fakeProbe{calls: map[string]int{}}
	ms, _ := store.GetMediaSettings(ctx, lib.db)
	ms.AnalyzeVideoFiles, ms.AnalyzeAudioFiles = false, false
	store.UpdateMediaSettings(ctx, lib.db, ms)
	a := &MediaAnalyzer{DB: lib.db, Probe: p.probe}
	if err := a.Run(ctx); err != nil || p.total() != 0 || !strings.Contains(a.Status().Note, "both off") {
		t.Fatalf("want nothing read with both settings off, got %d calls, note %q (%v)", p.total(), a.Status().Note, err)
	}

	plex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/sections":
			w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"1","type":"movie","title":"Movies"}]}}`))
		case "/library/sections/1/all":
			w.Write([]byte(`{"MediaContainer":{"size":1,"totalSize":1,"Metadata":[{"Media":[{"videoCodec":"h264","width":1280,"height":720,"audioCodec":"aac","audioChannels":2,"Part":[{"file":"` + lib.moviePath + `"}]}]}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer plex.Close()
	store.CreateNotification(ctx, lib.db, store.Notification{Name: "Plex", Implementation: store.NotifyPlex, Enabled: true, Settings: map[string]string{"server_url": plex.URL, "token": "t"}})
	ms.AnalyzeVideoFiles, ms.PlexMediaInfo = true, true
	store.UpdateMediaSettings(ctx, lib.db, ms)

	if err := a.Run(ctx); err != nil {
		t.Fatal(err)
	}
	md, _, _ := store.GetMovieDetail(ctx, lib.db, lib.movieID)
	if md.File.MediaInfo.Source != mediainfo.SourcePlex || md.File.MediaInfo.VideoCodec != "h264" || p.count(lib.moviePath) != 0 || p.count(lib.multiPath) != 1 || a.Status().FromPlex != 1 {
		t.Fatalf("want the movie from Plex and the episodes from FFprobe, got %+v calls %v status %+v", md.File.MediaInfo, p.calls, a.Status())
	}
	check, err := a.CheckPlex(ctx)
	if err != nil || check.PlexFiles != 1 || check.LibraryFiles != 3 || check.Matched != 1 || !strings.Contains(check.Summary(), "1 library (Movies) holding 1 files. 1 of UMMarr's 3 files") {
		t.Fatalf("check: %+v %q (%v)", check, check.Summary(), err)
	}

	// Without FFprobe only what Plex has is read, and the run still ends.
	store.ResetMediaInfo(ctx, lib.db, "movie", lib.movieID)
	store.ResetMediaInfo(ctx, lib.db, "series", lib.seriesID)
	noProbe := &MediaAnalyzer{DB: lib.db}
	done := make(chan error, 1)
	go func() { done <- noProbe.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a run without FFprobe must not loop on files Plex doesn't have")
	}
	if c, _ := store.CountMediaInfo(ctx, lib.db); c.Analyzed != 1 || c.FromPlex != 1 {
		t.Fatalf("want only the Plex movie analyzed, got %+v", c)
	}

	// Plex down: FFprobe carries on and the note says why.
	plex.Close()
	store.ResetMediaInfo(ctx, lib.db, "movie", lib.movieID)
	if err := a.Run(ctx); err != nil || p.count(lib.moviePath) != 1 || !strings.Contains(a.Status().Note, "Plex wasn't used") {
		t.Fatalf("want FFprobe to take over, got calls %v note %q (%v)", p.calls, a.Status().Note, err)
	}
	if err := noProbe.Run(ctx); !errors.Is(err, ErrFFprobeMissing) {
		t.Fatalf("want ErrFFprobeMissing with neither FFprobe nor Plex, got %v", err)
	}
	if _, err := (&MediaAnalyzer{DB: openTestDB(t)}).CheckPlex(ctx); err == nil || !strings.Contains(err.Error(), "no Plex connection") {
		t.Fatalf("want the missing connection explained, got %v", err)
	}
}

func TestMediaAnalyzer_ImportStartsARun(t *testing.T) {
	lib := seedAnalyzerLibrary(t)
	ctx := context.Background()
	p := &fakeProbe{calls: map[string]int{}}
	a := &MediaAnalyzer{DB: lib.db, Probe: p.probe}
	a.OnEvent(ctx, store.Event{Event: store.EventGrabbed})
	a.OnEvent(ctx, store.Event{Event: store.EventImported})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st := a.Status(); !st.Running && !st.FinishedAt.IsZero() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if p.count(lib.moviePath) != 1 {
		t.Fatalf("want an import to start a run in the background, got %v", p.calls)
	}
	var none *MediaAnalyzer
	none.Kick()
	if none.FFprobeAvailable() || none.Reanalyze(ctx, "movie", 1) != nil || none.Status().Running {
		t.Fatal("a missing analyzer must be harmless")
	}
	if groupDigits(39423) != "39,423" || groupDigits(7) != "7" || groupDigits(1000000) != "1,000,000" {
		t.Fatal("groupDigits")
	}
}
