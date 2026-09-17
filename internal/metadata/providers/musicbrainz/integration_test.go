//go:build integration

package musicbrainz

import (
	"context"
	"testing"
	"time"
)

// TestLive_ArtistSearchAndReleaseGroup hits the real MusicBrainz API (no
// key required, but rate-limited to 1 req/s by this package) to confirm
// basic search/lookup works against live data. Run with:
// go test -tags=integration ./...
func TestLive_ArtistSearchAndReleaseGroup(t *testing.T) {
	client, err := New(Options{UserAgent: "UMMarr-integration-test/0.1 (test@example.invalid)"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	artists, err := client.SearchArtist(ctx, "Daft Punk")
	if err != nil {
		t.Fatalf("search artist: %v", err)
	}
	if len(artists) == 0 {
		t.Fatal("want at least one search result for 'Daft Punk'")
	}

	releaseGroups, err := client.GetArtistReleaseGroups(ctx, artists[0].ID)
	if err != nil {
		t.Fatalf("get release groups: %v", err)
	}
	if len(releaseGroups) == 0 {
		t.Fatal("want at least one release group for Daft Punk")
	}
	t.Logf("Daft Punk (%s) has %d release groups", artists[0].ID, len(releaseGroups))
}

// TestLive_ReleaseGroupSeriesRels confirms real series-rels parsing
// against a MusicBrainz release-group known to be part of a series - the
// mechanism behind compilation_series. Searches by title directly (via
// SearchReleaseGroup) rather than going through an artist's full
// discography, so the test reliably lands on an actual "Now That's What I
// Call Music" entry instead of whatever happens to sort first.
func TestLive_ReleaseGroupSeriesRels(t *testing.T) {
	client, err := New(Options{UserAgent: "UMMarr-integration-test/0.1 (test@example.invalid)"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	candidates, err := client.SearchReleaseGroup(ctx, `"Now That's What I Call Music"`)
	if err != nil {
		t.Fatalf("search release-group: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("want at least one release-group match for 'Now That's What I Call Music'")
	}

	// Series relations only come back on a direct lookup-by-id
	// (inc=series-rels), not the search endpoint - check each candidate
	// until one with series data turns up, since not every numbered entry
	// is necessarily linked to the series in MusicBrainz's data.
	for _, candidate := range candidates {
		rg, err := client.GetReleaseGroup(ctx, candidate.ID)
		if err != nil {
			t.Fatalf("get release group %s: %v", candidate.ID, err)
		}
		for _, rel := range rg.Relations {
			if rel.TargetType == "series" && rel.Series != nil {
				t.Logf("release group %q is part of series %q (attrs: %v)", rg.Title, rel.Series.Name, rel.AttributeValues)
				return
			}
		}
	}
	t.Fatal("want at least one 'Now That's What I Call Music' candidate to have a series relation")
}
