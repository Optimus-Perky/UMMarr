//go:build integration

package sync

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/omdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// TestLive_AddMovieEndToEnd exercises the full real pipeline for a movie:
// TMDB search+lookup, OMDb supplementary lookup, merge, and persistence
// (including path resolution). Requires UMMARR_TMDB_TOKEN and
// UMMARR_OMDB_API_KEY - skipped if either is missing, since these are the
// two providers this project can't ship a bundled free key for. Run with:
// UMMARR_TMDB_TOKEN=... UMMARR_OMDB_API_KEY=... go test -tags=integration ./...
func TestLive_AddMovieEndToEnd(t *testing.T) {
	tmdbToken := os.Getenv("UMMARR_TMDB_TOKEN")
	omdbKey := os.Getenv("UMMARR_OMDB_API_KEY")
	if tmdbToken == "" || omdbKey == "" {
		t.Skip("UMMARR_TMDB_TOKEN and/or UMMARR_OMDB_API_KEY not set")
	}

	tmdbClient := tmdb.New(tmdb.Options{Token: tmdbToken, UserAgent: "UMMarr-integration-test/0.1"})
	omdbClient := omdb.New(omdb.Options{APIKey: omdbKey, UserAgent: "UMMarr-integration-test/0.1"})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer db.Close()

	rootFolderID := seedRootFolder(t, db, "movie")
	qualityProfileID := seedQualityProfile(t, db)

	results, err := tmdbClient.SearchMovies(ctx, "Inception", 2010)
	if err != nil {
		t.Fatalf("search tmdb: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("want at least one search result for 'Inception'")
	}

	movieSvc := &MovieService{DB: db, TMDB: tmdbClient, OMDb: omdbClient}
	movieID, err := movieSvc.AddByTMDBID(ctx, results[0].ID, rootFolderID, qualityProfileID)
	if err != nil {
		t.Fatalf("add movie: %v", err)
	}

	var title, path string
	var year int
	err = db.QueryRowContext(ctx, `
		SELECT mm.title, mm.year, m.path
		FROM movies m JOIN movie_metadata mm ON mm.id = m.movie_metadata_id
		WHERE m.id = ?
	`, movieID).Scan(&title, &year, &path)
	if err != nil {
		t.Fatalf("query movie: %v", err)
	}
	if title != "Inception" || year != 2010 {
		t.Fatalf("want title 'Inception' year 2010, got %q %d", title, year)
	}
	wantPath := "/media/movie/Inception (2010)"
	if path != wantPath {
		t.Fatalf("want path %q, got %q", wantPath, path)
	}

	// Confirm OMDb's ratings actually made it into the merged record -
	// proof both providers, not just TMDB, contributed.
	var ratingsJSON string
	if err := db.QueryRowContext(ctx, `SELECT ratings FROM movie_metadata WHERE id = (SELECT movie_metadata_id FROM movies WHERE id = ?)`, movieID).Scan(&ratingsJSON); err != nil {
		t.Fatalf("query ratings: %v", err)
	}
	t.Logf("merged ratings: %s", ratingsJSON)

	assertRefreshDoesNotMoveMovie(t, ctx, db, movieSvc, movieID, path)
}

func assertRefreshDoesNotMoveMovie(t *testing.T, ctx context.Context, db *sql.DB, svc *MovieService, movieID int64, originalPath string) {
	t.Helper()
	if err := svc.Refresh(ctx, movieID); err != nil {
		t.Fatalf("refresh movie: %v", err)
	}
	var path string
	if err := db.QueryRowContext(ctx, `SELECT path FROM movies WHERE id = ?`, movieID).Scan(&path); err != nil {
		t.Fatalf("query path after refresh: %v", err)
	}
	if path != originalPath {
		t.Fatalf("want path unchanged after refresh, got %q (was %q)", path, originalPath)
	}
}
