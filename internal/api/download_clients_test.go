package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// TestDownloadClients_Settings: clients are added, tested and edited from
// Settings, and the protocols they cover decide what can be grabbed.
func TestDownloadClients_Settings(t *testing.T) {
	db := openTestDB(t)
	download := &sync.DownloadService{DB: db}
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Download: download, BootstrapDelugeBaseURL: "http://deluge.env:8112"}))
	t.Cleanup(srv.Close)
	sab := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") != "sabkey" {
			json.NewEncoder(w).Encode(map[string]any{"status": false, "error": "API Key Incorrect"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"version": "4.3.0"})
	}))
	t.Cleanup(sab.Close)

	_, body := get(t, srv, "/settings/download-clients")
	if !strings.Contains(body, `data-section="download-clients"`) || !strings.Contains(body, "From .env") {
		t.Fatalf("want the Download Clients panel showing the .env Deluge, got:\n%s", body)
	}
	_, body = get(t, srv, "/settings/download-clients/new?implementation=sabnzbd")
	if !strings.Contains(body, "Add Download Client - SABnzbd") || !strings.Contains(body, `name="api_key"`) {
		t.Fatalf("want the SABnzbd form, got:\n%s", body)
	}

	form := url.Values{"implementation": {"sabnzbd"}, "name": {"SAB"}, "enabled": {"on"}, "base_url": {sab.URL}, "api_key": {"wrong"}, "priority": {"1"}}
	_, body = postForm(t, srv, "/settings/download-clients/test", form)
	if !strings.Contains(body, "API Key Incorrect") {
		t.Fatalf("want the bad key reported by Test, got:\n%s", body)
	}
	form.Set("api_key", "sabkey")
	_, body = postForm(t, srv, "/settings/download-clients/test", form)
	if !strings.Contains(body, "Test passed") {
		t.Fatalf("want Test to pass, got:\n%s", body)
	}
	resp, body := postForm(t, srv, "/settings/download-clients", form)
	if resp.Header.Get("HX-Redirect") != "/settings/download-clients" {
		t.Fatalf("want the client saved, got:\n%s", body)
	}
	clients, _ := store.ListDownloadClients(t.Context(), db)
	if len(clients) != 1 || clients[0].Name != "SAB" || clients[0].APIKey != "sabkey" || clients[0].Protocol() != "usenet" {
		t.Fatalf("want one SABnzbd client saved, got %+v", clients)
	}
	if p := download.EnabledProtocols(t.Context()); !p["usenet"] || p["torrent"] {
		t.Fatalf("want usenet available and torrent not (the .env Deluge yields to saved clients), got %v", p)
	}

	// Editing with a blank key keeps the saved one; the name must stay unique.
	edit := url.Values{"implementation": {"sabnzbd"}, "name": {"SAB two"}, "enabled": {"on"}, "base_url": {sab.URL}, "api_key": {""}, "priority": {"5"}}
	postForm(t, srv, "/settings/download-clients/"+itoa(clients[0].ID), edit)
	clients, _ = store.ListDownloadClients(t.Context(), db)
	if clients[0].Name != "SAB two" || clients[0].APIKey != "sabkey" || clients[0].Priority != 5 {
		t.Fatalf("want the edit saved with the key kept, got %+v", clients[0])
	}
	dup := url.Values{"implementation": {"qbittorrent"}, "name": {"SAB two"}, "enabled": {"on"}, "base_url": {"http://qbit:8080"}, "priority": {"1"}}
	_, body = postForm(t, srv, "/settings/download-clients", dup)
	if !strings.Contains(body, "already has that name") {
		t.Fatalf("want a duplicate name refused, got:\n%s", body)
	}
	_, body = get(t, srv, "/settings/download-clients")
	if !strings.Contains(body, "SAB two") || strings.Contains(body, "From .env") {
		t.Fatalf("want the saved client on the page instead of the .env one, got:\n%s", body)
	}
}
