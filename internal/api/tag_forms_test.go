package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// The download client, notification and indexer dialogs save typed tags
// and show them again on edit.
func TestTagsRoundTripThroughTheSettingsDialogs(t *testing.T) {
	db := openTestDB(t)
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db}))
	t.Cleanup(srv.Close)
	ctx := context.Background()

	post := func(path string, form url.Values) {
		t.Helper()
		resp, err := http.PostForm(srv.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.Header.Get("HX-Redirect") == "" {
			t.Fatalf("POST %s: want a redirect after saving, got %s", path, body)
		}
	}
	get := func(path string) string {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(body)
	}

	post("/settings/download-clients", url.Values{"implementation": {"qbittorrent"}, "name": {"Anime box"}, "enabled": {"on"},
		"base_url": {"http://qbit:8080"}, "priority": {"1"}, "tags": {"Anime, kids"}})
	clients, _ := store.ListDownloadClients(ctx, db)
	if len(clients) != 1 || len(clients[0].Tags) != 2 {
		t.Fatalf("want the client saved with two tags, got %+v", clients)
	}
	if body := get("/settings/download-clients/" + strconv.FormatInt(clients[0].ID, 10) + "/edit"); !strings.Contains(body, `name="tags" value="anime, kids"`) {
		t.Fatalf("want the tags shown on edit, got:\n%s", body)
	}

	post("/settings/notifications", url.Values{"implementation": {"webhook"}, "name": {"Kids hook"}, "enabled": {"on"},
		"url": {"http://hook.example/x"}, "on_import": {"on"}, "tags": {"kids"}})
	notifications, _ := store.ListNotifications(ctx, db)
	if len(notifications) != 1 || len(notifications[0].Tags) != 1 {
		t.Fatalf("want the notification saved with one tag, got %+v", notifications)
	}
	if body := get("/settings/notifications/" + strconv.FormatInt(notifications[0].ID, 10) + "/edit"); !strings.Contains(body, `name="tags" value="kids"`) {
		t.Fatalf("want the tag shown on edit, got:\n%s", body)
	}

	if body := get("/settings/indexers/new?implementation=Torznab"); !strings.Contains(body, `name="tags"`) || !strings.Contains(body, `<option value="`+strconv.FormatInt(clients[0].ID, 10)+`">Anime box</option>`) {
		t.Fatalf("want a tags box and the download clients on the indexer dialog, got:\n%s", body)
	}
}
