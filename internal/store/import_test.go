package store_test

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestResolveMovieFileName(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rootFolderID := seedRootFolder(t, db, "movie")
	qualityProfileID := seedQualityProfile(t, db)
	metadataID, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
		Title:       metadata.Field[string]{Value: "Inception", Provider: "tmdb"},
		Year:        metadata.Field[int]{Value: 2010, Provider: "tmdb"},
		ExternalIDs: map[string]string{"tmdb": "27205"},
	})
	if err != nil {
		t.Fatalf("upsert movie_metadata: %v", err)
	}
	movieID, err := store.UpsertMovie(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert movie: %v", err)
	}

	name, err := store.ResolveMovieFileName(ctx, db, movieID, "download.mkv")
	if err != nil {
		t.Fatalf("resolve movie file name: %v", err)
	}
	if name != "Inception (2010).mkv" {
		t.Fatalf("want 'Inception (2010).mkv', got %q", name)
	}
}

func seedSeriesWithEpisode(t *testing.T, db *sql.DB) (seriesID, episodeID int64) {
	t.Helper()
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
	seriesID, err = store.UpsertSeries(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert series: %v", err)
	}
	seasonID, err := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: 1})
	if err != nil {
		t.Fatalf("upsert season: %v", err)
	}
	episodeID, err = store.UpsertEpisode(ctx, db, seriesID, seasonID, 1, metadata.EpisodeMetadata{
		EpisodeNumber: 1, Title: metadata.Field[string]{Value: "Pilot", Provider: "tmdb"},
	})
	if err != nil {
		t.Fatalf("upsert episode: %v", err)
	}
	return seriesID, episodeID
}

func TestResolveEpisodeFileName(t *testing.T) {
	db := openTestDB(t)
	_, episodeID := seedSeriesWithEpisode(t, db)

	name, err := store.ResolveEpisodeFileName(context.Background(), db, episodeID, "download.mkv")
	if err != nil {
		t.Fatalf("resolve episode file name: %v", err)
	}
	if name != "Breaking Bad - S01E01 - Pilot.mkv" {
		t.Fatalf("want 'Breaking Bad - S01E01 - Pilot.mkv', got %q", name)
	}
}

func TestResolveTrackFileName(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	artistID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name:        metadata.Field[string]{Value: "Daft Punk", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "daft-punk-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert artist: %v", err)
	}
	albumID, _, err := store.UpsertAlbum(ctx, db, artistID, metadata.AlbumMetadata{
		Title:       metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "homework-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert album: %v", err)
	}
	releaseID, err := store.UpsertAlbumRelease(ctx, db, albumID, metadata.ReleaseMetadata{
		Title:       metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "homework-release-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert release: %v", err)
	}
	trackID, err := store.UpsertTrack(ctx, db, releaseID, artistID, metadata.TrackSource{
		Number: "1", Title: "Daftendirekt", MediumNumber: 1,
	})
	if err != nil {
		t.Fatalf("upsert track: %v", err)
	}

	name, err := store.ResolveTrackFileName(ctx, db, trackID, "download.flac")
	if err != nil {
		t.Fatalf("resolve track file name: %v", err)
	}
	if name != "Daft Punk - Homework - 01 - Daftendirekt.flac" {
		t.Fatalf("want 'Daft Punk - Homework - 01 - Daftendirekt.flac', got %q", name)
	}
}

func TestResolveEpisodeFolderPath_WithSeasonFolder(t *testing.T) {
	db := openTestDB(t)
	seriesID, _ := seedSeriesWithEpisode(t, db)

	path, err := store.ResolveEpisodeFolderPath(context.Background(), db, seriesID, 1)
	if err != nil {
		t.Fatalf("resolve episode folder path: %v", err)
	}
	seriesPath, err := store.ResolveSeriesPath(context.Background(), db, seriesID)
	if err != nil {
		t.Fatalf("resolve series path: %v", err)
	}
	want := seriesPath + "/Season 1"
	if path != want {
		t.Fatalf("want %q, got %q", want, path)
	}
}

func TestResolveEpisodeFolderPath_NoSeasonFolder(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seriesID, _ := seedSeriesWithEpisode(t, db)

	if _, err := db.ExecContext(ctx, `UPDATE series SET season_folder = 0 WHERE id = ?`, seriesID); err != nil {
		t.Fatalf("disable season folder: %v", err)
	}

	path, err := store.ResolveEpisodeFolderPath(ctx, db, seriesID, 1)
	if err != nil {
		t.Fatalf("resolve episode folder path: %v", err)
	}
	seriesPath, err := store.ResolveSeriesPath(ctx, db, seriesID)
	if err != nil {
		t.Fatalf("resolve series path: %v", err)
	}
	if path != seriesPath {
		t.Fatalf("want bare series path %q, got %q", seriesPath, path)
	}
}

func TestResolveEpisodeFolderPath_MissingSeasonRow(t *testing.T) {
	db := openTestDB(t)
	seriesID, _ := seedSeriesWithEpisode(t, db)

	// Season 5 was never upserted (no seasons row for it).
	path, err := store.ResolveEpisodeFolderPath(context.Background(), db, seriesID, 5)
	if err != nil {
		t.Fatalf("resolve episode folder path: %v", err)
	}
	seriesPath, err := store.ResolveSeriesPath(context.Background(), db, seriesID)
	if err != nil {
		t.Fatalf("resolve series path: %v", err)
	}
	if path != seriesPath {
		t.Fatalf("want fallback to bare series path %q, got %q", seriesPath, path)
	}
}

func TestFindEpisode_FoundAndNotFound(t *testing.T) {
	db := openTestDB(t)
	seriesID, episodeID := seedSeriesWithEpisode(t, db)

	id, found, err := store.FindEpisode(context.Background(), db, seriesID, 1, 1)
	if err != nil {
		t.Fatalf("find episode: %v", err)
	}
	if !found || id != episodeID {
		t.Fatalf("want found episode %d, got found=%v id=%d", episodeID, found, id)
	}

	_, found, err = store.FindEpisode(context.Background(), db, seriesID, 9, 9)
	if err != nil {
		t.Fatalf("find episode: %v", err)
	}
	if found {
		t.Fatalf("want not found for a nonexistent episode")
	}
}

func TestFindEpisodeMissingFile_ExcludesAlreadyFiledEpisode(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seriesID, episodeID := seedSeriesWithEpisode(t, db)

	// Before any file is attached, FindEpisodeMissingFile should find it.
	id, found, err := store.FindEpisodeMissingFile(ctx, db, seriesID, 1, 1)
	if err != nil {
		t.Fatalf("find episode missing file: %v", err)
	}
	if !found || id != episodeID {
		t.Fatalf("want found episode %d before filing, got found=%v id=%d", episodeID, found, id)
	}

	if _, err := store.AttachEpisodeFile(ctx, db, episodeID, "Ep1.mkv", 100); err != nil {
		t.Fatalf("attach episode file: %v", err)
	}

	// FindEpisode still finds it (it exists)...
	if _, found, err := store.FindEpisode(ctx, db, seriesID, 1, 1); err != nil || !found {
		t.Fatalf("want FindEpisode to still find a filed episode: found=%v err=%v", found, err)
	}
	// ...but FindEpisodeMissingFile must not, now that it has a file.
	_, found, err = store.FindEpisodeMissingFile(ctx, db, seriesID, 1, 1)
	if err != nil {
		t.Fatalf("find episode missing file after filing: %v", err)
	}
	if found {
		t.Fatalf("want not found for an already-filed episode")
	}
}

func TestInsertMovieFile(t *testing.T) {
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

	fileID, err := store.InsertMovieFile(ctx, db, movieID, "Inception (2010).mkv", 12345)
	if err != nil {
		t.Fatalf("insert movie file: %v", err)
	}
	var relativePath string
	var size int64
	if err := db.QueryRowContext(ctx, `SELECT relative_path, size FROM movie_files WHERE id = ?`, fileID).Scan(&relativePath, &size); err != nil {
		t.Fatalf("query movie file: %v", err)
	}
	if relativePath != "Inception (2010).mkv" || size != 12345 {
		t.Fatalf("movie file not recorded correctly: path=%q size=%d", relativePath, size)
	}
}

func TestAttachEpisodeFile_SetsEpisodeFileID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_, episodeID := seedSeriesWithEpisode(t, db)

	fileID, err := store.AttachEpisodeFile(ctx, db, episodeID, "Season 01/Breaking Bad - S01E01 - Pilot.mkv", 999)
	if err != nil {
		t.Fatalf("attach episode file: %v", err)
	}
	var gotFileID int64
	if err := db.QueryRowContext(ctx, `SELECT episode_file_id FROM episodes WHERE id = ?`, episodeID).Scan(&gotFileID); err != nil {
		t.Fatalf("query episode: %v", err)
	}
	if gotFileID != fileID {
		t.Fatalf("want episode_file_id %d, got %d", fileID, gotFileID)
	}
}

func TestAttachTrackFile_SetsTrackFileID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	artistID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name: metadata.Field[string]{Value: "Daft Punk", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "dp-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert artist: %v", err)
	}
	albumID, _, err := store.UpsertAlbum(ctx, db, artistID, metadata.AlbumMetadata{
		Title: metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "hw-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert album: %v", err)
	}
	releaseID, err := store.UpsertAlbumRelease(ctx, db, albumID, metadata.ReleaseMetadata{
		Title: metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "hw-release-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert release: %v", err)
	}
	trackID, err := store.UpsertTrack(ctx, db, releaseID, artistID, metadata.TrackSource{Number: "1", Title: "Daftendirekt", MediumNumber: 1})
	if err != nil {
		t.Fatalf("upsert track: %v", err)
	}

	fileID, err := store.AttachTrackFile(ctx, db, trackID, "Daft Punk - Homework - 01 - Daftendirekt.flac", 555)
	if err != nil {
		t.Fatalf("attach track file: %v", err)
	}
	var gotFileID int64
	if err := db.QueryRowContext(ctx, `SELECT track_file_id FROM tracks WHERE id = ?`, trackID).Scan(&gotFileID); err != nil {
		t.Fatalf("query track: %v", err)
	}
	if gotFileID != fileID {
		t.Fatalf("want track_file_id %d, got %d", fileID, gotFileID)
	}
}

func seedAlbumForRelease(t *testing.T, db *sql.DB) (albumID, artistID int64) {
	t.Helper()
	ctx := context.Background()
	artistID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name: metadata.Field[string]{Value: "Daft Punk", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "dp-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert artist: %v", err)
	}
	albumID, _, err = store.UpsertAlbum(ctx, db, artistID, metadata.AlbumMetadata{
		Title: metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "hw-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert album: %v", err)
	}
	return albumID, artistID
}

func addReleaseWithTracks(t *testing.T, db *sql.DB, albumID, artistID int64, mbid string, monitored bool, trackCount int) int64 {
	t.Helper()
	ctx := context.Background()
	releaseID, err := store.UpsertAlbumRelease(ctx, db, albumID, metadata.ReleaseMetadata{
		Title: metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": mbid},
	})
	if err != nil {
		t.Fatalf("upsert release %s: %v", mbid, err)
	}
	if !monitored {
		if _, err := db.ExecContext(ctx, `UPDATE album_releases SET monitored = 0 WHERE id = ?`, releaseID); err != nil {
			t.Fatalf("unmonitor release: %v", err)
		}
	}
	for i := 1; i <= trackCount; i++ {
		if _, err := store.UpsertTrack(ctx, db, releaseID, artistID, metadata.TrackSource{
			Number: strconv.Itoa(i), Title: "Track " + strconv.Itoa(i), MediumNumber: 1,
		}); err != nil {
			t.Fatalf("upsert track %d: %v", i, err)
		}
	}
	return releaseID
}

func TestFindImportRelease_PrefersMonitored(t *testing.T) {
	db := openTestDB(t)
	albumID, artistID := seedAlbumForRelease(t, db)

	addReleaseWithTracks(t, db, albumID, artistID, "unmonitored-but-bigger", false, 5)
	monitoredID := addReleaseWithTracks(t, db, albumID, artistID, "monitored-smaller", true, 2)

	releaseID, tracks, err := store.FindImportRelease(context.Background(), db, albumID)
	if err != nil {
		t.Fatalf("find import release: %v", err)
	}
	if releaseID != monitoredID {
		t.Fatalf("want the monitored release %d to win, got %d", monitoredID, releaseID)
	}
	if len(tracks) != 2 {
		t.Fatalf("want 2 tracks, got %d", len(tracks))
	}
}

func TestFindImportRelease_FallsBackWhenNoneMonitored(t *testing.T) {
	db := openTestDB(t)
	albumID, artistID := seedAlbumForRelease(t, db)

	addReleaseWithTracks(t, db, albumID, artistID, "smaller", false, 2)
	biggerID := addReleaseWithTracks(t, db, albumID, artistID, "bigger", false, 5)

	releaseID, tracks, err := store.FindImportRelease(context.Background(), db, albumID)
	if err != nil {
		t.Fatalf("find import release: %v", err)
	}
	if releaseID != biggerID {
		t.Fatalf("want the higher-track-count release %d to win, got %d", biggerID, releaseID)
	}
	if len(tracks) != 5 {
		t.Fatalf("want 5 tracks, got %d", len(tracks))
	}
}

func TestFindImportRelease_NoReleases(t *testing.T) {
	db := openTestDB(t)
	albumID, _ := seedAlbumForRelease(t, db)

	_, _, err := store.FindImportRelease(context.Background(), db, albumID)
	if !errors.Is(err, store.ErrNoAlbumRelease) {
		t.Fatalf("want ErrNoAlbumRelease, got %v", err)
	}
}

func TestFindImportRelease_TrackImportInfoReflectsHasFile(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	albumID, artistID := seedAlbumForRelease(t, db)
	addReleaseWithTracks(t, db, albumID, artistID, "release-mbid", true, 2)

	_, tracks, err := store.FindImportRelease(ctx, db, albumID)
	if err != nil {
		t.Fatalf("find import release: %v", err)
	}
	if len(tracks) != 2 {
		t.Fatalf("want 2 tracks, got %d", len(tracks))
	}
	for _, tr := range tracks {
		if tr.HasFile {
			t.Fatalf("want HasFile=false before attaching any file, got true for track %d", tr.ID)
		}
	}

	filedTrackID := tracks[0].ID
	if _, err := store.AttachTrackFile(ctx, db, filedTrackID, "track1.flac", 100); err != nil {
		t.Fatalf("attach track file: %v", err)
	}

	_, tracks, err = store.FindImportRelease(ctx, db, albumID)
	if err != nil {
		t.Fatalf("find import release after attach: %v", err)
	}
	for _, tr := range tracks {
		want := tr.ID == filedTrackID
		if tr.HasFile != want {
			t.Fatalf("track %d: want HasFile=%v, got %v", tr.ID, want, tr.HasFile)
		}
	}
}

// TestListTrackFilesForAlbum runs the query against the real migrated
// schema. track_files has no column pointing back at its track, and an
// earlier version joined on one that doesn't exist - which only surfaced
// at runtime, because the one caller swallowed the error.
func TestListTrackFilesForAlbum(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	artistID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name: metadata.Field[string]{Value: "Daft Punk", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "dp-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert artist: %v", err)
	}
	albumID, _, err := store.UpsertAlbum(ctx, db, artistID, metadata.AlbumMetadata{
		Title: metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "hw-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert album: %v", err)
	}
	releaseID, err := store.UpsertAlbumRelease(ctx, db, albumID, metadata.ReleaseMetadata{
		Title: metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "hw-release-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert release: %v", err)
	}
	filed, err := store.UpsertTrack(ctx, db, releaseID, artistID, metadata.TrackSource{Number: "1", Title: "Daftendirekt", MediumNumber: 1})
	if err != nil {
		t.Fatalf("upsert track 1: %v", err)
	}
	if _, err := store.UpsertTrack(ctx, db, releaseID, artistID, metadata.TrackSource{Number: "2", Title: "WDPK 83.7 FM", MediumNumber: 1}); err != nil {
		t.Fatalf("upsert track 2: %v", err)
	}
	fileID, err := store.AttachTrackFile(ctx, db, filed, "01 - Daftendirekt.flac", 555)
	if err != nil {
		t.Fatalf("attach track file: %v", err)
	}

	refs, err := store.ListTrackFilesForAlbum(ctx, db, albumID)
	if err != nil {
		t.Fatalf("list track files: %v", err)
	}
	if len(refs) != 1 || refs[0].ID != fileID || refs[0].RelativePath != "01 - Daftendirekt.flac" {
		t.Fatalf("want exactly the one attached file (id %d), got %+v", fileID, refs)
	}
}
