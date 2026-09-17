package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/notify"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// TestNotifications_Settings: a connection is added and tested from
// Settings, and a recorded event reaches it.
func TestNotifications_Settings(t *testing.T) {
	db := openTestDB(t)
	var got []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, string(b))
	}))
	t.Cleanup(hook.Close)
	notifier := &notify.Service{DB: db, Sync: true}
	events := &sync.Events{DB: db, Listeners: []sync.Listener{notifier}}
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Notifier: notifier, Events: events}))
	t.Cleanup(srv.Close)

	_, body := get(t, srv, "/settings/connect")
	if !strings.Contains(body, `data-section="connect"`) || !strings.Contains(body, "implementation=discord") {
		t.Fatalf("want the Connect panel with an Add dialog, got:\n%s", body)
	}
	form := url.Values{"implementation": {"webhook"}, "name": {"My hook"}, "enabled": {"on"}, "url": {hook.URL + "/x"}, "method": {"POST"}, "on_grab": {"on"}, "on_added": {"on"}}
	_, body = postForm(t, srv, "/settings/notifications/test", form)
	if !strings.Contains(body, "Test sent") || len(got) != 1 || !strings.Contains(got[0], `"title":"UMMarr test"`) {
		t.Fatalf("want a test message delivered, got %q / %v", body, got)
	}
	resp, body := postForm(t, srv, "/settings/notifications", form)
	if resp.Header.Get("HX-Redirect") != "/settings/connect" {
		t.Fatalf("want the connection saved, got:\n%s", body)
	}
	saved, _ := store.ListNotifications(t.Context(), db)
	if len(saved) != 1 || !saved[0].OnGrab || saved[0].OnImport || saved[0].Settings["url"] != hook.URL+"/x" {
		t.Fatalf("want the ticked events and URL saved, got %+v", saved)
	}

	// An event it wants reaches it; one it doesn't is skipped.
	events.Record(t.Context(), store.Event{Event: store.EventGrabbed, MediaType: "movie", Title: "Inception (2010)", Detail: "Inception.2010.1080p"})
	events.Record(t.Context(), store.Event{Event: store.EventImported, MediaType: "movie", Title: "Inception (2010)"})
	if len(got) != 2 || !strings.Contains(got[1], `"eventType":"grabbed"`) {
		t.Fatalf("want only the grab delivered, got %v", got)
	}
	_, body = get(t, srv, "/settings/connect")
	if !strings.Contains(body, "My hook") || !strings.Contains(body, "On: Grab, Added") {
		t.Fatalf("want the card with its events, got:\n%s", body)
	}
}
