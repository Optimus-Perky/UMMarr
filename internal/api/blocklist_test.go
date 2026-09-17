package api_test

import (
	"database/sql"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// TestBlocklist_Activity: the queue's Remove blocklists a release, it shows
// under Activity -> Blocklist, and can be removed from there again.
func TestBlocklist_Activity(t *testing.T) {
	db := openTestDB(t)
	movieID := seedTestMovie(t, db)
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Download: &sync.DownloadService{DB: db}}))
	t.Cleanup(srv.Close)

	grabID, err := store.InsertGrab(t.Context(), db, store.Grab{MovieID: sql.NullInt64{Int64: movieID, Valid: true},
		ReleaseTitle: "Inception.2010.1080p.BluRay.x264-GRP", Indexer: "Tracker", Protocol: "torrent", DownloadClient: "deluge", Status: "downloading"})
	if err != nil {
		t.Fatal(err)
	}

	_, body := get(t, srv, "/activity/queue")
	if !strings.Contains(body, `data-grab-id="`+itoa(grabID)+`"`) {
		t.Fatalf("want a Remove button on the queued grab, got:\n%s", body)
	}
	_, body = get(t, srv, "/activity")
	if !strings.Contains(body, `id="grab-remove-dialog"`) || !strings.Contains(body, "Blocklist and Search") || !strings.Contains(body, `href="/activity/blocklist"`) {
		t.Fatalf("want the Remove dialog and a Blocklist tab, got:\n%s", body)
	}

	resp, body := postForm(t, srv, "/activity/grabs/"+itoa(grabID)+"/remove", url.Values{"blocklist": {"only"}})
	if resp.Header.Get("HX-Trigger") != "grab-removed" {
		t.Fatalf("want the grab removed, got %d:\n%s", resp.StatusCode, body)
	}
	if g, _, _ := store.GetGrab(t.Context(), db, grabID); g.Status != "failed" {
		t.Errorf("want the grab marked failed, got %q", g.Status)
	}

	_, body = get(t, srv, "/activity/blocklist")
	if !strings.Contains(body, "Inception.2010.1080p.BluRay.x264-GRP") || !strings.Contains(body, "Manually marked as failed") || !strings.Contains(body, "Inception") {
		t.Fatalf("want the release on the blocklist page, got:\n%s", body)
	}
	entries, total, _ := store.ListBlocklist(t.Context(), db, 50, 0)
	if total != 1 {
		t.Fatalf("want one entry, got %d", total)
	}
	if resp := deleteRequest(t, srv, "/activity/blocklist/"+itoa(entries[0].ID)); resp.StatusCode != 200 {
		t.Fatalf("delete blocklist entry: %d", resp.StatusCode)
	}
	if _, total, _ := store.ListBlocklist(t.Context(), db, 50, 0); total != 0 {
		t.Errorf("want the entry removed, got %d", total)
	}
	_, body = get(t, srv, "/activity/blocklist")
	if !strings.Contains(body, "Nothing is blocklisted") {
		t.Errorf("want the empty notice, got:\n%s", body)
	}
}

// TestBlocklist_DeletedWithItsMovie: entries belong to their movie.
func TestBlocklist_DeletedWithItsMovie(t *testing.T) {
	db := openTestDB(t)
	movieID := seedTestMovie(t, db)
	if _, err := store.AddBlocklist(t.Context(), db, store.BlocklistEntry{MovieID: sql.NullInt64{Int64: movieID, Valid: true}, SourceTitle: "x", Protocol: "torrent"}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMovie(t.Context(), db, movieID); err != nil {
		t.Fatalf("delete movie with a blocklist entry: %v", err)
	}
	if _, total, _ := store.ListBlocklist(t.Context(), db, 50, 0); total != 0 {
		t.Errorf("want the entry gone with its movie, got %d", total)
	}
}

// TestFailedDownloadHandling_Settings: Redownload and per-client Remove
// Failed are saved from Settings -> Download Clients.
func TestFailedDownloadHandling_Settings(t *testing.T) {
	db := openTestDB(t)
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Download: &sync.DownloadService{DB: db}}))
	t.Cleanup(srv.Close)

	_, body := get(t, srv, "/settings/download-clients")
	if !strings.Contains(body, "Failed Download Handling") || strings.Contains(body, `name="redownload_failed" checked`) {
		t.Fatalf("want Failed Download Handling with Redownload off by default, got:\n%s", body)
	}
	resp, _ := postForm(t, srv, "/settings/download-handling", url.Values{"redownload_failed": {"on"}})
	if resp.Header.Get("HX-Redirect") != "/settings/download-clients" {
		t.Fatalf("want saved, got %d", resp.StatusCode)
	}
	if d, _ := store.GetDownloadHandling(t.Context(), db); !d.RedownloadFailed {
		t.Errorf("want Redownload saved on")
	}

	form := url.Values{"implementation": {"deluge"}, "name": {"Deluge"}, "enabled": {"on"}, "base_url": {"http://deluge:8112"}, "priority": {"1"}, "remove_failed": {"on"}}
	postForm(t, srv, "/settings/download-clients", form)
	clients, _ := store.ListDownloadClients(t.Context(), db)
	if len(clients) != 1 || !clients[0].RemoveFailed {
		t.Fatalf("want Remove Failed saved on the client, got %+v", clients)
	}
	_, body = get(t, srv, "/settings/download-clients/"+itoa(clients[0].ID)+"/edit")
	if !strings.Contains(body, `name="remove_failed" checked`) {
		t.Errorf("want Remove Failed shown ticked, got:\n%s", body)
	}
}

// TestBlocklist_RestAPI: Sonarr's queue delete with blocklist, and the
// blocklist endpoints.
func TestBlocklist_RestAPI(t *testing.T) {
	db := openTestDB(t)
	movieID := seedTestMovie(t, db)
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Download: &sync.DownloadService{DB: db}}))
	t.Cleanup(srv.Close)
	key, err := store.EnsureAPIKey(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	grabID, _ := store.InsertGrab(t.Context(), db, store.Grab{MovieID: sql.NullInt64{Int64: movieID, Valid: true},
		ReleaseTitle: "Inception.2010.720p", Indexer: "Tracker", Protocol: "torrent", DownloadClient: "deluge", Status: "failed"})

	if status, _, _ := apiCall(t, srv, key, "DELETE", "/api/v3/queue/"+itoa(grabID)+"?removeFromClient=false&blocklist=true&skipRedownload=true", nil); status != 200 {
		t.Fatalf("queue delete: %d", status)
	}
	status, page, _ := apiCall(t, srv, key, "GET", "/api/v3/blocklist", nil)
	records, _ := page["records"].([]any)
	if status != 200 || len(records) != 1 || records[0].(map[string]any)["sourceTitle"] != "Inception.2010.720p" {
		t.Fatalf("want the release in /api/v3/blocklist, got %d %v", status, page)
	}
	id := int64(records[0].(map[string]any)["id"].(float64))
	if status, _, _ := apiCall(t, srv, key, "DELETE", "/api/v3/blocklist/"+itoa(id), nil); status != 200 {
		t.Fatalf("blocklist delete: %d", status)
	}
	if _, total, _ := store.ListBlocklist(t.Context(), db, 50, 0); total != 0 {
		t.Errorf("want the blocklist empty, got %d", total)
	}
}

// TestTV_ContinuingFilterSeesTMDBStatuses: the Continuing and Ended filters
// compare against Sonarr's words, so TMDB's "Returning Series" and
// "Canceled" have to come out as continuing and ended.
func TestTV_ContinuingFilterSeesTMDBStatuses(t *testing.T) {
	db := openTestDB(t)
	id := seedTestSeries(t, db)
	srv := newTestServerWithDB(t, db)
	for raw, want := range map[string]string{"Returning Series": `data-status="continuing"`, "Canceled": `data-status="ended"`} {
		if _, err := db.Exec(`UPDATE series_metadata SET status = ? WHERE id = (SELECT series_metadata_id FROM series WHERE id = ?)`, raw, id); err != nil {
			t.Fatal(err)
		}
		_, body := get(t, srv, "/tv")
		if !strings.Contains(body, want) {
			t.Errorf("status %q: want %s on the card, got:\n%s", raw, want, body)
		}
	}
}
