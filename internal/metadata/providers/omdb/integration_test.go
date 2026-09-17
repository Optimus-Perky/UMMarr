//go:build integration

package omdb

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLive_GetByIMDbID hits the real OMDb API, requiring a key via
// UMMARR_OMDB_API_KEY (skipped if not set). Run with:
// UMMARR_OMDB_API_KEY=... go test -tags=integration ./...
func TestLive_GetByIMDbID(t *testing.T) {
	apiKey := os.Getenv("UMMARR_OMDB_API_KEY")
	if apiKey == "" {
		t.Skip("UMMARR_OMDB_API_KEY not set")
	}

	client := New(Options{APIKey: apiKey, UserAgent: "UMMarr-integration-test/0.1"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.GetByIMDbID(ctx, "tt1375666") // Inception
	if err != nil {
		t.Fatalf("get by imdb id: %v", err)
	}
	if resp.Title != "Inception" {
		t.Fatalf("want title 'Inception', got %q", resp.Title)
	}
	if len(resp.Ratings) == 0 {
		t.Fatal("want at least one rating (e.g. Rotten Tomatoes) on a well-known movie")
	}
	t.Logf("Inception ratings: %+v", resp.Ratings)
}
