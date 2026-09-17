package store_test

import (
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func field[T any](v T, provider string) metadata.Field[T] {
	return metadata.Field[T]{Value: v, Provider: provider}
}

// TestUpdateEpisodeDetails: a real name replaces a placeholder, a
// placeholder never replaces a real name, and nothing else is touched.
func TestUpdateEpisodeDetails(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	seriesID, episodeID := seedSeriesWithEpisode(t, db)

	aired := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	named, err := store.UpdateEpisodeDetails(ctx, db, episodeID, metadata.EpisodeMetadata{EpisodeNumber: 1,
		Title: field("I Wanna Be Your Dog", "tvdb"), Overview: field("A summary.", "tvdb"), AirDate: field(&aired, "tvdb"), Runtime: field(55, "tvdb")})
	if err != nil || !named {
		t.Fatalf("want the episode named, got %v %v", named, err)
	}
	var title, overview string
	db.QueryRow(`SELECT title, COALESCE(overview, '') FROM episodes WHERE id = ?`, episodeID).Scan(&title, &overview)
	if title != "I Wanna Be Your Dog" || overview != "A summary." {
		t.Fatalf("want the name and summary saved, got %q / %q", title, overview)
	}

	// A placeholder from a provider doesn't overwrite it.
	if named, err = store.UpdateEpisodeDetails(ctx, db, episodeID, metadata.EpisodeMetadata{EpisodeNumber: 1, Title: field("Episode 1", "tmdb")}); err != nil || named {
		t.Errorf("want no change from a placeholder, got %v %v", named, err)
	}
	db.QueryRow(`SELECT title FROM episodes WHERE id = ?`, episodeID).Scan(&title)
	if title != "I Wanna Be Your Dog" {
		t.Errorf("want the real name kept, got %q", title)
	}

	// Series with placeholders are the ones the search looks at.
	db.Exec(`UPDATE episodes SET title = 'Episode 2' WHERE id = ?`, episodeID)
	ids, err := store.SeriesWithUnnamedEpisodes(ctx, db)
	if err != nil || len(ids) != 1 || ids[0] != seriesID {
		t.Fatalf("want the series with placeholders listed, got %v %v", ids, err)
	}
	db.Exec(`UPDATE episodes SET title = 'A real name'`)
	if ids, _ = store.SeriesWithUnnamedEpisodes(ctx, db); len(ids) != 0 {
		t.Errorf("want nothing to look at once every episode is named, got %v", ids)
	}
}
