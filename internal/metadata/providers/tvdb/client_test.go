package tvdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type fake struct {
	mu     sync.Mutex
	logins int
	pin    string
	pages  int
}

func (f *fake) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/login":
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["apikey"] != "key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.logins++
		f.pin = body["pin"]
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"token": "tok"}})
	case r.Header.Get("Authorization") != "Bearer tok":
		w.WriteHeader(http.StatusUnauthorized)
	case r.URL.Path == "/search":
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"tvdb_id": "446831", "name": "MobLand", "year": "2025"}}})
	case strings.HasSuffix(r.URL.Path, "/extended"):
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"id": 446831, "name": "MobLand", "overview": "Two mob families clash.", "firstAired": "2025-03-30",
			"averageRuntime": 55, "image": "https://artworks.thetvdb.com/poster.jpg",
			"originalNetwork": map[string]string{"name": "Paramount+"}, "status": map[string]string{"name": "Continuing"},
			"genres":    []map[string]string{{"name": "Drama"}, {"name": "Crime"}},
			"remoteIds": []map[string]string{{"id": "tt31510819", "sourceName": "IMDB"}},
		}})
	case strings.HasSuffix(r.URL.Path, "/episodes/default"):
		f.pages++
		if r.URL.Query().Get("page") == "0" {
			json.NewEncoder(w).Encode(map[string]any{
				"data":  map[string]any{"episodes": []map[string]any{{"seasonNumber": 2, "number": 5, "name": "Real Name", "aired": "2026-10-16", "runtime": 55}}},
				"links": map[string]any{"next": "page=1"}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data":  map[string]any{"episodes": []map[string]any{{"seasonNumber": 2, "number": 6, "name": "Another", "aired": "2026-10-23"}}},
			"links": map[string]any{}})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestTVDB(t *testing.T) {
	ctx := context.Background()
	f := &fake{}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	c, err := New(Options{APIKey: "key", PIN: "1234", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Test(ctx); err != nil {
		t.Fatalf("test: %v", err)
	}
	series, err := c.GetSeries(ctx, 446831)
	if err != nil {
		t.Fatal(err)
	}
	if series.Name != "MobLand" || series.Status.Name != "Continuing" || series.OriginalNetwork.Name != "Paramount+" || len(series.Genres) != 2 {
		t.Errorf("want the series details, got %+v", series)
	}
	episodes, err := c.GetEpisodes(ctx, 446831)
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 2 || episodes[0].Name != "Real Name" || episodes[1].Number != 6 {
		t.Errorf("want both pages of episodes, got %+v", episodes)
	}
	if f.pages != 2 {
		t.Errorf("want paging followed once, got %d page requests", f.pages)
	}
	if f.logins != 1 {
		t.Errorf("want one login reused for every call, got %d", f.logins)
	}
	if f.pin != "1234" {
		t.Errorf("want the subscriber PIN sent, got %q", f.pin)
	}
	if results, err := c.SearchSeries(ctx, "mobland"); err != nil || len(results) != 1 || results[0].ID != "446831" {
		t.Errorf("search: %v %+v", err, results)
	}

	bad, _ := New(Options{APIKey: "wrong", BaseURL: srv.URL})
	if err := bad.Test(ctx); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("want a clear message for a bad key, got %v", err)
	}
	if _, err := New(Options{}); err == nil {
		t.Errorf("want a client with no key refused")
	}
}
