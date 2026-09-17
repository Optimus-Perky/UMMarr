package store_test

import (
	"context"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// TestUpdateMovieMonitored proves the movie detail page's clickable
// Monitored/Unmonitored chip actually persists.
func TestUpdateMovieMonitored(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rootFolderID := seedRootFolder(t, db, "movie")
	qualityProfileID := seedQualityProfile(t, db)
	metadataID, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
		Title: metadata.Field[string]{Value: "Inception", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "27205"},
	})
	if err != nil {
		t.Fatalf("upsert movie_metadata: %v", err)
	}
	movieID, err := store.UpsertMovie(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert movie: %v", err)
	}

	if err := store.UpdateMovieMonitored(ctx, db, movieID, false); err != nil {
		t.Fatalf("update movie monitored: %v", err)
	}
	detail, found, err := store.GetMovieDetail(ctx, db, movieID)
	if err != nil || !found {
		t.Fatalf("get movie detail: found=%v err=%v", found, err)
	}
	if detail.Monitored {
		t.Fatalf("want monitored=false after update, got true")
	}
}

// TestUpdateSeriesMonitored mirrors TestUpdateMovieMonitored for series.
func TestUpdateSeriesMonitored(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rootFolderID := seedRootFolder(t, db, "series")
	qualityProfileID := seedQualityProfile(t, db)
	metadataID, err := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{
		Title: metadata.Field[string]{Value: "Breaking Bad", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "1396"},
	})
	if err != nil {
		t.Fatalf("upsert series_metadata: %v", err)
	}
	seriesID, err := store.UpsertSeries(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert series: %v", err)
	}

	if err := store.UpdateSeriesMonitored(ctx, db, seriesID, false); err != nil {
		t.Fatalf("update series monitored: %v", err)
	}
	detail, found, err := store.GetSeriesDetail(ctx, db, seriesID)
	if err != nil || !found {
		t.Fatalf("get series detail: found=%v err=%v", found, err)
	}
	if detail.Monitored {
		t.Fatalf("want monitored=false after update, got true")
	}
}

// TestUpdateSeasonMonitored proves a season's monitored flag can be
// toggled independently of its series' own flag.
func TestUpdateSeasonMonitored(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rootFolderID := seedRootFolder(t, db, "series")
	qualityProfileID := seedQualityProfile(t, db)
	metadataID, err := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{
		Title: metadata.Field[string]{Value: "Breaking Bad", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "1396"},
	})
	if err != nil {
		t.Fatalf("upsert series_metadata: %v", err)
	}
	seriesID, err := store.UpsertSeries(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert series: %v", err)
	}
	if _, err := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: 1}); err != nil {
		t.Fatalf("upsert season: %v", err)
	}

	if err := store.UpdateSeasonMonitored(ctx, db, seriesID, 1, false); err != nil {
		t.Fatalf("update season monitored: %v", err)
	}
	seasons, err := store.ListSeasonsForSeries(ctx, db, seriesID)
	if err != nil {
		t.Fatalf("list seasons: %v", err)
	}
	if len(seasons) != 1 || seasons[0].Monitored {
		t.Fatalf("want season 1 monitored=false after update, got %+v", seasons)
	}
}

// TestUpdateEpisodeMonitored mirrors TestUpdateSeasonMonitored for a
// single episode.
func TestUpdateEpisodeMonitored(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rootFolderID := seedRootFolder(t, db, "series")
	qualityProfileID := seedQualityProfile(t, db)
	metadataID, err := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{
		Title: metadata.Field[string]{Value: "Breaking Bad", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "1396"},
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
	episodeID, err := store.UpsertEpisode(ctx, db, seriesID, seasonID, 1, metadata.EpisodeMetadata{
		EpisodeNumber: 1, Title: metadata.Field[string]{Value: "Pilot", Provider: "tmdb"},
	})
	if err != nil {
		t.Fatalf("upsert episode: %v", err)
	}

	if err := store.UpdateEpisodeMonitored(ctx, db, episodeID, false); err != nil {
		t.Fatalf("update episode monitored: %v", err)
	}
	episodes, err := store.ListEpisodesForSeries(ctx, db, seriesID)
	if err != nil {
		t.Fatalf("list episodes: %v", err)
	}
	if len(episodes) != 1 || episodes[0].Monitored {
		t.Fatalf("want episode 1 monitored=false after update, got %+v", episodes)
	}
}

// TestUpdateAlbumMonitored mirrors TestUpdateMovieMonitored for albums.
func TestUpdateAlbumMonitored(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rootFolderID := seedRootFolder(t, db, "music")
	qualityProfileID := seedQualityProfile(t, db)
	artistMetadataID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name: metadata.Field[string]{Value: "Daft Punk", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "daft-punk-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert artist_metadata: %v", err)
	}
	if _, err := store.UpsertArtist(ctx, db, artistMetadataID, qualityProfileID, rootFolderID, true); err != nil {
		t.Fatalf("upsert artist: %v", err)
	}
	albumID, _, err := store.UpsertAlbum(ctx, db, artistMetadataID, metadata.AlbumMetadata{
		Title: metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "homework-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert album: %v", err)
	}

	if err := store.UpdateAlbumMonitored(ctx, db, albumID, false); err != nil {
		t.Fatalf("update album monitored: %v", err)
	}
	detail, found, err := store.GetAlbumDetail(ctx, db, albumID)
	if err != nil || !found {
		t.Fatalf("get album detail: found=%v err=%v", found, err)
	}
	if detail.Monitored {
		t.Fatalf("want monitored=false after update, got true")
	}
}
