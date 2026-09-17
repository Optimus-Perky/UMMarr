//go:build integration

package tmdb

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLive_SearchAndGetMovie hits the real TMDB API, requiring a token via
// UMMARR_TMDB_TOKEN (skipped if not set - unlike TVMaze/MusicBrainz, TMDB
// needs a free key the user obtains themselves, see internal/config).
// Run with: UMMARR_TMDB_TOKEN=... go test -tags=integration ./...
func TestLive_SearchAndGetMovie(t *testing.T) {
	token := os.Getenv("UMMARR_TMDB_TOKEN")
	if token == "" {
		t.Skip("UMMARR_TMDB_TOKEN not set")
	}

	client := New(Options{Token: token, UserAgent: "UMMarr-integration-test/0.1"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	results, err := client.SearchMovies(ctx, "Inception", 2010)
	if err != nil {
		t.Fatalf("search movies: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("want at least one search result for 'Inception'")
	}

	movie, err := client.GetMovie(ctx, results[0].ID)
	if err != nil {
		t.Fatalf("get movie: %v", err)
	}
	if movie.ExternalIDs == nil || movie.ExternalIDs.IMDbID == "" {
		t.Fatalf("want an imdb external id on a well-known movie, got %+v", movie.ExternalIDs)
	}
	t.Logf("Inception -> tmdb id %d, imdb %s", movie.ID, movie.ExternalIDs.IMDbID)
}
