package store_test

import (
	"context"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestListSeries(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rootFolderID := seedRootFolder(t, db, "series")
	qualityProfileID := seedQualityProfile(t, db)

	for _, title := range []string{"Breaking Bad", "The Wire"} {
		metadataID, err := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{
			Title:       metadata.Field[string]{Value: title, Provider: "tmdb"},
			ExternalIDs: map[string]string{"tmdb": title},
		})
		if err != nil {
			t.Fatalf("upsert series_metadata %s: %v", title, err)
		}
		if _, err := store.UpsertSeries(ctx, db, metadataID, qualityProfileID, rootFolderID, true); err != nil {
			t.Fatalf("upsert series %s: %v", title, err)
		}
	}

	all, err := store.ListSeries(ctx, db)
	if err != nil {
		t.Fatalf("list series: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 series, got %d", len(all))
	}

	recent, err := store.ListRecentSeries(ctx, db, 1)
	if err != nil {
		t.Fatalf("list recent series: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("want 1 recent series (limit), got %d", len(recent))
	}
}

func TestSeriesEpisodeCounts(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rootFolderID := seedRootFolder(t, db, "series")
	qualityProfileID := seedQualityProfile(t, db)

	metadataID, err := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{
		Title:       metadata.Field[string]{Value: "Breaking Bad", Provider: "tmdb"},
		ExternalIDs: map[string]string{"tmdb": "1396"},
	})
	if err != nil {
		t.Fatalf("upsert series_metadata: %v", err)
	}
	seriesID, err := store.UpsertSeries(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert series: %v", err)
	}
	seasonID, err := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: 1})
	if err != nil {
		t.Fatalf("upsert season: %v", err)
	}
	for i := 1; i <= 3; i++ {
		if _, err := store.UpsertEpisode(ctx, db, seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: i, Title: metadata.Field[string]{Value: "Ep", Provider: "tmdb"}}); err != nil {
			t.Fatalf("upsert episode %d: %v", i, err)
		}
	}

	total, downloaded, err := store.SeriesEpisodeCounts(ctx, db, seriesID)
	if err != nil {
		t.Fatalf("series episode counts: %v", err)
	}
	if total != 3 {
		t.Fatalf("want 3 total episodes, got %d", total)
	}
	if downloaded != 0 {
		t.Fatalf("want 0 downloaded episodes (no files synced), got %d", downloaded)
	}
}

func TestSeriesWithMissingEpisodes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rootFolderID := seedRootFolder(t, db, "series")
	qualityProfileID := seedQualityProfile(t, db)

	// A fully-filed series - should NOT be returned.
	completeMetaID, err := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{
		Title: metadata.Field[string]{Value: "Complete Show", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "complete"},
	})
	if err != nil {
		t.Fatalf("upsert complete series_metadata: %v", err)
	}
	completeSeriesID, err := store.UpsertSeries(ctx, db, completeMetaID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert complete series: %v", err)
	}
	completeSeasonID, err := store.UpsertSeason(ctx, db, completeSeriesID, metadata.SeasonMetadata{SeasonNumber: 1})
	if err != nil {
		t.Fatalf("upsert complete season: %v", err)
	}
	completeEpisodeID, err := store.UpsertEpisode(ctx, db, completeSeriesID, completeSeasonID, 1, metadata.EpisodeMetadata{
		EpisodeNumber: 1, Title: metadata.Field[string]{Value: "Ep1", Provider: "tmdb"},
	})
	if err != nil {
		t.Fatalf("upsert complete episode: %v", err)
	}
	if _, err := store.AttachEpisodeFile(ctx, db, completeEpisodeID, "Ep1.mkv", 100); err != nil {
		t.Fatalf("attach complete episode file: %v", err)
	}

	// A partial series (1 of 2 episodes filed) - SHOULD be returned.
	partialMetaID, err := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{
		Title: metadata.Field[string]{Value: "Partial Show", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "partial"},
	})
	if err != nil {
		t.Fatalf("upsert partial series_metadata: %v", err)
	}
	partialSeriesID, err := store.UpsertSeries(ctx, db, partialMetaID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert partial series: %v", err)
	}
	partialSeasonID, err := store.UpsertSeason(ctx, db, partialSeriesID, metadata.SeasonMetadata{SeasonNumber: 1})
	if err != nil {
		t.Fatalf("upsert partial season: %v", err)
	}
	filedEpisodeID, err := store.UpsertEpisode(ctx, db, partialSeriesID, partialSeasonID, 1, metadata.EpisodeMetadata{
		EpisodeNumber: 1, Title: metadata.Field[string]{Value: "Ep1", Provider: "tmdb"},
	})
	if err != nil {
		t.Fatalf("upsert partial episode 1: %v", err)
	}
	if _, err := store.AttachEpisodeFile(ctx, db, filedEpisodeID, "Ep1.mkv", 100); err != nil {
		t.Fatalf("attach partial episode file: %v", err)
	}
	if _, err := store.UpsertEpisode(ctx, db, partialSeriesID, partialSeasonID, 1, metadata.EpisodeMetadata{
		EpisodeNumber: 2, Title: metadata.Field[string]{Value: "Ep2", Provider: "tmdb"},
	}); err != nil {
		t.Fatalf("upsert partial episode 2: %v", err)
	}

	missing, err := store.SeriesWithMissingEpisodes(ctx, db)
	if err != nil {
		t.Fatalf("series with missing episodes: %v", err)
	}
	if len(missing) != 1 || missing[0].ID != partialSeriesID {
		t.Fatalf("want only the partial series %d, got %+v", partialSeriesID, missing)
	}
}
