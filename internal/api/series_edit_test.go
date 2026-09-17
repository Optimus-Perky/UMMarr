package api_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

func TestSeriesToolbar_DialogsLoad(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seriesID := seedTestSeries(t, db)
	id := itoa(seriesID)
	store.CreateQualityProfile(t.Context(), db, "HD-1080p")

	_, body := get(t, srv, "/tv/"+id+"/edit")
	for _, want := range []string{"Edit - Breaking Bad", `name="monitored" checked`, `name="monitor_new_items"`, `name="season_folder" checked`, "HD-1080p", `name="series_type"`, `name="path" value="/media/tv/Breaking Bad"`, `name="tags"`, "Not active yet"} {
		if !strings.Contains(body, want) {
			t.Errorf("edit dialog: want %q in:\n%s", want, body)
		}
	}
	_, body = get(t, srv, "/tv/"+id+"/monitor")
	for _, want := range []string{"Series Monitoring - Breaking Bad", `value="all"`, "Future Episodes", "Pilot Episode", "Unmonitor Specials", `value="none"`} {
		if !strings.Contains(body, want) {
			t.Errorf("monitor dialog: want %q", want)
		}
	}
	_, body = get(t, srv, "/tv/"+id+"/delete")
	for _, want := range []string{"Delete - Breaking Bad", `name="add_exclusion"`, `name="delete_files"`, `hx-delete="/tv/` + id + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("delete dialog: want %q", want)
		}
	}
	_, body = get(t, srv, "/tv/"+id+"/manage-episodes")
	if !strings.Contains(body, "no episode files") {
		t.Errorf("manage episodes: want the empty notice, got:\n%s", body)
	}
}

func TestSeriesEdit_SavesAndMovesFolder(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	root := t.TempDir()
	seriesID := seedTestSeriesWithRootFolder(t, db, root)
	id := itoa(seriesID)
	oldPath, _ := store.ResolveSeriesPath(t.Context(), db, seriesID)
	os.MkdirAll(filepath.Join(oldPath, "Season 01"), 0o755)
	os.WriteFile(filepath.Join(oldPath, "Season 01", "ep.mkv"), []byte("x"), 0o644)
	profile, _ := store.CreateQualityProfile(t.Context(), db, "HD-1080p")
	newPath := filepath.Join(root, "Breaking Bad (2008)")

	resp, body := postForm(t, srv, "/tv/"+id+"/edit", url.Values{"monitor_new_items": {"none"}, "quality_profile_id": {itoa(profile)}, "series_type": {"daily"}, "path": {newPath}, "move_files": {"on"}, "tags": {"kids, 4K"}})
	if resp.Header.Get("HX-Redirect") != "/tv/breaking-bad" {
		t.Fatalf("want a redirect back to the series, got %d:\n%s", resp.StatusCode, body)
	}
	if _, err := os.Stat(filepath.Join(newPath, "Season 01", "ep.mkv")); err != nil {
		t.Fatalf("want the folder moved with its files: %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatal("want the old folder gone")
	}
	s, _ := store.GetSeriesSettings(t.Context(), db, seriesID)
	if s.Monitored || s.MonitorNewItems != "none" || s.SeasonFolder || s.QualityProfileID != profile || s.SeriesType != "daily" || s.Path != newPath || strings.Join(s.Tags, ",") != "4k,kids" {
		t.Fatalf("want the dialog saved, got %+v", s)
	}
	_, page := get(t, srv, "/tv/"+id)
	if !strings.Contains(page, "HD-1080p") || !strings.Contains(page, newPath) {
		t.Fatal("want the page to show the new profile and path")
	}

	// A relative path is refused and the dialog re-renders with the reason.
	resp, body = postForm(t, srv, "/tv/"+id+"/edit", url.Values{"quality_profile_id": {itoa(profile)}, "path": {"Breaking Bad"}})
	if resp.Header.Get("HX-Redirect") != "" || !strings.Contains(body, "absolute folder") {
		t.Fatalf("want the relative path refused, got:\n%s", body)
	}
}

func TestSeriesMonitor_AppliesOption(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seriesID := seedTestSeries(t, db)
	resp, _ := postForm(t, srv, "/tv/"+itoa(seriesID)+"/monitor", url.Values{"monitor": {"none"}})
	if resp.Header.Get("HX-Redirect") != "/tv/breaking-bad" {
		t.Fatalf("want a redirect, got %d", resp.StatusCode)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM episodes WHERE series_id = ? AND monitored`, seriesID).Scan(&n)
	if n != 0 {
		t.Fatalf("want every episode unmonitored, %d still are", n)
	}
	if resp, body := postForm(t, srv, "/tv/"+itoa(seriesID)+"/monitor", url.Values{"monitor": {"bogus"}}); resp.Header.Get("HX-Redirect") != "" || !strings.Contains(body, "unknown monitoring option") {
		t.Fatalf("want a bad option refused, got:\n%s", body)
	}
}

func TestSeriesManageEpisodes_DeletesFiles(t *testing.T) {
	db := openTestDB(t)
	events := &sync.Events{DB: db}
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Import: &sync.ImportService{DB: db, Events: events}, Events: events}))
	defer srv.Close()
	root := t.TempDir()
	seriesID := seedTestSeriesWithRootFolder(t, db, root)
	seriesPath, _ := store.ResolveSeriesPath(t.Context(), db, seriesID)
	os.MkdirAll(filepath.Join(seriesPath, "Season 01"), 0o755)
	os.WriteFile(filepath.Join(seriesPath, "Season 01", "ep.mkv"), []byte("x"), 0o644)
	var e1 int64
	db.QueryRow(`SELECT id FROM episodes WHERE series_id = ?`, seriesID).Scan(&e1)
	fileID, _ := store.AttachEpisodeFile(t.Context(), db, e1, "Season 01/ep.mkv", 1)

	_, body := get(t, srv, "/tv/"+itoa(seriesID)+"/manage-episodes")
	if !strings.Contains(body, `name="file_id" value="`+itoa(fileID)+`"`) || !strings.Contains(body, "Season 01/ep.mkv") {
		t.Fatalf("want the file listed with a checkbox, got:\n%s", body)
	}
	resp, body := postForm(t, srv, "/tv/"+itoa(seriesID)+"/manage-episodes/delete", url.Values{})
	if resp.Header.Get("HX-Redirect") != "" || !strings.Contains(body, "Tick at least one") {
		t.Fatalf("want nothing deleted without a tick, got:\n%s", body)
	}
	resp, _ = postForm(t, srv, "/tv/"+itoa(seriesID)+"/manage-episodes/delete", url.Values{"file_id": {itoa(fileID)}})
	if resp.Header.Get("HX-Redirect") != "/tv/breaking-bad?deleted=1" {
		t.Fatalf("want a redirect reporting 1 deleted, got %q", resp.Header.Get("HX-Redirect"))
	}
	if _, err := os.Stat(filepath.Join(seriesPath, "Season 01", "ep.mkv")); !os.IsNotExist(err) {
		t.Fatal("want the file removed from disk")
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM episode_files`).Scan(&n)
	if n != 0 {
		t.Fatalf("want the file row gone, got %d", n)
	}
	db.QueryRow(`SELECT COUNT(*) FROM history WHERE series_id = ? AND event = 'deleted'`, seriesID).Scan(&n)
	if n != 1 {
		t.Fatalf("want a history event for the deletion, got %d", n)
	}
	_, page := get(t, srv, "/tv/"+itoa(seriesID)+"?deleted=1")
	if !strings.Contains(page, "1 file deleted.") {
		t.Fatal("want the page to report the deletion")
	}
}

func TestSeriesDelete_KeepsFilesAndAddsExclusion(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	root := t.TempDir()
	seriesID := seedTestSeriesWithRootFolder(t, db, root)
	seriesPath, _ := store.ResolveSeriesPath(t.Context(), db, seriesID)
	os.MkdirAll(seriesPath, 0o755)
	os.WriteFile(filepath.Join(seriesPath, "ep.mkv"), []byte("x"), 0o644)

	resp := deleteRequest(t, srv, "/tv/"+itoa(seriesID)+"?add_exclusion=on")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("HX-Redirect") != "/tv" {
		t.Fatalf("want a redirect to the TV page, got %d %q", resp.StatusCode, resp.Header.Get("HX-Redirect"))
	}
	if _, err := os.Stat(filepath.Join(seriesPath, "ep.mkv")); err != nil {
		t.Fatalf("want the files kept when Delete Files is off: %v", err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM series`).Scan(&n)
	if n != 0 {
		t.Fatal("want the series removed")
	}
	var title string
	if err := db.QueryRow(`SELECT title FROM import_list_exclusions WHERE media_type = 'series' AND tmdb_id = 1396`).Scan(&title); err != nil || title != "Breaking Bad" {
		t.Fatalf("want a list exclusion for the series, got %q (%v)", title, err)
	}
}

func TestHistory_FilteredBySeries(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seriesID := seedTestSeries(t, db)
	db.Exec(`INSERT INTO history (event, media_type, series_id, title, detail, source) VALUES ('grabbed', 'series', ?, 'Breaking Bad', 'S01E01', 'test'), ('grabbed', 'movie', NULL, 'Inception', 'x', 'test')`, seriesID)
	_, body := get(t, srv, "/history?series="+itoa(seriesID))
	if !strings.Contains(body, "History - Breaking Bad") || !strings.Contains(body, "S01E01") || strings.Contains(body, "Inception") || !strings.Contains(body, `href="/tv/`+itoa(seriesID)+`"`) {
		t.Fatalf("want only the series' events with a way back, got:\n%s", body)
	}
}

func TestSeriesManageEpisodes_EditorAndImport(t *testing.T) {
	db := openTestDB(t)
	events := &sync.Events{DB: db}
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Import: &sync.ImportService{DB: db, Events: events}, Events: events}))
	defer srv.Close()
	seriesID := seedTestSeries(t, db)
	var seasonID, e1 int64
	db.QueryRow(`SELECT id FROM episodes WHERE series_id = ?`, seriesID).Scan(&e1)
	db.QueryRow(`SELECT season_id FROM episodes WHERE id = ?`, e1).Scan(&seasonID)
	e2, _ := store.UpsertEpisode(t.Context(), db, seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: 2, Title: metadata.Field[string]{Value: "Cat's in the Bag", Provider: "tmdb"}})
	fileID, _ := store.AttachEpisodeFile(t.Context(), db, e1, "Season 01/Breaking Bad - S01E01 - Pilot - HDTV-1080p x265-MeGusta.mkv", 517<<20)
	db.Exec(`UPDATE episode_files SET quality = '{"source":"HDTV","resolution":"1080p","releaseGroup":"MeGusta"}' WHERE id = ?`, fileID)

	_, body := get(t, srv, "/tv/"+itoa(seriesID)+"/manage-episodes")
	for _, want := range []string{"Manage Episodes - Breaking Bad", ">Relative Path<", ">Season<", ">Episodes<", ">Release Group<", ">Quality<", ">Languages<", ">Size<", ">Release Type<",
		"1 - Pilot", "MeGusta", "HDTV-1080p", "English", "517.0 MiB", "Select Season", "Select Episode(s)", "Select Quality", "Select Release Group", "Select Language", "Select Release Type",
		`id="manage-import"`, `id="manage-delete"`, `data-episodes='{&#34;1&#34;:[{&#34;n&#34;:1,&#34;t&#34;:&#34;Pilot&#34;},{&#34;n&#34;:2,&#34;t&#34;:&#34;Cat&#39;s in the Bag&#34;}]}'`} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q in the editor, got:\n%s", want, body)
		}
	}

	p := "f" + itoa(fileID) + "_"
	resp, body := postForm(t, srv, "/tv/"+itoa(seriesID)+"/manage-episodes", url.Values{p + "quality": {"WEBDL-1080p"}, p + "group": {"NTb"}, p + "languages": {"English, German"}, p + "release_type": {"singleEpisode"}, p + "episodes": {"2"}})
	if resp.Header.Get("HX-Redirect") != "/tv/breaking-bad?edited=1" {
		t.Fatalf("want a redirect reporting 1 edited, got %q:\n%s", resp.Header.Get("HX-Redirect"), body)
	}
	files, _ := store.ListEpisodeFileDetails(t.Context(), db, seriesID)
	if len(files) != 1 || files[0].Quality != "WEBDL-1080p" || files[0].ReleaseGroup != "NTb" || files[0].Languages != "English, German" || files[0].ReleaseType != "singleEpisode" || files[0].EpisodeNumbers[0] != 2 {
		t.Fatalf("want the edits applied and the file on E02, got %+v", files)
	}
	var fileOf2 int64
	db.QueryRow(`SELECT episode_file_id FROM episodes WHERE id = ?`, e2).Scan(&fileOf2)
	if fileOf2 == 0 {
		t.Fatal("want episode 2 to point at the file")
	}
	_, page := get(t, srv, "/tv/"+itoa(seriesID)+"?edited=1")
	if !strings.Contains(page, "1 file edited.") {
		t.Fatal("want the page to report the edit")
	}
	// Moving it back onto E02 (where it already is) is fine; onto a missing episode is refused with the reason.
	resp, body = postForm(t, srv, "/tv/"+itoa(seriesID)+"/manage-episodes", url.Values{"f" + itoa(fileOf2) + "_season": {"3"}})
	if resp.Header.Get("HX-Redirect") != "" || !strings.Contains(body, "no episode S03E02") {
		t.Fatalf("want the bad season refused with the reason, got:\n%s", body)
	}
}
