package api_test

import (
	"database/sql"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// TestManualImport_Page: scan a folder, see the guess, import it.
func TestManualImport_Page(t *testing.T) {
	db := openTestDB(t)
	movieID := seedTestMovieWithRootFolder(t, db, t.TempDir())
	events := &sync.Events{DB: db}
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Import: &sync.ImportService{DB: db, Events: events}, Download: &sync.DownloadService{DB: db}, Events: events}))
	t.Cleanup(srv.Close)

	downloads := t.TempDir()
	file := filepath.Join(downloads, "Inception.2010.1080p.WEB-DL.x264", "Inception.2010.1080p.WEB-DL.x264.mkv")
	os.MkdirAll(filepath.Dir(file), 0o755)
	os.WriteFile(file, []byte("movie"), 0o644)

	_, body := get(t, srv, "/activity/manual-import")
	if !strings.Contains(body, `name="path"`) || !strings.Contains(body, `href="/activity/manual-import"`) {
		t.Fatalf("want the folder box and a Manual Import tab, got:\n%s", body)
	}
	_, body = get(t, srv, "/activity/manual-import?path="+url.QueryEscape(downloads))
	if !strings.Contains(body, "Inception.2010.1080p.WEB-DL.x264.mkv") || !strings.Contains(body, `value="Movie: Inception (2010) [m`+itoa(movieID)+`]"`) {
		t.Fatalf("want the file listed with Inception guessed, got:\n%s", body)
	}
	if !regexp.MustCompile(`<option value="WEBDL-1080p" selected>`).MatchString(body) {
		t.Errorf("want WEBDL-1080p preselected")
	}
	if _, body = get(t, srv, "/activity/manual-import?path="+url.QueryEscape(filepath.Join(downloads, "nope"))); !strings.Contains(body, "not a folder UMMarr can read") {
		t.Errorf("want a missing folder reported, got:\n%s", body)
	}

	form := url.Values{"path": {downloads}, "mode": {"copy"}, "selected": {"0"},
		"path_0": {"Inception.2010.1080p.WEB-DL.x264/Inception.2010.1080p.WEB-DL.x264.mkv"},
		"item_0": {"Movie: Inception (2010) [m" + itoa(movieID) + "]"}, "quality_0": {"Bluray-1080p"}}
	_, body = postForm(t, srv, "/activity/manual-import", form)
	if !strings.Contains(body, "Imported") {
		t.Fatalf("want the import reported, got:\n%s", body)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM movie_files WHERE movie_id = ?`, movieID).Scan(&n)
	if n != 1 {
		t.Errorf("want one movie file, got %d", n)
	}
}

// TestActivityQueue_ShowsDates: each queue item says when it was grabbed,
// and when its status changed once it has.
func TestActivityQueue_ShowsDates(t *testing.T) {
	db := openTestDB(t)
	movieID := seedTestMovie(t, db)
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Download: &sync.DownloadService{DB: db}}))
	t.Cleanup(srv.Close)
	id, _ := store.InsertGrab(t.Context(), db, store.Grab{MovieID: sql.NullInt64{Int64: movieID, Valid: true}, ReleaseTitle: "Inception.2010.720p", Indexer: "Tracker", Protocol: "torrent", DownloadClient: "deluge", Status: "grabbed"})
	db.Exec(`UPDATE grabs SET added = '2026-09-17 08:05:00', updated = '2026-09-17 09:30:00', status = 'imported' WHERE id = ?`, id)

	_, body := get(t, srv, "/activity/queue")
	if !strings.Contains(body, `title="When it was grabbed">17 Sep 2026`) || !strings.Contains(body, "Imported 17 Sep 2026") {
		t.Fatalf("want grabbed and imported times, got:\n%s", body)
	}
}
