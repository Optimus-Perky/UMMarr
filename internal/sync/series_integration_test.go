//go:build integration

package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tvmaze"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// TestLive_AddSeriesEndToEnd exercises the full real pipeline for a TV
// series: TMDB search+lookup+season/episode fetches, a best-effort TVMaze
// match (no key needed), merge, and persistence of series+seasons+
// episodes. Requires UMMARR_TMDB_TOKEN - skipped if missing. Run with:
// UMMARR_TMDB_TOKEN=... go test -tags=integration ./...
func TestLive_AddSeriesEndToEnd(t *testing.T) {
	tmdbToken := os.Getenv("UMMARR_TMDB_TOKEN")
	if tmdbToken == "" {
		t.Skip("UMMARR_TMDB_TOKEN not set")
	}

	tmdbClient := tmdb.New(tmdb.Options{Token: tmdbToken, UserAgent: "UMMarr-integration-test/0.1"})
	tvmazeClient := tvmaze.New(tvmaze.Options{UserAgent: "UMMarr-integration-test/0.1"})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer db.Close()

	rootFolderID := seedRootFolder(t, db, "series")
	qualityProfileID := seedQualityProfile(t, db)

	results, err := tmdbClient.SearchSeries(ctx, "Breaking Bad")
	if err != nil {
		t.Fatalf("search tmdb series: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("want at least one search result for 'Breaking Bad'")
	}

	seriesSvc := &SeriesService{DB: db, TMDB: tmdbClient, TVMaze: tvmazeClient}
	seriesID, err := seriesSvc.AddByTMDBID(ctx, results[0].ID, rootFolderID, qualityProfileID)
	if err != nil {
		t.Fatalf("add series: %v", err)
	}

	var title, path string
	err = db.QueryRowContext(ctx, `
		SELECT sm.title, s.path
		FROM series s JOIN series_metadata sm ON sm.id = s.series_metadata_id
		WHERE s.id = ?
	`, seriesID).Scan(&title, &path)
	if err != nil {
		t.Fatalf("query series: %v", err)
	}
	if title != "Breaking Bad" {
		t.Fatalf("want title 'Breaking Bad', got %q", title)
	}
	if want := "/media/series/Breaking Bad"; path != want {
		t.Fatalf("want path %q, got %q", want, path)
	}

	var seasonCount, episodeCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM seasons WHERE series_id = ?`, seriesID).Scan(&seasonCount); err != nil {
		t.Fatalf("count seasons: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM episodes WHERE series_id = ?`, seriesID).Scan(&episodeCount); err != nil {
		t.Fatalf("count episodes: %v", err)
	}
	if seasonCount == 0 || episodeCount == 0 {
		t.Fatalf("want real seasons/episodes synced, got %d seasons, %d episodes", seasonCount, episodeCount)
	}
	t.Logf("Breaking Bad synced: %d seasons, %d episodes", seasonCount, episodeCount)
}
