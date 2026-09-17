package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedRootFolder(t *testing.T, db *sql.DB, mediaType string) int64 {
	t.Helper()
	// Points at a real, writable t.TempDir() - unlike internal/store's own
	// test helper of the same name (which uses a fixed "/media/<type>"
	// string), this package's tests actually copy files into the
	// resolved folder, so it needs to exist on disk.
	id, err := store.CreateRootFolder(context.Background(), db, t.TempDir(), mediaType)
	if err != nil {
		t.Fatalf("seed root folder: %v", err)
	}
	return id
}

func seedQualityProfile(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	id, err := store.CreateQualityProfile(context.Background(), db, "Any")
	if err != nil {
		t.Fatalf("seed quality profile: %v", err)
	}
	return id
}

func writeDownloadFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", relPath, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func seedMovie(t *testing.T, db *sql.DB) int64 {
	t.Helper()
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
	return movieID
}

func TestImport_Movie_PicksLargestVideoFile(t *testing.T) {
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	downloadDir := t.TempDir()
	writeDownloadFile(t, downloadDir, "sample.mkv", "tiny")
	writeDownloadFile(t, downloadDir, "Inception.2010.1080p.mkv", "the real movie bytes")

	svc := &ImportService{DB: db}
	status, message := svc.Import(context.Background(), store.Grab{MovieID: sql.NullInt64{Int64: movieID, Valid: true}},
		[]importer.File{{Path: "sample.mkv", Size: 4}, {Path: "Inception.2010.1080p.mkv", Size: 20}}, downloadDir)

	if status != "imported" {
		t.Fatalf("want imported, got %q (%s)", status, message)
	}

	moviePath, err := store.ResolveMoviePath(context.Background(), db, movieID)
	if err != nil {
		t.Fatalf("resolve movie path: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(moviePath, "Inception (2010).mkv"))
	if err != nil {
		t.Fatalf("read imported file: %v", err)
	}
	if string(data) != "the real movie bytes" {
		t.Fatalf("want the large file's content copied, got %q", data)
	}
	if _, err := os.Stat(filepath.Join(downloadDir, "Inception.2010.1080p.mkv")); err != nil {
		t.Fatalf("want the original download file to still exist (copy, not move): %v", err)
	}
}

// TestImport_Movie_PersistsParsedQuality proves quality/release-group
// gets parsed from the downloaded file's own (pre-rename) name and saved
// into movie_files.quality - a column that has existed since the
// original schema but was never written to before this pass.
func TestImport_Movie_PersistsParsedQuality(t *testing.T) {
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	downloadDir := t.TempDir()
	writeDownloadFile(t, downloadDir, "Inception 2010 BluRay 1080p x265-hallowed.mkv", "movie bytes")

	svc := &ImportService{DB: db}
	status, message := svc.Import(context.Background(), store.Grab{MovieID: sql.NullInt64{Int64: movieID, Valid: true}},
		[]importer.File{{Path: "Inception 2010 BluRay 1080p x265-hallowed.mkv", Size: 20}}, downloadDir)
	if status != "imported" {
		t.Fatalf("want imported, got %q (%s)", status, message)
	}

	var quality string
	if err := db.QueryRowContext(context.Background(), `SELECT quality FROM movie_files WHERE movie_id = ?`, movieID).Scan(&quality); err != nil {
		t.Fatalf("query movie_files quality: %v", err)
	}
	if !strings.Contains(quality, `"source":"Bluray"`) || !strings.Contains(quality, `"resolution":"1080p"`) || !strings.Contains(quality, `"releaseGroup":"hallowed"`) {
		t.Fatalf("want quality JSON to reflect Bluray/1080p/hallowed, got %s", quality)
	}
}

func TestImport_Movie_NoVideoFileNoArchive(t *testing.T) {
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	downloadDir := t.TempDir()
	writeDownloadFile(t, downloadDir, "readme.nfo", "not a video")

	svc := &ImportService{DB: db}
	status, _ := svc.Import(context.Background(), store.Grab{MovieID: sql.NullInt64{Int64: movieID, Valid: true}},
		[]importer.File{{Path: "readme.nfo", Size: 4}}, downloadDir)
	if status != "import_failed" {
		t.Fatalf("want import_failed, got %q", status)
	}
}

func TestImport_Movie_ArchiveOnlyNeedsExtraction(t *testing.T) {
	db := openTestDB(t)
	movieID := seedMovie(t, db)

	svc := &ImportService{DB: db}
	status, _ := svc.Import(context.Background(), store.Grab{MovieID: sql.NullInt64{Int64: movieID, Valid: true}},
		[]importer.File{{Path: "release.zip", Size: 1000}}, t.TempDir())
	if status != "needs_extraction" {
		t.Fatalf("want needs_extraction, got %q", status)
	}
}

func seedSeries(t *testing.T, db *sql.DB) int64 {
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
	seriesID, err := store.UpsertSeries(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert series: %v", err)
	}
	for _, season := range []int{1, 2} {
		seasonID, err := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: season})
		if err != nil {
			t.Fatalf("upsert season %d: %v", season, err)
		}
		for ep := 1; ep <= 2; ep++ {
			if _, err := store.UpsertEpisode(ctx, db, seriesID, seasonID, season, metadata.EpisodeMetadata{
				EpisodeNumber: ep, Title: metadata.Field[string]{Value: "Ep", Provider: "tmdb"},
			}); err != nil {
				t.Fatalf("upsert episode s%de%d: %v", season, ep, err)
			}
		}
	}
	return seriesID
}

func TestImport_Series_WholeSeriesGrab_MatchesEachFileToItsEpisode(t *testing.T) {
	db := openTestDB(t)
	seriesID := seedSeries(t, db)
	downloadDir := t.TempDir()
	writeDownloadFile(t, downloadDir, "Season 01/Show.S01E01.mkv", "s1e1")
	writeDownloadFile(t, downloadDir, "Season 01/Show.S01E02.mkv", "s1e2")
	writeDownloadFile(t, downloadDir, "Season 02/Show.S02E01.mkv", "s2e1")
	writeDownloadFile(t, downloadDir, "Extras/behindthescenes.mkv", "extra")

	svc := &ImportService{DB: db}
	status, message := svc.Import(context.Background(), store.Grab{SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}},
		[]importer.File{
			{Path: "Season 01/Show.S01E01.mkv", Size: 4}, {Path: "Season 01/Show.S01E02.mkv", Size: 4},
			{Path: "Season 02/Show.S02E01.mkv", Size: 4}, {Path: "Extras/behindthescenes.mkv", Size: 5},
		}, downloadDir)

	if status != "imported" {
		t.Fatalf("want imported, got %q (%s)", status, message)
	}

	total, downloaded, err := store.SeriesEpisodeCounts(context.Background(), db, seriesID)
	if err != nil {
		t.Fatalf("series episode counts: %v", err)
	}
	if total != 4 || downloaded != 3 {
		t.Fatalf("want 3 of 4 episodes to have files, got total=%d downloaded=%d", total, downloaded)
	}

	if _, err := os.Stat(filepath.Join(downloadDir, "Extras/behindthescenes.mkv")); err != nil {
		t.Fatalf("want the unmatched extras file to still exist: %v", err)
	}
}

// TestImport_Series_PersistsParsedQualityPerEpisode mirrors
// TestImport_Movie_PersistsParsedQuality for episode_files - each
// episode's own downloaded filename is what gets parsed, not the
// series-wide grab release title, so a season pack with mixed-quality
// releases (unusual but possible) would still be recorded per-episode.
func TestImport_Series_PersistsParsedQualityPerEpisode(t *testing.T) {
	db := openTestDB(t)
	seriesID := seedSeries(t, db)
	downloadDir := t.TempDir()
	writeDownloadFile(t, downloadDir, "Season 01/Show.S01E01.WEBDL.720p-OnlyWeb.mkv", "s1e1")

	svc := &ImportService{DB: db}
	status, message := svc.Import(context.Background(), store.Grab{SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}},
		[]importer.File{{Path: "Season 01/Show.S01E01.WEBDL.720p-OnlyWeb.mkv", Size: 4}}, downloadDir)
	if status != "imported" {
		t.Fatalf("want imported, got %q (%s)", status, message)
	}

	episodeID, found, err := store.FindEpisode(context.Background(), db, seriesID, 1, 1)
	if err != nil || !found {
		t.Fatalf("find S01E01: found=%v err=%v", found, err)
	}
	var quality string
	if err := db.QueryRowContext(context.Background(), `
		SELECT ef.quality FROM episodes e JOIN episode_files ef ON ef.id = e.episode_file_id WHERE e.id = ?
	`, episodeID).Scan(&quality); err != nil {
		t.Fatalf("query episode_files quality: %v", err)
	}
	if !strings.Contains(quality, `"source":"WEBDL"`) || !strings.Contains(quality, `"resolution":"720p"`) || !strings.Contains(quality, `"releaseGroup":"OnlyWeb"`) {
		t.Fatalf("want quality JSON to reflect WEBDL/720p/OnlyWeb, got %s", quality)
	}
}

func TestImport_Series_NoMatches(t *testing.T) {
	db := openTestDB(t)
	seriesID := seedSeries(t, db)

	svc := &ImportService{DB: db}
	status, _ := svc.Import(context.Background(), store.Grab{SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}},
		[]importer.File{{Path: "Show.Unparseable.mkv", Size: 4}}, t.TempDir())
	if status != "import_failed" {
		t.Fatalf("want import_failed, got %q", status)
	}
}

func seedAlbumWithRelease(t *testing.T, db *sql.DB, trackCount int) int64 {
	t.Helper()
	ctx := context.Background()
	rootFolderID := seedRootFolder(t, db, "music")
	qualityProfileID := seedQualityProfile(t, db)
	artistID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name: metadata.Field[string]{Value: "Daft Punk", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "dp-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert artist metadata: %v", err)
	}
	if _, err := store.UpsertArtist(ctx, db, artistID, qualityProfileID, rootFolderID, true); err != nil {
		t.Fatalf("upsert artist: %v", err)
	}
	albumID, _, err := store.UpsertAlbum(ctx, db, artistID, metadata.AlbumMetadata{
		Title: metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "hw-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert album: %v", err)
	}
	if err := store.SetAlbumPath(ctx, db, albumID); err != nil {
		t.Fatalf("set album path: %v", err)
	}
	releaseID, err := store.UpsertAlbumRelease(ctx, db, albumID, metadata.ReleaseMetadata{
		Title: metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "hw-release-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert release: %v", err)
	}
	for i := 1; i <= trackCount; i++ {
		if _, err := store.UpsertTrack(ctx, db, releaseID, artistID, metadata.TrackSource{
			Number: strconv.Itoa(i), Title: "Track " + strconv.Itoa(i), MediumNumber: 1,
		}); err != nil {
			t.Fatalf("upsert track %d: %v", i, err)
		}
	}
	return albumID
}

func TestImport_Album_PositionalMatchAgainstRelease(t *testing.T) {
	db := openTestDB(t)
	albumID := seedAlbumWithRelease(t, db, 3)
	downloadDir := t.TempDir()
	writeDownloadFile(t, downloadDir, "01 - A.flac", "a")
	writeDownloadFile(t, downloadDir, "02 - B.flac", "b")
	writeDownloadFile(t, downloadDir, "03 - C.flac", "c")

	svc := &ImportService{DB: db}
	status, message := svc.Import(context.Background(), store.Grab{AlbumID: sql.NullInt64{Int64: albumID, Valid: true}},
		[]importer.File{{Path: "01 - A.flac", Size: 1}, {Path: "02 - B.flac", Size: 1}, {Path: "03 - C.flac", Size: 1}}, downloadDir)

	if status != "imported" {
		t.Fatalf("want imported, got %q (%s)", status, message)
	}
	var trackFileCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM track_files`).Scan(&trackFileCount); err != nil {
		t.Fatalf("count track_files: %v", err)
	}
	if trackFileCount != 3 {
		t.Fatalf("want 3 track_files rows, got %d", trackFileCount)
	}
}

// TestImport_Track_AttachesToExactTrackNotPositionalMatch proves
// importTrack's whole reason for existing: a single-track grab for
// "Track 2" must attach the downloaded file to Track 2 specifically, not
// to whichever track importAlbum's positional match would pick (Track 1,
// since it's the only track downloaded and would sort first).
func TestImport_Track_AttachesToExactTrackNotPositionalMatch(t *testing.T) {
	db := openTestDB(t)
	albumID := seedAlbumWithRelease(t, db, 3)
	var track1ID, track2ID int64
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM tracks WHERE title = 'Track 1'`).Scan(&track1ID); err != nil {
		t.Fatalf("find track 1: %v", err)
	}
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM tracks WHERE title = 'Track 2'`).Scan(&track2ID); err != nil {
		t.Fatalf("find track 2: %v", err)
	}

	downloadDir := t.TempDir()
	writeDownloadFile(t, downloadDir, "Track 2.flac", "track 2 bytes")

	svc := &ImportService{DB: db}
	status, message := svc.Import(context.Background(), store.Grab{
		AlbumID: sql.NullInt64{Int64: albumID, Valid: true}, TrackID: sql.NullInt64{Int64: track2ID, Valid: true},
	}, []importer.File{{Path: "Track 2.flac", Size: 13}}, downloadDir)
	if status != "imported" {
		t.Fatalf("want imported, got %q (%s)", status, message)
	}

	var track1HasFile, track2HasFile bool
	if err := db.QueryRowContext(context.Background(), `SELECT track_file_id IS NOT NULL FROM tracks WHERE id = ?`, track1ID).Scan(&track1HasFile); err != nil {
		t.Fatalf("query track 1: %v", err)
	}
	if err := db.QueryRowContext(context.Background(), `SELECT track_file_id IS NOT NULL FROM tracks WHERE id = ?`, track2ID).Scan(&track2HasFile); err != nil {
		t.Fatalf("query track 2: %v", err)
	}
	if track1HasFile {
		t.Fatalf("want Track 1 to remain unfiled - the file was for Track 2, not a positional match")
	}
	if !track2HasFile {
		t.Fatalf("want Track 2 to have the imported file attached")
	}
}

func TestImport_Album_FileCountMismatchStillImportsMinLength(t *testing.T) {
	db := openTestDB(t)
	albumID := seedAlbumWithRelease(t, db, 3)
	downloadDir := t.TempDir()
	writeDownloadFile(t, downloadDir, "01 - A.flac", "a")
	writeDownloadFile(t, downloadDir, "02 - B.flac", "b")

	svc := &ImportService{DB: db}
	status, message := svc.Import(context.Background(), store.Grab{AlbumID: sql.NullInt64{Int64: albumID, Valid: true}},
		[]importer.File{{Path: "01 - A.flac", Size: 1}, {Path: "02 - B.flac", Size: 1}}, downloadDir)

	if status != "imported" {
		t.Fatalf("want imported (partial), got %q (%s)", status, message)
	}
	var trackFileCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM track_files`).Scan(&trackFileCount); err != nil {
		t.Fatalf("count track_files: %v", err)
	}
	if trackFileCount != 2 {
		t.Fatalf("want min(2,3)=2 track_files rows, got %d", trackFileCount)
	}
}

func TestImport_Album_NoAudioFiles(t *testing.T) {
	db := openTestDB(t)
	albumID := seedAlbumWithRelease(t, db, 3)

	svc := &ImportService{DB: db}
	status, _ := svc.Import(context.Background(), store.Grab{AlbumID: sql.NullInt64{Int64: albumID, Valid: true}},
		[]importer.File{{Path: "cover.jpg", Size: 1}}, t.TempDir())
	if status != "import_failed" {
		t.Fatalf("want import_failed, got %q", status)
	}
}

func TestImport_UnknownGrabKind(t *testing.T) {
	db := openTestDB(t)
	svc := &ImportService{DB: db}
	status, _ := svc.Import(context.Background(), store.Grab{}, nil, t.TempDir())
	if status != "import_failed" {
		t.Fatalf("want import_failed for a grab with no target, got %q", status)
	}
}

func TestScanMovieLibrary_ImportsAlreadyOrganizedFile(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	movieID := seedMovie(t, db)

	moviePath, err := store.ResolveMoviePath(ctx, db, movieID)
	if err != nil {
		t.Fatalf("resolve movie path: %v", err)
	}
	name, err := store.ResolveMovieFileName(ctx, db, movieID, "download.mkv")
	if err != nil {
		t.Fatalf("resolve movie file name: %v", err)
	}
	want := "already-organized movie bytes"
	writeDownloadFile(t, moviePath, name, want)

	svc := &ImportService{DB: db}
	imported, err := svc.ScanMovieLibrary(ctx)
	if err != nil {
		t.Fatalf("scan movie library: %v", err)
	}
	if imported != 1 {
		t.Fatalf("want 1 imported, got %d", imported)
	}

	got, err := os.ReadFile(filepath.Join(moviePath, name))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != want {
		t.Fatalf("want the already-correctly-named file untouched (same-path guard), got %q", got)
	}

	var fileID sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT id FROM movie_files WHERE movie_id = ?`, movieID).Scan(&fileID); err != nil {
		t.Fatalf("query movie_files: %v", err)
	}
}

func TestScanMovieLibrary_RenamesDifferentlyNamedFile(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	movieID := seedMovie(t, db)

	moviePath, err := store.ResolveMoviePath(ctx, db, movieID)
	if err != nil {
		t.Fatalf("resolve movie path: %v", err)
	}
	writeDownloadFile(t, moviePath, "Inception.2010.1080p.WEB.mkv", "movie bytes")

	svc := &ImportService{DB: db}
	imported, err := svc.ScanMovieLibrary(ctx)
	if err != nil {
		t.Fatalf("scan movie library: %v", err)
	}
	if imported != 1 {
		t.Fatalf("want 1 imported, got %d", imported)
	}

	name, err := store.ResolveMovieFileName(ctx, db, movieID, "download.mkv")
	if err != nil {
		t.Fatalf("resolve movie file name: %v", err)
	}
	if _, err := os.Stat(filepath.Join(moviePath, name)); err != nil {
		t.Fatalf("want the renamed file to exist at %s: %v", name, err)
	}
}

// TestScanMovie_ImportsSingleMovieOnDemand is the "Refresh" button's
// backing method - proves it finds a file sitting directly in one
// movie's own folder that a normal grab/import never went through UMMarr
// for (the user's real "the file exists but I can't point to it" report).
func TestScanMovie_ImportsSingleMovieOnDemand(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	movieID := seedMovie(t, db)

	moviePath, err := store.ResolveMoviePath(ctx, db, movieID)
	if err != nil {
		t.Fatalf("resolve movie path: %v", err)
	}
	writeDownloadFile(t, moviePath, "Inception.2010.1080p.WEB.mkv", "movie bytes")

	svc := &ImportService{DB: db}
	imported, err := svc.ScanMovie(ctx, movieID)
	if err != nil {
		t.Fatalf("scan movie: %v", err)
	}
	if !imported {
		t.Fatalf("want imported=true")
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM movie_files WHERE movie_id = ?`, movieID).Scan(&count); err != nil {
		t.Fatalf("count movie_files: %v", err)
	}
	if count != 1 {
		t.Fatalf("want exactly 1 movie_files row, got %d", count)
	}
}

// TestScanMovie_NoOpsWhenAlreadyFiled proves ScanMovie never attaches a
// second file row over a movie that already has one - re-running the
// same "Refresh" click twice must not create a duplicate.
func TestScanMovie_NoOpsWhenAlreadyFiled(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	movieID := seedMovie(t, db)

	if _, err := store.InsertMovieFile(ctx, db, movieID, "Inception (2010).mkv", 100); err != nil {
		t.Fatalf("insert movie file: %v", err)
	}

	moviePath, err := store.ResolveMoviePath(ctx, db, movieID)
	if err != nil {
		t.Fatalf("resolve movie path: %v", err)
	}
	// The recorded file has to actually exist, or the scan now (correctly)
	// treats it as deleted-by-hand and prunes the row.
	writeDownloadFile(t, moviePath, "Inception (2010).mkv", "the already-filed movie")
	writeDownloadFile(t, moviePath, "Inception.2010.1080p.WEB.mkv", "a different file")

	svc := &ImportService{DB: db}
	imported, err := svc.ScanMovie(ctx, movieID)
	if err != nil {
		t.Fatalf("scan movie: %v", err)
	}
	if imported {
		t.Fatalf("want imported=false - movie already has a file")
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM movie_files WHERE movie_id = ?`, movieID).Scan(&count); err != nil {
		t.Fatalf("count movie_files: %v", err)
	}
	if count != 1 {
		t.Fatalf("want still exactly 1 movie_files row (no duplicate), got %d", count)
	}
}

// TestScanSeries_ImportsSingleSeriesOnDemand is ScanSeries's counterpart
// to TestScanMovie_ImportsSingleMovieOnDemand - the series detail page's
// "Refresh" button.
func TestScanSeries_ImportsSingleSeriesOnDemand(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seriesID := seedSeries(t, db)

	seriesPath, err := store.ResolveSeriesPath(ctx, db, seriesID)
	if err != nil {
		t.Fatalf("resolve series path: %v", err)
	}
	writeDownloadFile(t, seriesPath, "Season 01/Show.S01E01.mkv", "s1e1 bytes")

	svc := &ImportService{DB: db}
	imported, err := svc.ScanSeries(ctx, seriesID)
	if err != nil {
		t.Fatalf("scan series: %v", err)
	}
	if imported != 1 {
		t.Fatalf("want 1 imported episode, got %d", imported)
	}

	episodeID, found, err := store.FindEpisode(ctx, db, seriesID, 1, 1)
	if err != nil || !found {
		t.Fatalf("find S01E01: found=%v err=%v", found, err)
	}
	var hasFile bool
	if err := db.QueryRowContext(ctx, `SELECT episode_file_id IS NOT NULL FROM episodes WHERE id = ?`, episodeID).Scan(&hasFile); err != nil {
		t.Fatalf("query episode: %v", err)
	}
	if !hasFile {
		t.Fatalf("want S01E01 to have a file attached")
	}
}

func TestScanSeriesLibrary_SkipsAlreadyFiledEpisodesInAPartialSeries(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seriesID := seedSeries(t, db)

	// Pre-file S01E01 directly (simulating an earlier grab/scan), then
	// place a DIFFERENT file for it on disk too - scanning must leave it
	// alone since it already has a file.
	filedEpisodeID, found, err := store.FindEpisode(ctx, db, seriesID, 1, 1)
	if err != nil || !found {
		t.Fatalf("find S01E01: found=%v err=%v", found, err)
	}
	if _, err := store.AttachEpisodeFile(ctx, db, filedEpisodeID, "Season 01/Show - S01E01 - Ep.mkv", 999); err != nil {
		t.Fatalf("attach S01E01 file: %v", err)
	}

	seriesPath, err := store.ResolveSeriesPath(ctx, db, seriesID)
	if err != nil {
		t.Fatalf("resolve series path: %v", err)
	}
	// S01E01's attached file has to actually exist, or the scan now
	// (correctly) prunes it as deleted-by-hand and re-imports it.
	writeDownloadFile(t, seriesPath, "Season 01/Show - S01E01 - Ep.mkv", "the already-filed episode")
	writeDownloadFile(t, seriesPath, "Season 01/Show.S01E01.mkv", "a NEW file that must be ignored")
	writeDownloadFile(t, seriesPath, "Season 01/Show.S01E02.mkv", "s1e2 bytes")

	svc := &ImportService{DB: db}
	imported, err := svc.ScanSeriesLibrary(ctx)
	if err != nil {
		t.Fatalf("scan series library: %v", err)
	}
	if imported != 1 {
		t.Fatalf("want 1 newly-imported episode (S01E02 only), got %d", imported)
	}

	// S01E01's original attached file record must be untouched.
	var relPath string
	if err := db.QueryRowContext(ctx, `
		SELECT ef.relative_path FROM episodes e JOIN episode_files ef ON ef.id = e.episode_file_id WHERE e.id = ?
	`, filedEpisodeID).Scan(&relPath); err != nil {
		t.Fatalf("query S01E01 file record: %v", err)
	}
	if relPath != "Season 01/Show - S01E01 - Ep.mkv" {
		t.Fatalf("want S01E01's original file record untouched, got %q", relPath)
	}

	// S01E02 should now have a file.
	ep2ID, found, err := store.FindEpisode(ctx, db, seriesID, 1, 2)
	if err != nil || !found {
		t.Fatalf("find S01E02: found=%v err=%v", found, err)
	}
	_, found, err = store.FindEpisodeMissingFile(ctx, db, seriesID, 1, 2)
	if err != nil {
		t.Fatalf("find episode missing file: %v", err)
	}
	if found {
		t.Fatalf("want S01E02 (id %d) to now have a file", ep2ID)
	}
}

func TestScanMusicLibrary_SkipsAlbumWithAnyExistingFile(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	albumID := seedAlbumWithRelease(t, db, 3)

	_, tracks, err := store.FindImportRelease(ctx, db, albumID)
	if err != nil {
		t.Fatalf("find import release: %v", err)
	}
	if _, err := store.AttachTrackFile(ctx, db, tracks[0].ID, "01 - Track 1.flac", 100); err != nil {
		t.Fatalf("attach track file: %v", err)
	}

	albumPath, err := store.ResolveAlbumPath(ctx, db, albumID)
	if err != nil {
		t.Fatalf("resolve album path: %v", err)
	}
	// The attached track's file has to actually exist, or the scan now
	// (correctly) prunes it as deleted-by-hand and treats the album as
	// fully unfiled.
	writeDownloadFile(t, albumPath, "01 - Track 1.flac", "a")
	writeDownloadFile(t, albumPath, "02 - Track 2.flac", "b")
	writeDownloadFile(t, albumPath, "03 - Track 3.flac", "c")

	svc := &ImportService{DB: db}
	imported, err := svc.ScanMusicLibrary(ctx)
	if err != nil {
		t.Fatalf("scan music library: %v", err)
	}
	if imported != 0 {
		t.Fatalf("want 0 imported for a partially-filed album (all-or-nothing scope), got %d", imported)
	}
}

func TestScanMusicLibrary_ImportsFullyUnfiledAlbum(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	albumID := seedAlbumWithRelease(t, db, 3)

	albumPath, err := store.ResolveAlbumPath(ctx, db, albumID)
	if err != nil {
		t.Fatalf("resolve album path: %v", err)
	}
	writeDownloadFile(t, albumPath, "01 - Track 1.flac", "a")
	writeDownloadFile(t, albumPath, "02 - Track 2.flac", "b")
	writeDownloadFile(t, albumPath, "03 - Track 3.flac", "c")

	svc := &ImportService{DB: db}
	imported, err := svc.ScanMusicLibrary(ctx)
	if err != nil {
		t.Fatalf("scan music library: %v", err)
	}
	if imported != 3 {
		t.Fatalf("want 3 imported, got %d", imported)
	}
}

// TestScanSeries_PrunesEpisodeFileDeletedByHand covers the "I deleted the
// file myself, tell UMMarr" case: Refresh must drop the stale row rather
// than keep reporting the episode as Downloaded forever.
func TestScanSeries_PrunesEpisodeFileDeletedByHand(t *testing.T) {
	db := openTestDB(t)
	svc := &ImportService{DB: db}
	seriesID := seedSeries(t, db)

	seriesPath, err := store.ResolveSeriesPath(context.Background(), db, seriesID)
	if err != nil {
		t.Fatalf("resolve series path: %v", err)
	}
	writeDownloadFile(t, seriesPath, "Season 01/Show.S01E01.mkv", "episode bytes")
	if _, err := svc.ScanSeries(context.Background(), seriesID); err != nil {
		t.Fatalf("initial scan: %v", err)
	}
	refs, err := store.ListEpisodeFilesForSeries(context.Background(), db, seriesID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("want 1 episode file after the first scan, got %d (err=%v)", len(refs), err)
	}

	// The scan renames in place, so this is the only copy in the folder.
	if err := os.Remove(filepath.Join(seriesPath, refs[0].RelativePath)); err != nil {
		t.Fatalf("delete the imported file by hand: %v", err)
	}
	if _, err := svc.ScanSeries(context.Background(), seriesID); err != nil {
		t.Fatalf("rescan: %v", err)
	}

	refs, err = store.ListEpisodeFilesForSeries(context.Background(), db, seriesID)
	if err != nil {
		t.Fatalf("list episode files: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("want the stale episode_files row pruned, got %d", len(refs))
	}
	// The trigger from migration 00016 must also have cleared the forward
	// pointer - a dangling one renders as Downloaded with no size.
	var dangling int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM episodes e
		WHERE e.episode_file_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM episode_files f WHERE f.id = e.episode_file_id)
	`).Scan(&dangling); err != nil {
		t.Fatalf("count dangling pointers: %v", err)
	}
	if dangling != 0 {
		t.Fatalf("want episodes.episode_file_id cleared when its file row was deleted, got %d dangling", dangling)
	}
}

// TestScanMovie_PrunesMovieFileDeletedByHand is the movie equivalent -
// and also proves the rescan re-imports a replacement sitting in the
// folder, rather than stopping at "this movie already has a file".
func TestScanMovie_PrunesMovieFileDeletedByHand(t *testing.T) {
	db := openTestDB(t)
	svc := &ImportService{DB: db}
	movieID := seedMovie(t, db)

	detail, _, err := store.GetMovieDetail(context.Background(), db, movieID)
	if err != nil {
		t.Fatalf("get movie detail: %v", err)
	}
	moviePath := detail.Path.String
	writeDownloadFile(t, moviePath, "Inception.2010.1080p.mkv", "the original file")
	if _, err := svc.ScanMovie(context.Background(), movieID); err != nil {
		t.Fatalf("initial scan: %v", err)
	}
	refs, err := store.ListMovieFilesForMovie(context.Background(), db, movieID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("want 1 movie file after the first scan, got %d (err=%v)", len(refs), err)
	}

	// The scan renames in place, so this is the only copy in the folder.
	if err := os.Remove(filepath.Join(moviePath, refs[0].RelativePath)); err != nil {
		t.Fatalf("delete the imported file by hand: %v", err)
	}
	if _, err := svc.ScanMovie(context.Background(), movieID); err != nil {
		t.Fatalf("rescan with nothing on disk: %v", err)
	}
	refs, err = store.ListMovieFilesForMovie(context.Background(), db, movieID)
	if err != nil {
		t.Fatalf("list movie files: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("want the stale movie_files row pruned, got %d", len(refs))
	}

	writeDownloadFile(t, moviePath, "Inception.2010.2160p.mkv", "a replacement put there by hand")
	if _, err := svc.ScanMovie(context.Background(), movieID); err != nil {
		t.Fatalf("rescan with a replacement: %v", err)
	}
	refs, err = store.ListMovieFilesForMovie(context.Background(), db, movieID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("want the replacement imported, got %d rows (err=%v)", len(refs), err)
	}
}

// TestPruneMissingFiles_KeepsRowWhenFolderIsUnreadable guards the
// dangerous direction: an unreadable folder is not evidence the files are
// gone, and must never cost real library state.
func TestPruneMissingFiles_KeepsRowWhenFolderIsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root - permission bits do not deny access")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatalf("mkdir locked: %v", err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	deleted := 0
	removed := pruneMissingFiles(context.Background(), dir,
		[]store.FileRef{{ID: 1, RelativePath: "locked/movie.mkv"}},
		func(context.Context, int64) error { deleted++; return nil })

	if len(removed) != 0 || deleted != 0 {
		t.Fatalf("want an unreadable path left alone, got removed=%d deleted=%d", len(removed), deleted)
	}
}

// TestScanMovie_RenamesInPlaceWithoutDuplicating covers the space bug: a
// scan of the library folder used to copy the file alongside itself under
// the template name, leaving two full copies of every scanned file.
func TestScanMovie_RenamesInPlaceWithoutDuplicating(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	svc := &ImportService{DB: db}
	movieID := seedMovie(t, db)

	moviePath, err := store.ResolveMoviePath(ctx, db, movieID)
	if err != nil {
		t.Fatalf("resolve movie path: %v", err)
	}
	writeDownloadFile(t, moviePath, "Inception.2010.1080p.BluRay.mkv", "the one and only copy")

	if _, err := svc.ScanMovie(ctx, movieID); err != nil {
		t.Fatalf("scan movie: %v", err)
	}

	entries, err := os.ReadDir(moviePath)
	if err != nil {
		t.Fatalf("read movie folder: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 {
		t.Fatalf("want the scanned file renamed in place (1 file), got %d: %v", len(names), names)
	}
	if names[0] == "Inception.2010.1080p.BluRay.mkv" {
		t.Fatalf("want the file renamed to the naming template, still got the original name")
	}

	refs, err := store.ListMovieFilesForMovie(ctx, db, movieID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("want 1 movie_files row, got %d (err=%v)", len(refs), err)
	}
	if refs[0].RelativePath != names[0] {
		t.Fatalf("recorded %q but the file on disk is %q", refs[0].RelativePath, names[0])
	}
}

// TestImport_Movie_LeavesDownloadInPlace is the other half: a grab import
// must still COPY, never move, or the torrent stops seeding.
func TestImport_Movie_LeavesDownloadInPlace(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	svc := &ImportService{DB: db}
	movieID := seedMovie(t, db)

	downloadDir := t.TempDir()
	writeDownloadFile(t, downloadDir, "Inception.2010.1080p.mkv", "seeding bytes")

	grab := store.Grab{MovieID: sql.NullInt64{Int64: movieID, Valid: true}}
	files, err := importer.ScanDirectory(downloadDir)
	if err != nil {
		t.Fatalf("scan download dir: %v", err)
	}
	if status, msg := svc.Import(ctx, grab, files, downloadDir); status != "imported" {
		t.Fatalf("want imported, got %s: %s", status, msg)
	}

	if _, err := os.Stat(filepath.Join(downloadDir, "Inception.2010.1080p.mkv")); err != nil {
		t.Fatalf("want the download left in place for seeding: %v", err)
	}
}

// TestScanMusicLibrary_PrunesTrackFileDeletedByHand is the music
// equivalent of the movie/series prune tests. It exists because music
// pruning silently did nothing for a while: the track file lookup failed
// on a bad join and the scan swallowed the error.
func TestScanMusicLibrary_PrunesTrackFileDeletedByHand(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	albumID := seedAlbumWithRelease(t, db, 2)

	_, tracks, err := store.FindImportRelease(ctx, db, albumID)
	if err != nil {
		t.Fatalf("find import release: %v", err)
	}
	// Recorded as filed, but the file is not on disk - deleted by hand.
	if _, err := store.AttachTrackFile(ctx, db, tracks[0].ID, "01 - Track 1.flac", 100); err != nil {
		t.Fatalf("attach track file: %v", err)
	}

	svc := &ImportService{DB: db}
	if _, err := svc.ScanMusicLibrary(ctx); err != nil {
		t.Fatalf("scan music library: %v", err)
	}

	refs, err := store.ListTrackFilesForAlbum(ctx, db, albumID)
	if err != nil {
		t.Fatalf("list track files: %v", err)
	}
	if len(refs) != 0 {
		t.Fatalf("want the stale track_files row pruned, got %d", len(refs))
	}
	var dangling int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM tracks t
		WHERE t.track_file_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM track_files f WHERE f.id = t.track_file_id)
	`).Scan(&dangling); err != nil {
		t.Fatalf("count dangling pointers: %v", err)
	}
	if dangling != 0 {
		t.Fatalf("want tracks.track_file_id cleared with its row, got %d dangling", dangling)
	}
}

func savedFolder(t *testing.T, db *sql.DB, table string, id int64) string {
	t.Helper()
	var p sql.NullString
	if err := db.QueryRow(`SELECT path FROM `+table+` WHERE id = ?`, id).Scan(&p); err != nil || !p.Valid || p.String == "" {
		t.Fatalf("want %s %d to have a saved folder, got %q (err %v)", table, id, p.String, err)
	}
	return p.String
}

// TestImport_Series_UsesSavedFolderAfterTemplateChange is the regression test
// for changing the series folder template on a show already in the library:
// the download used to go to a new folder named by the new template, and the
// next Refresh - which looks in the saved folder - dropped its entry.
func TestImport_Series_UsesSavedFolderAfterTemplateChange(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	svc := &ImportService{DB: db}
	seriesID := seedSeries(t, db)
	saved := savedFolder(t, db, "series", seriesID)

	if err := store.UpdateSeriesNamingConfig(ctx, db, "Renamed {Series Title}", "Season {season}", "{Series Title} - S{season:00}E{episode:00}"); err != nil {
		t.Fatalf("change series naming: %v", err)
	}
	downloadDir := t.TempDir()
	writeDownloadFile(t, downloadDir, "Show.S01E01.mkv", "s1e1")
	if status, msg := svc.Import(ctx, store.Grab{SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}},
		[]importer.File{{Path: "Show.S01E01.mkv", Size: 4}}, downloadDir); status != "imported" {
		t.Fatalf("want imported, got %s: %s", status, msg)
	}

	refs, err := store.ListEpisodeFilesForSeries(ctx, db, seriesID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("want 1 episode file, got %d (err %v)", len(refs), err)
	}
	if _, err := os.Stat(filepath.Join(saved, refs[0].RelativePath)); err != nil {
		t.Fatalf("want the episode inside the series' saved folder %s (recorded as %q): %v", saved, refs[0].RelativePath, err)
	}
	if _, err := svc.ScanSeries(ctx, seriesID); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refs, _ := store.ListEpisodeFilesForSeries(ctx, db, seriesID); len(refs) != 1 {
		t.Fatalf("want Refresh to keep the imported episode, got %d entries", len(refs))
	}
}

// TestImport_Movie_UsesSavedFolderAfterTemplateChange is the movie equivalent.
func TestImport_Movie_UsesSavedFolderAfterTemplateChange(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	svc := &ImportService{DB: db}
	movieID := seedMovie(t, db)
	saved := savedFolder(t, db, "movies", movieID)

	if err := store.UpdateMovieNamingConfig(ctx, db, "Renamed {Movie Title}", "{Movie Title} ({Release Year})"); err != nil {
		t.Fatalf("change movie naming: %v", err)
	}
	downloadDir := t.TempDir()
	writeDownloadFile(t, downloadDir, "Inception.2010.1080p.mkv", "movie bytes")
	if status, msg := svc.Import(ctx, store.Grab{MovieID: sql.NullInt64{Int64: movieID, Valid: true}},
		[]importer.File{{Path: "Inception.2010.1080p.mkv", Size: 11}}, downloadDir); status != "imported" {
		t.Fatalf("want imported, got %s: %s", status, msg)
	}

	refs, err := store.ListMovieFilesForMovie(ctx, db, movieID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("want 1 movie file, got %d (err %v)", len(refs), err)
	}
	if _, err := os.Stat(filepath.Join(saved, refs[0].RelativePath)); err != nil {
		t.Fatalf("want the movie inside its saved folder %s: %v", saved, err)
	}
	if _, err := svc.ScanMovie(ctx, movieID); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refs, _ := store.ListMovieFilesForMovie(ctx, db, movieID); len(refs) != 1 {
		t.Fatalf("want Refresh to keep the imported movie, got %d entries", len(refs))
	}
}

// TestImport_Album_UsesSavedFolderAfterTemplateChange covers whole-album grabs.
func TestImport_Album_UsesSavedFolderAfterTemplateChange(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	svc := &ImportService{DB: db}
	albumID := seedAlbumWithRelease(t, db, 2)
	saved := savedFolder(t, db, "albums", albumID)

	if err := store.UpdateMusicNamingConfig(ctx, db, "{Artist Name}", "Renamed {Album Title}", "Various Artists/{Series Name}", "{track:00} - {Track Title}"); err != nil {
		t.Fatalf("change music naming: %v", err)
	}
	downloadDir := t.TempDir()
	writeDownloadFile(t, downloadDir, "01.flac", "a")
	writeDownloadFile(t, downloadDir, "02.flac", "b")
	if status, msg := svc.Import(ctx, store.Grab{AlbumID: sql.NullInt64{Int64: albumID, Valid: true}},
		[]importer.File{{Path: "01.flac", Size: 1}, {Path: "02.flac", Size: 1}}, downloadDir); status != "imported" {
		t.Fatalf("want imported, got %s: %s", status, msg)
	}

	refs, err := store.ListTrackFilesForAlbum(ctx, db, albumID)
	if err != nil || len(refs) != 2 {
		t.Fatalf("want 2 track files, got %d (err %v)", len(refs), err)
	}
	for _, ref := range refs {
		if _, err := os.Stat(filepath.Join(saved, ref.RelativePath)); err != nil {
			t.Fatalf("want %q inside the album's saved folder %s: %v", ref.RelativePath, saved, err)
		}
	}
	if _, err := svc.ScanMusicLibrary(ctx); err != nil {
		t.Fatalf("library scan: %v", err)
	}
	if refs, _ := store.ListTrackFilesForAlbum(ctx, db, albumID); len(refs) != 2 {
		t.Fatalf("want the scan to keep both tracks, got %d entries", len(refs))
	}
}

// TestSetAlbumPath_NewAlbumGoesUnderArtistsSavedFolder - an album added after
// the artist template changed must sit beside the artist's other albums, not
// in a second folder for the same artist.
func TestSetAlbumPath_NewAlbumGoesUnderArtistsSavedFolder(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedAlbumWithRelease(t, db, 1)
	var artistID, artistMetadataID int64
	if err := db.QueryRow(`SELECT id, artist_metadata_id FROM artists LIMIT 1`).Scan(&artistID, &artistMetadataID); err != nil {
		t.Fatalf("find artist: %v", err)
	}
	artistFolder := savedFolder(t, db, "artists", artistID)

	if err := store.UpdateMusicNamingConfig(ctx, db, "Renamed {Artist Name}", "{Album Title}", "Various Artists/{Series Name}", "{Track Title}"); err != nil {
		t.Fatalf("change music naming: %v", err)
	}
	albumID, _, err := store.UpsertAlbum(ctx, db, artistMetadataID, metadata.AlbumMetadata{
		Title:       metadata.Field[string]{Value: "Discovery", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "discovery-rg-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert second album: %v", err)
	}
	if err := store.SetAlbumPath(ctx, db, albumID); err != nil {
		t.Fatalf("set album path: %v", err)
	}
	if got := savedFolder(t, db, "albums", albumID); !strings.HasPrefix(got, artistFolder+"/") {
		t.Fatalf("want the new album inside the artist's saved folder %s, got %s", artistFolder, got)
	}
}

// TestMovieFolderPath_FallsBackToTemplateAndSavesIt - an item with no saved
// folder gets one from the template, and keeps it.
func TestMovieFolderPath_FallsBackToTemplateAndSavesIt(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	movieID := seedMovie(t, db)
	if _, err := db.Exec(`UPDATE movies SET path = NULL WHERE id = ?`, movieID); err != nil {
		t.Fatalf("clear saved path: %v", err)
	}
	want, err := store.ResolveMoviePath(ctx, db, movieID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got, err := store.MovieFolderPath(ctx, db, movieID)
	if err != nil || got != want {
		t.Fatalf("want fallback %q, got %q (err %v)", want, got, err)
	}
	if saved := savedFolder(t, db, "movies", movieID); saved != want {
		t.Fatalf("want the fallback saved, got %q", saved)
	}
}

// TestScanSeries_BackfillsQualityFromTheGrab: a file renamed to the naming
// template says nothing about its quality, but the grab that fetched it
// still has the release name. Both a fresh scan and a Refresh of an
// already-tracked file use it.
func TestScanSeries_BackfillsQualityFromTheGrab(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seriesID := seedSeries(t, db)
	var seriesPath string
	if err := db.QueryRow(`SELECT path FROM series WHERE id = ?`, seriesID).Scan(&seriesPath); err != nil {
		t.Fatalf("series path: %v", err)
	}
	if _, err := store.InsertGrab(ctx, db, store.Grab{
		SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}, SeasonNumber: sql.NullInt64{Int64: 1, Valid: true},
		EpisodeNumber: sql.NullInt64{Int64: 1, Valid: true}, ReleaseTitle: "Breaking.Bad.S01E01.1080p.HEVC.x265-MeGusta",
		Indexer: "x", Protocol: "torrent", DownloadClient: "deluge", Status: "imported",
	}); err != nil {
		t.Fatalf("insert grab: %v", err)
	}
	// Fresh scan of an already-renamed file.
	writeDownloadFile(t, seriesPath, "Season 01/Breaking Bad - S01E01 - Ep.mkv", "s1e1")
	svc := &ImportService{DB: db}
	if n, err := svc.ScanSeries(ctx, seriesID); err != nil || n != 1 {
		t.Fatalf("scan: %d %v", n, err)
	}
	quality := func(episode int) releaseparse.FileQuality {
		var raw string
		if err := db.QueryRow(`SELECT ef.quality FROM episode_files ef JOIN episodes e ON e.episode_file_id = ef.id WHERE e.series_id = ? AND e.season_number = 1 AND e.episode_number = ?`, seriesID, episode).Scan(&raw); err != nil {
			t.Fatalf("quality for E%02d: %v", episode, err)
		}
		var q releaseparse.FileQuality
		json.Unmarshal([]byte(raw), &q)
		return q
	}
	if q := quality(1); q.Key() != "HDTV-1080p" || q.ReleaseGroup != "MeGusta" {
		t.Fatalf("want E01's quality from the grab's release name, got %+v", q)
	}

	// An already-tracked file with nothing recorded, and a season-pack grab.
	writeDownloadFile(t, seriesPath, "Season 01/Breaking Bad - S01E02 - Ep.mkv", "s1e2")
	var e2 int64
	if err := db.QueryRow(`SELECT id FROM episodes WHERE series_id = ? AND season_number = 1 AND episode_number = 2`, seriesID).Scan(&e2); err != nil {
		t.Fatalf("find E02: %v", err)
	}
	if _, err := store.AttachEpisodeFile(ctx, db, e2, "Season 01/Breaking Bad - S01E02 - Ep.mkv", 4); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if _, err := store.InsertGrab(ctx, db, store.Grab{
		SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}, SeasonNumber: sql.NullInt64{Int64: 1, Valid: true},
		ReleaseTitle: "Breaking.Bad.S01.720p.BluRay.x264-GRP", Indexer: "x", Protocol: "torrent", DownloadClient: "deluge", Status: "imported",
	}); err != nil {
		t.Fatalf("insert pack grab: %v", err)
	}
	if _, err := svc.ScanSeries(ctx, seriesID); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if q := quality(2); q.Key() != "Bluray-720p" || q.ReleaseGroup != "GRP" {
		t.Fatalf("want E02 backfilled from the season pack, got %+v", q)
	}
	if q := quality(1); q.Key() != "HDTV-1080p" {
		t.Fatalf("want E01's recorded quality left alone, got %+v", q)
	}
}

// TestImport_Movie_UpgradeReplacesOldFile: importing a better release for a
// movie that already has a file replaces it - the old file goes to the
// recycle bin when one is set, and only the new file stays recorded.
func TestImport_Movie_UpgradeReplacesOldFile(t *testing.T) {
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	svc := &ImportService{DB: db}
	ctx := context.Background()
	grab := store.Grab{MovieID: sql.NullInt64{Int64: movieID, Valid: true}}

	first := t.TempDir()
	writeDownloadFile(t, first, "Inception 2010 720p HDTV x264-A.mkv", "old bytes")
	if status, msg := svc.Import(ctx, grab, []importer.File{{Path: "Inception 2010 720p HDTV x264-A.mkv", Size: 9}}, first); status != "imported" {
		t.Fatalf("first import: %s (%s)", status, msg)
	}
	folder, _ := store.MovieFolderPath(ctx, db, movieID)
	refs, _ := store.ListMovieFilesForMovie(ctx, db, movieID)
	if len(refs) != 1 {
		t.Fatalf("want one file after the first import, got %+v", refs)
	}
	oldPath := filepath.Join(folder, refs[0].RelativePath)

	recycle := filepath.Join(t.TempDir(), "recycle")
	if _, err := db.Exec(`UPDATE media_settings SET recycle_bin_path = ?`, recycle); err != nil {
		t.Fatal(err)
	}
	second := t.TempDir()
	writeDownloadFile(t, second, "Inception 2010 1080p BluRay x264-B.mkv", "new bytes")
	if status, msg := svc.Import(ctx, grab, []importer.File{{Path: "Inception 2010 1080p BluRay x264-B.mkv", Size: 9}}, second); status != "imported" {
		t.Fatalf("upgrade import: %s (%s)", status, msg)
	}
	refs, _ = store.ListMovieFilesForMovie(ctx, db, movieID)
	if len(refs) != 1 {
		t.Fatalf("want only the new file recorded, got %+v", refs)
	}
	newPath := filepath.Join(folder, refs[0].RelativePath)
	if data, err := os.ReadFile(newPath); err != nil || string(data) != "new bytes" {
		t.Fatalf("want the new file in the library, got %q (%v)", data, err)
	}
	if oldPath != newPath {
		if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
			t.Fatalf("want the old file gone from the library, still at %s", oldPath)
		}
	}
	entries, _ := os.ReadDir(recycle)
	if len(entries) != 1 || string(must(os.ReadFile(filepath.Join(recycle, entries[0].Name())))) != "old bytes" {
		t.Fatalf("want the old file in the recycle bin, got %v", entries)
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

// TestMultiEpisodeFile: a file holding two episodes attaches to both on a
// scan, is named for both (S07E23-E24, titles joined), and the rename
// preview shows it once - not once per episode with conflicting names.
func TestMultiEpisodeFile(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seriesID := seedSeries(t, db)
	if _, err := db.Exec(`UPDATE episodes SET title = CASE episode_number WHEN 1 THEN 'Hit (1)' WHEN 2 THEN 'Run (2)' ELSE title END WHERE series_id = ? AND season_number = 1`, seriesID); err != nil {
		t.Fatal(err)
	}
	seriesPath, err := store.SeriesFolderPath(ctx, db, seriesID)
	if err != nil {
		t.Fatal(err)
	}
	writeDownloadFile(t, seriesPath, "Season 1/Breaking Bad - S01E01-02 - Hit + Run - WEBRip-1080p - h265.mkv", "two episodes")

	svc := &ImportService{DB: db}
	detail, _, _ := store.GetSeriesDetail(ctx, db, seriesID)
	if n := svc.scanSeriesFolder(ctx, mustMediaSettings(t, db), detail.SeriesSummary, false); n != 2 {
		t.Fatalf("want both episodes attached from the one file, got %d", n)
	}
	refs, _ := store.ListEpisodeFileRefs(ctx, db, seriesID)
	if len(refs) != 2 || refs[0].RelativePath != refs[1].RelativePath {
		t.Fatalf("want two rows sharing one path, got %+v", refs)
	}
	_, items, err := svc.RenamePreview(ctx, seriesID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !strings.Contains(items[0].New, "S01E01-E02 - Hit (1) + Run (2)") || len(items[0].FileIDs) != 2 {
		t.Fatalf("want one preview row naming both episodes, got %+v", items)
	}
	if renamed, problems, err := svc.RenameFiles(ctx, seriesID, []int64{items[0].FileID}); err != nil || renamed != 1 || len(problems) != 0 {
		t.Fatalf("rename: %d %v %v", renamed, problems, err)
	}
	refs, _ = store.ListEpisodeFileRefs(ctx, db, seriesID)
	if refs[0].RelativePath != items[0].New || refs[1].RelativePath != items[0].New {
		t.Fatalf("want both rows recording the new path, got %+v", refs)
	}
	if _, err := os.Stat(filepath.Join(seriesPath, items[0].New)); err != nil {
		t.Fatalf("want the renamed file on disk: %v", err)
	}
}

func mustMediaSettings(t *testing.T, db *sql.DB) store.MediaSettings {
	t.Helper()
	ms, err := store.GetMediaSettings(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}
