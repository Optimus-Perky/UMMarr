package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// writeSizedFile makes a sparse file of size bytes, so the sample check sees
// a real-sized video without writing it.
func writeSizedFile(t *testing.T, dir, relPath string, size int64) {
	t.Helper()
	writeDownloadFile(t, dir, relPath, "")
	if err := os.Truncate(filepath.Join(dir, relPath), size); err != nil {
		t.Fatal(err)
	}
}

func findItem(t *testing.T, items []ManualImportItem, path string) ManualImportItem {
	t.Helper()
	for _, it := range items {
		if it.Path == path {
			return it
		}
	}
	t.Fatalf("no item for %s in %+v", path, items)
	return ManualImportItem{}
}

func TestManualImportScan_Guesses(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	renameProfiles(t, db)
	seriesID := seedSeries(t, db)
	renameProfiles(t, db)
	albumID := seedAlbumWithRelease(t, db, 3)
	svc := &ImportService{DB: db}

	dir := filepath.Join(t.TempDir(), "downloads")
	writeSizedFile(t, dir, "Inception.2010.1080p.BluRay.x264-GRP/Inception.2010.1080p.BluRay.x264-GRP.mkv", 700<<20)
	writeSizedFile(t, dir, "Inception.2010.1080p.BluRay.x264-GRP/sample.mkv", 20<<20)
	writeSizedFile(t, dir, "Breaking.Bad.S02E01.720p.HDTV.x264.mkv", 300<<20)
	writeSizedFile(t, dir, "Unknown.Thing.2019.mkv", 300<<20)
	writeDownloadFile(t, dir, "Daft Punk - Homework (1997)/02 - Around the World.flac", "b")
	writeDownloadFile(t, dir, "notes.txt", "ignored")

	items, err := svc.ManualImportScan(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("want the five media files and nothing else, got %d: %+v", len(items), items)
	}
	movie := findItem(t, items, "Inception.2010.1080p.BluRay.x264-GRP/Inception.2010.1080p.BluRay.x264-GRP.mkv")
	if movie.Kind != ManualMovie || movie.MovieID != movieID || movie.Quality != "Bluray-1080p" || len(movie.Rejections) != 0 {
		t.Errorf("want Inception guessed as Bluray-1080p with nothing wrong, got %+v", movie)
	}
	if sample := findItem(t, items, "Inception.2010.1080p.BluRay.x264-GRP/sample.mkv"); !strings.Contains(strings.Join(sample.Rejections, "|"), "Sample") {
		t.Errorf("want the sample flagged, got %+v", sample)
	}
	ep := findItem(t, items, "Breaking.Bad.S02E01.720p.HDTV.x264.mkv")
	if ep.Kind != ManualSeries || ep.SeriesID != seriesID || ep.Season != 2 || len(ep.Episodes) != 1 || ep.Episodes[0] != 1 || ep.Quality != "HDTV-720p" {
		t.Errorf("want Breaking Bad S02E01 guessed, got %+v", ep)
	}
	if unknown := findItem(t, items, "Unknown.Thing.2019.mkv"); unknown.Kind != "" || len(unknown.Rejections) == 0 {
		t.Errorf("want an unknown file left for the user to choose, got %+v", unknown)
	}
	track := findItem(t, items, "Daft Punk - Homework (1997)/02 - Around the World.flac")
	_, tracks, _ := store.FindImportRelease(ctx, db, albumID)
	if track.Kind != ManualTrack || track.AlbumID != albumID || track.TrackID != tracks[1].ID {
		t.Errorf("want track 2 of Homework guessed from the leading number, got %+v", track)
	}

	if _, err := svc.ManualImportScan(ctx, filepath.Join(dir, "missing"), nil); err == nil {
		t.Errorf("want an error for a folder that isn't there")
	}
}

func fileQualityKey(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var raw string
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&raw); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	var q releaseparse.FileQuality
	_ = json.Unmarshal([]byte(raw), &q)
	return q.Key()
}

func TestManualImport_ImportsWhatWasChosen(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	renameProfiles(t, db)
	seriesID := seedSeries(t, db)
	renameProfiles(t, db)
	albumID := seedAlbumWithRelease(t, db, 3)
	_, tracks, _ := store.FindImportRelease(ctx, db, albumID)
	svc := &ImportService{DB: db, Events: &Events{DB: db}}

	dir := t.TempDir()
	writeDownloadFile(t, dir, "weird-name.mkv", "movie")            // named nothing like the movie
	writeDownloadFile(t, dir, "Season Two/part one.mkv", "episode") // no S/E in the name
	writeDownloadFile(t, dir, "tune.flac", "track")

	results := svc.ManualImport(ctx, dir, ImportCopy, []ManualImportItem{
		{Path: "weird-name.mkv", Kind: ManualMovie, MovieID: movieID, Quality: "WEBDL-1080p"},
		{Path: "Season Two/part one.mkv", Kind: ManualSeries, SeriesID: seriesID, Season: 2, Episodes: []int{1, 2}, Quality: "HDTV-720p"},
		{Path: "tune.flac", Kind: ManualTrack, AlbumID: albumID, TrackID: tracks[2].ID, Quality: "Unknown"},
		{Path: "../escape.mkv", Kind: ManualMovie, MovieID: movieID},
	}, nil)
	for i, want := range []bool{true, true, true, false} {
		if results[i].OK != want {
			t.Errorf("result %d (%s): want OK=%v, got %+v", i, results[i].Path, want, results[i])
		}
	}

	if got := fileQualityKey(t, db, `SELECT quality FROM movie_files WHERE movie_id = ?`, movieID); got != "WEBDL-1080p" {
		t.Errorf("want the chosen movie quality kept, got %q", got)
	}
	var episodeFiles int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM episodes e JOIN seasons s ON s.id = e.season_id WHERE s.series_id = ? AND s.season_number = 2 AND e.episode_file_id IS NOT NULL`, seriesID).Scan(&episodeFiles)
	if episodeFiles != 2 {
		t.Errorf("want the file attached to S02E01 and S02E02, got %d episodes with files", episodeFiles)
	}
	var trackHasFile bool
	db.QueryRowContext(ctx, `SELECT track_file_id IS NOT NULL FROM tracks WHERE id = ?`, tracks[2].ID).Scan(&trackHasFile)
	if !trackHasFile {
		t.Errorf("want track 3 imported")
	}
	if _, err := os.Stat(filepath.Join(dir, "weird-name.mkv")); err != nil {
		t.Errorf("want Copy to leave the original, got %v", err)
	}
	history, _, _ := store.ListHistory(ctx, db, store.HistoryFilter{Limit: 10})
	if len(history) != 3 || history[0].Source != "manual import" {
		t.Errorf("want three manual import history events, got %+v", history)
	}
}

func TestManualImport_MoveAndGrab(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	svc := &ImportService{DB: db}
	grabID := insertTestGrab(t, db, movieID, "import_failed", sql.NullTime{})
	grab, _, _ := store.GetGrab(ctx, db, grabID)

	dir := filepath.Join(t.TempDir(), "Inception.2010")
	writeDownloadFile(t, dir, "sub/Inception.mkv", "movie")
	results := svc.ManualImport(ctx, dir, ImportMove, []ManualImportItem{{Path: "sub/Inception.mkv", Kind: ManualMovie, MovieID: movieID, Quality: "Bluray-1080p"}}, &grab)
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("want imported, got %+v", results)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "Inception.mkv")); !os.IsNotExist(err) {
		t.Errorf("want Move to remove the original, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
		t.Errorf("want the emptied folder tidied, got %v", err)
	}
	if g, _, _ := store.GetGrab(ctx, db, grabID); g.Status != "imported" {
		t.Errorf("want the queue item marked imported, got %q", g.Status)
	}
}

// renameProfiles lets the next seed helper create its own "Any" profile.
func renameProfiles(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`UPDATE quality_profiles SET name = 'Any ' || id`); err != nil {
		t.Fatal(err)
	}
}
