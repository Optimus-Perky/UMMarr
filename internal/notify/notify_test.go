package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

type capture struct {
	path, body, auth, token string
}

func server(t *testing.T, c *capture, status int, reply string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.path, c.body, c.auth, c.token = r.URL.Path, string(b), r.Header.Get("Authorization"), r.Header.Get("X-Plex-Token")
		if strings.HasSuffix(r.URL.Path, "/library/sections") {
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{"Directory": []map[string]any{{"key": "1", "type": "movie", "title": "Movies"}, {"key": "2", "type": "show", "title": "TV"}}}})
			return
		}
		w.WriteHeader(status)
		w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCompose(t *testing.T) {
	m := Compose(store.Event{Event: store.EventImported, Title: "Inception (2010)", Detail: "Inception (2010).mkv", Quality: "Bluray-1080p", Source: "rss"})
	if m.Subject != "UMMarr - Imported: Inception (2010)" || !strings.Contains(m.Body, "Quality: Bluray-1080p") || m.Color != 0x6fbe8e {
		t.Fatalf("got %+v", m)
	}
}

func TestSenders(t *testing.T) {
	s := &Service{Sync: true}
	e := store.Event{Event: store.EventGrabbed, MediaType: "movie", Title: "Inception (2010)", Detail: "Inception 2010 1080p", Source: "interactive"}
	ctx := context.Background()

	var c capture
	web := server(t, &c, 200, "ok")
	if err := s.Send(ctx, store.Notification{Implementation: store.NotifyWebhook, Settings: map[string]string{"url": web.URL + "/hook", "username": "u", "password": "p"}}, e); err != nil {
		t.Fatalf("webhook: %v", err)
	}
	if c.path != "/hook" || !strings.Contains(c.body, `"eventType":"grabbed"`) || !strings.HasPrefix(c.auth, "Basic ") {
		t.Fatalf("webhook got %+v", c)
	}

	bad := server(t, &c, 500, "boom")
	if err := s.Send(ctx, store.Notification{Implementation: store.NotifyWebhook, Settings: map[string]string{"url": bad.URL}}, e); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("want the failure reported, got %v", err)
	}

	if err := s.Send(ctx, store.Notification{Implementation: store.NotifyDiscord, Settings: map[string]string{"webhook_url": "https://example.com/x"}}, e); err == nil {
		t.Fatalf("want a non-Discord URL refused")
	}

	push := server(t, &c, 200, `{"status":1}`)
	if err := s.Send(ctx, store.Notification{Implementation: store.NotifyPushover, Settings: map[string]string{"api_token": "t", "user_key": "u", "api_url": push.URL + "/1/messages.json"}}, e); err != nil {
		t.Fatalf("pushover: %v", err)
	}
	if !strings.Contains(c.body, "token=t") || !strings.Contains(c.body, "title=UMMarr+-+Grabbed") {
		t.Fatalf("pushover got %s", c.body)
	}

	tg := server(t, &c, 200, `{"ok":true}`)
	if err := s.Send(ctx, store.Notification{Implementation: store.NotifyTelegram, Settings: map[string]string{"bot_token": "123:abc", "chat_id": "42", "api_url": tg.URL}}, e); err != nil {
		t.Fatalf("telegram: %v", err)
	}
	if c.path != "/bot123:abc/sendMessage" || !strings.Contains(c.body, `"chat_id":"42"`) {
		t.Fatalf("telegram got %+v", c)
	}

	plex := server(t, &c, 200, "")
	imported := e
	imported.Event = store.EventImported
	if err := s.Send(ctx, store.Notification{Implementation: store.NotifyPlex, Settings: map[string]string{"server_url": plex.URL, "token": "tok"}}, imported); err != nil {
		t.Fatalf("plex: %v", err)
	}
	if c.path != "/library/sections/1/refresh" || c.token != "tok" {
		t.Fatalf("want the movie library refreshed, got %+v", c)
	}
	if err := s.Test(ctx, store.Notification{Implementation: store.NotifyPlex, Settings: map[string]string{"server_url": plex.URL, "token": "tok"}}); err != nil {
		t.Fatalf("plex test: %v", err)
	}
}

func TestWants(t *testing.T) {
	n := store.Notification{OnGrab: true, OnFailed: true}
	if !n.Wants(store.EventGrabbed) || !n.Wants(store.EventImportFailed) || n.Wants(store.EventImported) || n.Wants(store.EventRenamed) {
		t.Fatalf("event flags not honoured")
	}
}
