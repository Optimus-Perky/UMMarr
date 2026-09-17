package store_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestGetMovieDetail_ReturnsFullView(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rootFolderID := seedRootFolder(t, db, "movie")
	qualityProfileID := seedQualityProfile(t, db)

	metadataID, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
		Title: metadata.Field[string]{Value: "Inception", Provider: "tmdb"}, Year: metadata.Field[int]{Value: 2010, Provider: "tmdb"},
		Overview:    metadata.Field[string]{Value: "A thief who steals corporate secrets...", Provider: "tmdb"},
		Genres:      metadata.Field[[]string]{Value: []string{"Action", "Sci-Fi"}, Provider: "tmdb"},
		Images:      metadata.Field[[]string]{Value: []string{"https://image.tmdb.org/poster.jpg"}, Provider: "tmdb"},
		Ratings:     map[string]float64{"tmdb": 8.4},
		ExternalIDs: map[string]string{"tmdb": "27205"},
	})
	if err != nil {
		t.Fatalf("upsert movie_metadata: %v", err)
	}
	movieID, err := store.UpsertMovie(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert movie: %v", err)
	}

	detail, found, err := store.GetMovieDetail(ctx, db, movieID)
	if err != nil {
		t.Fatalf("get movie detail: %v", err)
	}
	if !found {
		t.Fatalf("want found=true")
	}
	if detail.Title != "Inception" || detail.Overview.String != "A thief who steals corporate secrets..." {
		t.Fatalf("unexpected detail: %+v", detail)
	}
	if len(detail.Genres) != 2 || detail.Genres[0] != "Action" {
		t.Fatalf("want genres [Action Sci-Fi], got %v", detail.Genres)
	}
	if detail.PosterURL != "https://image.tmdb.org/poster.jpg" {
		t.Fatalf("want poster url populated from images[0], got %q", detail.PosterURL)
	}
	if detail.Ratings["tmdb"] != 8.4 {
		t.Fatalf("want tmdb rating 8.4, got %v", detail.Ratings)
	}
	if !detail.QualityProfileName.Valid {
		t.Fatalf("want quality profile name joined in")
	}
	if detail.File != nil {
		t.Fatalf("want no file yet, got %+v", detail.File)
	}

	if _, err := store.InsertMovieFile(ctx, db, movieID, "Inception (2010).mkv", 12345); err != nil {
		t.Fatalf("insert movie file: %v", err)
	}
	detail2, _, err := store.GetMovieDetail(ctx, db, movieID)
	if err != nil {
		t.Fatalf("get movie detail after file: %v", err)
	}
	if detail2.File == nil || detail2.File.RelativePath != "Inception (2010).mkv" || detail2.File.Size.Int64 != 12345 {
		t.Fatalf("want file info populated, got %+v", detail2.File)
	}
}

func TestGetMovieDetail_NotFound(t *testing.T) {
	db := openTestDB(t)
	_, found, err := store.GetMovieDetail(context.Background(), db, 999)
	if err != nil {
		t.Fatalf("get movie detail: %v", err)
	}
	if found {
		t.Fatalf("want found=false for a nonexistent movie id")
	}
}

func TestGetSeriesDetail_AndSeasonEpisodeListing(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rootFolderID := seedRootFolder(t, db, "series")
	qualityProfileID := seedQualityProfile(t, db)

	metadataID, err := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{
		Title:       metadata.Field[string]{Value: "Breaking Bad", Provider: "tmdb"},
		Genres:      metadata.Field[[]string]{Value: []string{"Drama"}, Provider: "tmdb"},
		ExternalIDs: map[string]string{"tmdb": "1396"},
	})
	if err != nil {
		t.Fatalf("upsert series_metadata: %v", err)
	}
	seriesID, err := store.UpsertSeries(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert series: %v", err)
	}

	detail, found, err := store.GetSeriesDetail(ctx, db, seriesID)
	if err != nil {
		t.Fatalf("get series detail: %v", err)
	}
	if !found || detail.Title != "Breaking Bad" || len(detail.Genres) != 1 {
		t.Fatalf("unexpected series detail: %+v found=%v", detail, found)
	}

	season1ID, err := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: 1})
	if err != nil {
		t.Fatalf("upsert season 1: %v", err)
	}
	ep1ID, err := store.UpsertEpisode(ctx, db, seriesID, season1ID, 1, metadata.EpisodeMetadata{
		EpisodeNumber: 1, Title: metadata.Field[string]{Value: "Pilot", Provider: "tmdb"},
	})
	if err != nil {
		t.Fatalf("upsert episode 1: %v", err)
	}
	if _, err := store.UpsertEpisode(ctx, db, seriesID, season1ID, 1, metadata.EpisodeMetadata{
		EpisodeNumber: 2, Title: metadata.Field[string]{Value: "Cat's in the Bag...", Provider: "tmdb"},
	}); err != nil {
		t.Fatalf("upsert episode 2: %v", err)
	}
	if err := store.WithTx(ctx, db, func(tx *sql.Tx) error {
		_, err := store.AttachEpisodeFile(ctx, tx, ep1ID, "Season 01/Breaking Bad - S01E01 - Pilot.mkv", 999)
		return err
	}); err != nil {
		t.Fatalf("attach episode file: %v", err)
	}

	seasons, err := store.ListSeasonsForSeries(ctx, db, seriesID)
	if err != nil {
		t.Fatalf("list seasons: %v", err)
	}
	if len(seasons) != 1 || seasons[0].Total != 2 || seasons[0].Downloaded != 1 {
		t.Fatalf("want 1 season with 2 total/1 downloaded, got %+v", seasons)
	}

	episodes, err := store.ListEpisodesForSeries(ctx, db, seriesID)
	if err != nil {
		t.Fatalf("list episodes: %v", err)
	}
	if len(episodes) != 2 {
		t.Fatalf("want 2 episodes, got %d", len(episodes))
	}
	if episodes[0].EpisodeNumber != 1 || !episodes[0].HasFile || episodes[0].FileSize.Int64 != 999 {
		t.Fatalf("want episode 1 filed with size 999, got %+v", episodes[0])
	}
	if episodes[1].EpisodeNumber != 2 || episodes[1].HasFile {
		t.Fatalf("want episode 2 unfiled, got %+v", episodes[1])
	}
}

func TestGetAlbumDetail_AndTrackListing(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rootFolderID := seedRootFolder(t, db, "music")
	qualityProfileID := seedQualityProfile(t, db)

	artistMetadataID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name:        metadata.Field[string]{Value: "Daft Punk", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "daft-punk-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert artist_metadata: %v", err)
	}
	if _, err := store.UpsertArtist(ctx, db, artistMetadataID, qualityProfileID, rootFolderID, true); err != nil {
		t.Fatalf("upsert artist: %v", err)
	}

	albumID, _, err := store.UpsertAlbum(ctx, db, artistMetadataID, metadata.AlbumMetadata{
		Title:       metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"},
		Genres:      metadata.Field[[]string]{Value: []string{"Electronic"}, Provider: "musicbrainz"},
		Images:      metadata.Field[[]string]{Value: []string{"https://coverartarchive.org/homework.jpg"}, Provider: "musicbrainz"},
		Ratings:     map[string]float64{"musicbrainz": 4.5},
		ExternalIDs: map[string]string{"musicbrainz": "homework-rg-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert album: %v", err)
	}
	releaseID, err := store.UpsertAlbumRelease(ctx, db, albumID, metadata.ReleaseMetadata{
		Title:       metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "homework-release-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert album_release: %v", err)
	}
	track1ID, err := store.UpsertTrack(ctx, db, releaseID, artistMetadataID, metadata.TrackSource{
		Number: "1", Title: "Daftendirekt", DurationMs: 210000, MediumNumber: 1,
	})
	if err != nil {
		t.Fatalf("upsert track 1: %v", err)
	}
	if _, err := store.UpsertTrack(ctx, db, releaseID, artistMetadataID, metadata.TrackSource{
		Number: "2", Title: "WDPK 83.7 FM", DurationMs: 20000, MediumNumber: 1,
	}); err != nil {
		t.Fatalf("upsert track 2: %v", err)
	}
	if err := store.WithTx(ctx, db, func(tx *sql.Tx) error {
		_, err := store.AttachTrackFile(ctx, tx, track1ID, "Daft Punk - Homework - 01 - Daftendirekt.flac", 555)
		return err
	}); err != nil {
		t.Fatalf("attach track file: %v", err)
	}

	detail, found, err := store.GetAlbumDetail(ctx, db, albumID)
	if err != nil {
		t.Fatalf("get album detail: %v", err)
	}
	if !found || detail.Title != "Homework" || detail.ArtistName != "Daft Punk" {
		t.Fatalf("unexpected album detail: %+v found=%v", detail, found)
	}
	if len(detail.Genres) != 1 || detail.Genres[0] != "Electronic" {
		t.Fatalf("want genres [Electronic], got %v", detail.Genres)
	}
	if detail.PosterURL != "https://coverartarchive.org/homework.jpg" {
		t.Fatalf("want cover art url, got %q", detail.PosterURL)
	}
	if !detail.ArtistID.Valid || !detail.QualityProfileName.Valid {
		t.Fatalf("want artist/quality-profile joined in via the tracked artists row, got %+v", detail)
	}

	tracks, err := store.ListTracksForAlbum(ctx, db, albumID)
	if err != nil {
		t.Fatalf("list tracks: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("want 2 tracks, got %d", len(tracks))
	}
	if tracks[0].Title != "Daftendirekt" || !tracks[0].HasFile || tracks[0].FileSize.Int64 != 555 {
		t.Fatalf("want track 1 filed with size 555, got %+v", tracks[0])
	}
	if tracks[1].Title != "WDPK 83.7 FM" || tracks[1].HasFile {
		t.Fatalf("want track 2 unfiled, got %+v", tracks[1])
	}
}
