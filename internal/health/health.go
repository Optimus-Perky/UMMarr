// Package health is Sonarr's System → Health: what's wrong with the setup,
// checked every few minutes and shown until fixed.
package health

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Issue is one problem.
type Issue struct {
	Level   string // error or warning
	Message string
	Fix     string
}

// Checker runs the checks and keeps the latest result.
type Checker struct {
	DB *sql.DB
	// TMDBConfigured says whether a TMDB token is set.
	TMDBConfigured bool
	// FFprobeAvailable says whether files can be analyzed.
	FFprobeAvailable bool
	// SSL is the Host settings, for checking the certificate files exist.
	SSL store.HostSettings
	// TestClient checks a download client's connection (nil skips that check).
	TestClient func(ctx context.Context, dc store.DownloadClient) error
	// OnNewIssue is told about an issue that wasn't there last time.
	OnNewIssue func(ctx context.Context, issue Issue)

	mu        sync.Mutex
	issues    []Issue
	checkedAt time.Time
}

// Last is the latest result and when it was taken.
func (c *Checker) Last() ([]Issue, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Issue(nil), c.issues...), c.checkedAt
}

// Check runs every check and remembers the result.
func (c *Checker) Check(ctx context.Context) []Issue {
	var issues []Issue
	add := func(level, message, fix string) {
		issues = append(issues, Issue{Level: level, Message: message, Fix: fix})
	}

	if !c.TMDBConfigured {
		add("error", "No TMDB token is configured, so movies and series can't be searched for or refreshed.", "Set UMMARR_TMDB_TOKEN in the container's .env.")
	}
	if ms, err := store.GetMediaSettings(ctx, c.DB); err == nil {
		if (ms.AnalyzeVideoFiles || ms.AnalyzeAudioFiles) && !c.FFprobeAvailable {
			add("warning", "FFprobe isn't installed, so files can't be analyzed for codecs and audio tracks.", "Rebuild UMMarr's image, which bundles FFprobe, or turn off Analyze Video Files and Analyze Audio Files under Settings → Media Management.")
		}
		if ms.PlexMediaInfo {
			if _, _, found, err := store.PlexConnection(ctx, c.DB); err == nil && !found {
				add("warning", "Use Plex Media Info is on, but there's no Plex connection with a server URL and token.", "Add one under Settings → Connect, or turn the option off under Settings → Media Management.")
			}
		}
	}
	if indexers, err := store.ListIndexers(ctx, c.DB); err == nil {
		enabled := 0
		for _, ix := range indexers {
			if ix.EnableRSS || ix.EnableAutomaticSearch || ix.EnableInteractiveSearch {
				enabled++
			}
			if ix.BackedOff(time.Now()) {
				add("warning", fmt.Sprintf("Indexer %s is unavailable due to failures: %s", ix.Name, ix.LastError), "It's retried after a back-off; check the indexer's URL and API key if it keeps failing.")
			}
		}
		if enabled == 0 {
			add("warning", "No indexers are enabled, so nothing can be searched for or grabbed.", "Add one under Settings → Indexers, or sync them from Prowlarr.")
		}
	}
	if clients, err := store.ListDownloadClients(ctx, c.DB); err == nil {
		enabled := 0
		for _, dc := range clients {
			if !dc.Enabled {
				continue
			}
			enabled++
			if c.TestClient != nil {
				tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
				if err := c.TestClient(tctx, dc); err != nil {
					add("error", fmt.Sprintf("Download client %s can't be reached: %v", dc.Name, err), "Check its URL and credentials under Settings → Download Clients.")
				}
				cancel()
			}
		}
		if enabled == 0 {
			add("warning", "No download client is enabled, so nothing can be grabbed.", "Add one under Settings → Download Clients.")
		}
	}
	ms, _ := store.GetMediaSettings(ctx, c.DB)
	for _, mediaType := range []string{"movie", "series", "music"} {
		folders, err := store.ListRootFolders(ctx, c.DB, mediaType)
		if err != nil {
			continue
		}
		for _, f := range folders {
			info, err := os.Stat(f.Path)
			switch {
			case err != nil:
				add("error", fmt.Sprintf("Library folder %s is missing or unreadable.", f.Path), "Check the container's volume mounts and the folder's permissions.")
				continue
			case !info.IsDir():
				add("error", fmt.Sprintf("Library folder %s isn't a folder.", f.Path), "Point the library folder at a directory.")
				continue
			}
			if free, err := importer.FreeSpace(f.Path); err == nil && ms.MinimumFreeSpaceMB > 0 && free < ms.MinimumFreeSpaceMB<<20 {
				add("warning", fmt.Sprintf("Library folder %s has only %s free, below the %d MB minimum.", f.Path, importer.FormatBytes(free), ms.MinimumFreeSpaceMB), "Free some space, or lower Minimum Free Space under Media management.")
			}
		}
	}
	if c.SSL.SSLEnabled {
		for _, p := range []string{c.SSL.SSLCertPath, c.SSL.SSLKeyPath} {
			if _, err := os.Stat(p); err != nil {
				add("error", fmt.Sprintf("SSL is enabled but %s can't be read.", p), "Point Settings → General → SSL at the certificate and key files as the container sees them.")
			}
		}
	}
	if settings, err := store.GetIndexerSettings(ctx, c.DB); err == nil && settings.RSSSyncInterval <= 0 {
		add("warning", "RSS Sync is off, so new releases are only found when you search.", "Set an RSS Sync Interval under Settings → Indexers → Options.")
	}

	c.mu.Lock()
	previous := c.issues
	c.issues, c.checkedAt = issues, time.Now()
	c.mu.Unlock()
	if c.OnNewIssue != nil {
		seen := map[string]bool{}
		for _, p := range previous {
			seen[p.Message] = true
		}
		for _, i := range issues {
			if !seen[i.Message] && i.Level == "error" {
				c.OnNewIssue(ctx, i)
			}
		}
	}
	return issues
}
