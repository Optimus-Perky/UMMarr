package sync

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func setMediaSettings(t *testing.T, db *sql.DB, change func(*store.MediaSettings)) {
	t.Helper()
	ctx := context.Background()
	ms, err := store.GetMediaSettings(ctx, db)
	if err != nil {
		t.Fatalf("get media settings: %v", err)
	}
	change(&ms)
	if err := store.UpdateMediaSettings(ctx, db, ms); err != nil {
		t.Fatalf("update media settings: %v", err)
	}
}

func importMovieDownload(t *testing.T, svc *ImportService, movieID int64, dir string, files ...string) (string, string) {
	t.Helper()
	var list []importer.File
	for _, f := range files {
		info, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("stat %s: %v", f, err)
		}
		list = append(list, importer.File{Path: f, Size: info.Size()})
	}
	return svc.Import(context.Background(), store.Grab{MovieID: sql.NullInt64{Int64: movieID, Valid: true}}, list, dir)
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestImport_RenameOffKeepsTheDownloadsName(t *testing.T) {
	db := openTestDB(t)
	svc := &ImportService{DB: db}
	movieID := seedMovie(t, db)
	if _, err := db.Exec(`UPDATE naming_config SET rename_files = 0 WHERE media_type = 'movie'`); err != nil {
		t.Fatalf("turn renaming off: %v", err)
	}
	dir := t.TempDir()
	writeDownloadFile(t, dir, "Inception.2010.1080p.BluRay.x265-GRP.mkv", "movie bytes")

	if status, msg := importMovieDownload(t, svc, movieID, dir, "Inception.2010.1080p.BluRay.x265-GRP.mkv"); status != "imported" {
		t.Fatalf("want imported, got %s: %s", status, msg)
	}
	folder := savedFolder(t, db, "movies", movieID)
	if names := dirNames(t, folder); len(names) != 1 || names[0] != "Inception.2010.1080p.BluRay.x265-GRP.mkv" {
		t.Fatalf("want the download's own name kept, got %v", names)
	}
}

func TestImport_QualityAndCodecTokens(t *testing.T) {
	db := openTestDB(t)
	svc := &ImportService{DB: db}
	movieID := seedMovie(t, db)
	if err := store.UpdateMovieNamingConfig(context.Background(), db, "{Movie Title} ({Release Year})",
		"{Movie Title} ({Release Year}) {Quality Title} {Video Codec}-{Release Group}"); err != nil {
		t.Fatalf("set template: %v", err)
	}
	dir := t.TempDir()
	writeDownloadFile(t, dir, "Inception.2010.1080p.BluRay.x265-GRP.mkv", "movie bytes")

	if status, msg := importMovieDownload(t, svc, movieID, dir, "Inception.2010.1080p.BluRay.x265-GRP.mkv"); status != "imported" {
		t.Fatalf("want imported, got %s: %s", status, msg)
	}
	folder := savedFolder(t, db, "movies", movieID)
	if names := dirNames(t, folder); len(names) != 1 || names[0] != "Inception (2010) Bluray-1080p x265-GRP.mkv" {
		t.Fatalf("want quality, codec and group in the name, got %v", names)
	}
}

func TestImport_ExtraFilesComeAlong(t *testing.T) {
	db := openTestDB(t)
	svc := &ImportService{DB: db}
	movieID := seedMovie(t, db)
	setMediaSettings(t, db, func(ms *store.MediaSettings) {
		ms.ImportExtraFiles = true
		ms.ExtraFileExtensions = "srt, nfo"
	})
	dir := t.TempDir()
	files := []string{"Inception.2010.1080p.mkv", "Inception.2010.1080p.en.srt", "Inception.2010.1080p.nfo", "readme.txt"}
	for _, f := range files {
		writeDownloadFile(t, dir, f, f)
	}

	if status, msg := importMovieDownload(t, svc, movieID, dir, files...); status != "imported" {
		t.Fatalf("want imported, got %s: %s", status, msg)
	}
	got := strings.Join(dirNames(t, savedFolder(t, db, "movies", movieID)), " | ")
	want := "Inception (2010).en.srt | Inception (2010).mkv | Inception (2010).nfo-orig"
	if got != want {
		t.Fatalf("library folder:\n got %s\nwant %s", got, want)
	}
}

func TestImport_EpisodeExtrasOnlyGoBesideTheirOwnEpisode(t *testing.T) {
	db := openTestDB(t)
	svc := &ImportService{DB: db}
	seriesID := seedSeries(t, db)
	setMediaSettings(t, db, func(ms *store.MediaSettings) { ms.ImportExtraFiles = true })
	dir := t.TempDir()
	files := []string{"Show.S01E01.mkv", "Show.S01E01.en.srt", "Show.S01E02.mkv"}
	var list []importer.File
	for _, f := range files {
		writeDownloadFile(t, dir, f, f)
		list = append(list, importer.File{Path: f, Size: int64(len(f))})
	}

	if status, msg := svc.Import(context.Background(), store.Grab{SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}}, list, dir); status != "imported" {
		t.Fatalf("want imported, got %s: %s", status, msg)
	}
	season := filepath.Join(savedFolder(t, db, "series", seriesID), "Season 1")
	got := strings.Join(dirNames(t, season), " | ")
	want := "Breaking Bad - S01E01 - Ep.en.srt | Breaking Bad - S01E01 - Ep.mkv | Breaking Bad - S01E02 - Ep.mkv"
	if got != want {
		t.Fatalf("season folder:\n got %s\nwant %s", got, want)
	}
}

func TestImport_UsesHardlinksWhenTurnedOn(t *testing.T) {
	db := openTestDB(t)
	svc := &ImportService{DB: db}
	movieID := seedMovie(t, db)
	setMediaSettings(t, db, func(ms *store.MediaSettings) { ms.UseHardlinks = true })
	dir := t.TempDir()
	writeDownloadFile(t, dir, "Inception.2010.mkv", "seeding bytes")

	status, msg := importMovieDownload(t, svc, movieID, dir, "Inception.2010.mkv")
	if status != "imported" || !strings.HasSuffix(msg, "(hardlinked)") {
		t.Skipf("hardlink not possible between the test's temp dirs (status %s: %s)", status, msg)
	}
	src, _ := os.Stat(filepath.Join(dir, "Inception.2010.mkv"))
	dest, err := os.Stat(filepath.Join(savedFolder(t, db, "movies", movieID), "Inception (2010).mkv"))
	if err != nil || !os.SameFile(src, dest) {
		t.Fatalf("want the library file to be a hardlink of the download (err %v)", err)
	}
}

func TestImport_FreeSpaceMinimumRefusesTheImport(t *testing.T) {
	db := openTestDB(t)
	svc := &ImportService{DB: db}
	movieID := seedMovie(t, db)
	setMediaSettings(t, db, func(ms *store.MediaSettings) { ms.MinimumFreeSpaceMB = 1 << 40 })
	dir := t.TempDir()
	writeDownloadFile(t, dir, "Inception.2010.mkv", "movie bytes")

	status, msg := importMovieDownload(t, svc, movieID, dir, "Inception.2010.mkv")
	if status != "import_failed" || !strings.Contains(msg, "not enough free space") {
		t.Fatalf("want the import refused for free space, got %s: %s", status, msg)
	}
}

func TestRefresh_UnmonitorsAMovieWhoseFileWasDeleted(t *testing.T) {
	for _, tc := range []struct {
		name          string
		unmonitor     bool
		wantMonitored bool
	}{
		{"setting on", true, false},
		{"setting off", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			svc := &ImportService{DB: db}
			movieID := seedMovie(t, db)
			setMediaSettings(t, db, func(ms *store.MediaSettings) { ms.UnmonitorDeleted = tc.unmonitor })
			dir := t.TempDir()
			writeDownloadFile(t, dir, "Inception.2010.mkv", "movie bytes")
			if status, msg := importMovieDownload(t, svc, movieID, dir, "Inception.2010.mkv"); status != "imported" {
				t.Fatalf("import: %s: %s", status, msg)
			}
			if err := os.Remove(filepath.Join(savedFolder(t, db, "movies", movieID), "Inception (2010).mkv")); err != nil {
				t.Fatalf("delete by hand: %v", err)
			}
			if _, err := svc.ScanMovie(context.Background(), movieID); err != nil {
				t.Fatalf("refresh: %v", err)
			}
			var monitored bool
			if err := db.QueryRow(`SELECT monitored FROM movies WHERE id = ?`, movieID).Scan(&monitored); err != nil {
				t.Fatalf("read monitored: %v", err)
			}
			if monitored != tc.wantMonitored {
				t.Fatalf("want monitored=%v, got %v", tc.wantMonitored, monitored)
			}
		})
	}
}

func TestRefresh_UnmonitorsAnEpisodeWhoseFileWasDeleted(t *testing.T) {
	db := openTestDB(t)
	svc := &ImportService{DB: db}
	seriesID := seedSeries(t, db)
	setMediaSettings(t, db, func(ms *store.MediaSettings) { ms.UnmonitorDeleted = true })
	dir := t.TempDir()
	writeDownloadFile(t, dir, "Show.S01E01.mkv", "s1e1")
	if status, msg := svc.Import(context.Background(), store.Grab{SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}},
		[]importer.File{{Path: "Show.S01E01.mkv", Size: 4}}, dir); status != "imported" {
		t.Fatalf("import: %s: %s", status, msg)
	}
	refs, err := store.ListEpisodeFilesForSeries(context.Background(), db, seriesID)
	if err != nil || len(refs) != 1 {
		t.Fatalf("want 1 episode file, got %d (err %v)", len(refs), err)
	}
	if err := os.Remove(filepath.Join(savedFolder(t, db, "series", seriesID), refs[0].RelativePath)); err != nil {
		t.Fatalf("delete by hand: %v", err)
	}
	if _, err := svc.ScanSeries(context.Background(), seriesID); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	var monitored bool
	if err := db.QueryRow(`SELECT monitored FROM episodes WHERE id = ?`, refs[0].OwnerID).Scan(&monitored); err != nil {
		t.Fatalf("read monitored: %v", err)
	}
	if monitored {
		t.Fatalf("want the episode unmonitored")
	}
}

func TestImport_SetsTheFileDateToTheAirDate(t *testing.T) {
	db := openTestDB(t)
	svc := &ImportService{DB: db}
	seriesID := seedSeries(t, db)
	if _, err := db.Exec(`UPDATE episodes SET air_date = '2008-01-13' WHERE series_id = ? AND season_number = 1 AND episode_number = 1`, seriesID); err != nil {
		t.Fatalf("set air date: %v", err)
	}
	setMediaSettings(t, db, func(ms *store.MediaSettings) { ms.EpisodeFileDate = store.EpisodeFileDateAirDate })
	dir := t.TempDir()
	writeDownloadFile(t, dir, "Show.S01E01.mkv", "s1e1")
	if status, msg := svc.Import(context.Background(), store.Grab{SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}},
		[]importer.File{{Path: "Show.S01E01.mkv", Size: 4}}, dir); status != "imported" {
		t.Fatalf("import: %s: %s", status, msg)
	}
	refs, _ := store.ListEpisodeFilesForSeries(context.Background(), db, seriesID)
	if len(refs) != 1 {
		t.Fatalf("want 1 episode file, got %d", len(refs))
	}
	info, err := os.Stat(filepath.Join(savedFolder(t, db, "series", seriesID), refs[0].RelativePath))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.ModTime().UTC().Format("2006-01-02"); got != "2008-01-13" {
		t.Fatalf("want the file dated to the air date, got %s", got)
	}
}

func TestRefresh_DeleteEmptyFolders(t *testing.T) {
	t.Run("removes an empty season folder, keeps the one with a file", func(t *testing.T) {
		db := openTestDB(t)
		svc := &ImportService{DB: db}
		seriesID := seedSeries(t, db)
		setMediaSettings(t, db, func(ms *store.MediaSettings) { ms.DeleteEmptyFolders = true })
		folder := savedFolder(t, db, "series", seriesID)
		writeDownloadFile(t, folder, "Season 1/Breaking Bad - S01E01 - Ep.mkv", "s1e1")
		if err := os.MkdirAll(filepath.Join(folder, "Season 2"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if _, err := svc.ScanSeries(context.Background(), seriesID); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if _, err := os.Stat(filepath.Join(folder, "Season 2")); !os.IsNotExist(err) {
			t.Errorf("want the empty Season 2 folder removed")
		}
		if _, err := os.Stat(filepath.Join(folder, "Season 1")); err != nil {
			t.Errorf("want Season 1 kept: %v", err)
		}
	})

	t.Run("never removes a root folder", func(t *testing.T) {
		db := openTestDB(t)
		svc := &ImportService{DB: db}
		movieID := seedMovie(t, db)
		setMediaSettings(t, db, func(ms *store.MediaSettings) { ms.DeleteEmptyFolders = true })
		var root string
		if err := db.QueryRow(`SELECT rf.path FROM movies m JOIN root_folders rf ON rf.id = m.root_folder_id WHERE m.id = ?`, movieID).Scan(&root); err != nil {
			t.Fatalf("find root: %v", err)
		}
		// A movie whose folder template came out empty lives in the root itself.
		if _, err := db.Exec(`UPDATE movies SET path = ? WHERE id = ?`, root, movieID); err != nil {
			t.Fatalf("point movie at root: %v", err)
		}
		if _, err := svc.ScanMovie(context.Background(), movieID); err != nil {
			t.Fatalf("refresh: %v", err)
		}
		if _, err := os.Stat(root); err != nil {
			t.Fatalf("the empty root folder was removed: %v", err)
		}
	})
}

func TestRefresh_CreatesAMissingMovieFolder(t *testing.T) {
	db := openTestDB(t)
	svc := &ImportService{DB: db}
	movieID := seedMovie(t, db)
	setMediaSettings(t, db, func(ms *store.MediaSettings) { ms.CreateEmptyFolders = true })
	folder := savedFolder(t, db, "movies", movieID)
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("precondition: want no movie folder yet")
	}
	if _, err := svc.ScanMovie(context.Background(), movieID); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		t.Fatalf("want the movie folder created: %v", err)
	}
}

func TestImport_SetPermissionsAppliesTheChosenModeAndGroup(t *testing.T) {
	db := openTestDB(t)
	svc := &ImportService{DB: db}
	movieID := seedMovie(t, db)
	gid := os.Getgid()
	setMediaSettings(t, db, func(ms *store.MediaSettings) {
		ms.SetPermissions = true
		ms.ChmodFolder = "750"
		ms.ChownGroup = strconv.Itoa(gid)
	})
	dir := t.TempDir()
	writeDownloadFile(t, dir, "Inception.2010.mkv", "movie bytes")
	if status, msg := importMovieDownload(t, svc, movieID, dir, "Inception.2010.mkv"); status != "imported" {
		t.Fatalf("import: %s: %s", status, msg)
	}
	folder := savedFolder(t, db, "movies", movieID)
	folderInfo, err := os.Stat(folder)
	if err != nil {
		t.Fatalf("stat folder: %v", err)
	}
	if got := folderInfo.Mode().Perm(); got != 0o750 {
		t.Errorf("movie folder: want 0750, got %v", got)
	}
	fileInfo, err := os.Stat(filepath.Join(folder, "Inception (2010).mkv"))
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o640 {
		t.Errorf("movie file: want 0640, got %v", got)
	}
	if st, ok := fileInfo.Sys().(*syscall.Stat_t); !ok || int(st.Gid) != gid {
		t.Errorf("movie file: want group %d", gid)
	}
}
