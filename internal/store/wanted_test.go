package store_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestWantedMovie(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	movieID := seedMovie(t, db)

	m, err := store.GetWantedMovie(ctx, db, movieID)
	if err != nil {
		t.Fatalf("get wanted movie: %v", err)
	}
	if m.Title != "Inception" || !m.Monitored || m.HasFile || m.Queued || m.TMDbID != 27205 || m.MinimumAvailability != "released" || !m.QualityProfileID.Valid {
		t.Fatalf("unexpected wanted movie %+v", m)
	}
	if list, _ := store.ListWantedMovies(ctx, db); len(list) != 1 {
		t.Fatalf("want the missing movie listed, got %+v", list)
	}

	grabID, err := store.InsertGrab(ctx, db, store.Grab{MovieID: sql.NullInt64{Int64: movieID, Valid: true}, ReleaseTitle: "x", Indexer: "x", Protocol: "torrent", DownloadClient: "deluge", Status: "downloading"})
	if err != nil {
		t.Fatalf("insert grab: %v", err)
	}
	if m, _ = store.GetWantedMovie(ctx, db, movieID); !m.Queued {
		t.Fatalf("want a downloading grab to count as queued")
	}
	if err := store.UpdateGrabStatus(ctx, db, grabID, "failed", sql.NullString{}); err != nil {
		t.Fatalf("update grab: %v", err)
	}
	if m, _ = store.GetWantedMovie(ctx, db, movieID); m.Queued {
		t.Fatalf("want a failed grab not to count as queued")
	}

	if _, err := store.InsertMovieFile(ctx, db, movieID, "Inception.mkv", 10); err != nil {
		t.Fatalf("insert movie file: %v", err)
	}
	if m, _ = store.GetWantedMovie(ctx, db, movieID); !m.HasFile {
		t.Fatalf("want the file noticed")
	}
	if list, _ := store.ListWantedMovies(ctx, db); len(list) != 1 || !list[0].HasFile {
		t.Fatalf("want a movie with a file still listed (for upgrades), got %+v", list)
	}
}

func TestWantedSeries(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seriesID, episodeID := seedSeriesWithEpisode(t, db)
	seasonID, err := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: 2})
	if err != nil {
		t.Fatalf("upsert season 2: %v", err)
	}
	if _, err := store.UpsertEpisode(ctx, db, seriesID, seasonID, 2, metadata.EpisodeMetadata{EpisodeNumber: 1}); err != nil {
		t.Fatalf("upsert s02e01: %v", err)
	}
	if err := store.UpdateSeasonMonitored(ctx, db, seriesID, 2, false); err != nil {
		t.Fatalf("unmonitor season 2: %v", err)
	}

	s, err := store.GetWantedSeries(ctx, db, seriesID)
	if err != nil {
		t.Fatalf("get wanted series: %v", err)
	}
	e1, ok1 := s.Episode(1, 1)
	e2, ok2 := s.Episode(2, 1)
	if s.Title != "Breaking Bad" || len(s.Episodes) != 2 || !ok1 || !ok2 || !e1.Monitored || e2.Monitored || len(s.Season(2)) != 1 {
		t.Fatalf("want S01E01 monitored and S02E01 unmonitored through its season, got %+v", s)
	}

	if _, err := store.InsertGrab(ctx, db, store.Grab{
		SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}, SeasonNumber: sql.NullInt64{Int64: 1, Valid: true},
		ReleaseTitle: "Breaking Bad S01", Indexer: "x", Protocol: "torrent", DownloadClient: "deluge", Status: "grabbed",
	}); err != nil {
		t.Fatalf("insert season pack grab: %v", err)
	}
	s, _ = store.GetWantedSeries(ctx, db, seriesID)
	if e1, _ = s.Episode(1, 1); !e1.Queued {
		t.Fatalf("want a season pack grab to queue its season's episodes")
	}
	if e2, _ = s.Episode(2, 1); e2.Queued {
		t.Fatalf("want other seasons unaffected")
	}

	if list, _ := store.ListWantedSeries(ctx, db); len(list) != 1 || len(list[0].Episodes) != 2 {
		t.Fatalf("want the series listed with its episodes, got %+v", list)
	}
	if _, err := store.AttachEpisodeFile(ctx, db, episodeID, "s01e01.mkv", 1); err != nil {
		t.Fatalf("attach episode file: %v", err)
	}
	if list, _ := store.ListWantedSeries(ctx, db); len(list) != 1 || list[0].MissingMonitored() {
		t.Fatalf("want the series still listed but with nothing missing, got %+v", list)
	}
}

func TestWantedAlbum(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	albumID, artistID := seedAlbumForRelease(t, db)
	addReleaseWithTracks(t, db, albumID, artistID, "release-mbid", true, 3)

	a, err := store.GetWantedAlbum(ctx, db, albumID)
	if err != nil {
		t.Fatalf("get wanted album: %v", err)
	}
	if a.Artist != "Daft Punk" || a.Title != "Homework" || len(a.Tracks) != 3 || a.FileCount() != 0 || !a.Monitored || a.QualityProfileID.Valid {
		t.Fatalf("unexpected wanted album %+v", a)
	}
	if list, _ := store.ListWantedAlbums(ctx, db); len(list) != 1 {
		t.Fatalf("want the album listed, got %+v", list)
	}
	if _, err := store.AttachTrackFile(ctx, db, a.Tracks[0].ID, "01.flac", 1); err != nil {
		t.Fatalf("attach track file: %v", err)
	}
	if a, _ = store.GetWantedAlbum(ctx, db, albumID); a.FileCount() != 1 {
		t.Fatalf("want one track with a file, got %d", a.FileCount())
	}
	if list, _ := store.ListWantedAlbums(ctx, db); len(list) != 0 {
		t.Fatalf("want an album with files no longer listed as missing, got %+v", list)
	}
}
