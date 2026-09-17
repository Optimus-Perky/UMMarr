//go:build integration

package tvmaze

import (
	"context"
	"testing"
	"time"
)

// TestLive_SearchAndExternals hits the real TVMaze API (no key required)
// to confirm a live show's externals.imdb round-trips correctly - the
// mechanism used to attribute provider='imdb' external_ids rows without
// ever calling IMDb directly. Run with: go test -tags=integration ./...
func TestLive_SearchAndExternals(t *testing.T) {
	client := New(Options{UserAgent: "UMMarr-integration-test/0.1"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	results, err := client.SearchShows(ctx, "Breaking Bad")
	if err != nil {
		t.Fatalf("search shows: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("want at least one search result for 'Breaking Bad'")
	}

	show, err := client.GetShow(ctx, results[0].Show.ID)
	if err != nil {
		t.Fatalf("get show: %v", err)
	}
	if show.Externals.IMDb == nil || *show.Externals.IMDb == "" {
		t.Fatalf("want an imdb external id on a well-known show, got %+v", show.Externals)
	}
	t.Logf("Breaking Bad -> tvmaze id %d, imdb %s", show.ID, *show.Externals.IMDb)
}
