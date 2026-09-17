package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/importlist"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// TestImportLists_Settings: a list is added from Settings, Test previews
// it, and exclusions can be added and removed.
func TestImportLists_Settings(t *testing.T) {
	db := openTestDB(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/movie/popular" {
			json.NewEncoder(w).Encode(map[string]any{"page": 1, "total_pages": 1, "results": []map[string]any{{"id": 603, "title": "The Matrix", "release_date": "1999-03-31"}, {"id": 27205, "title": "Inception", "release_date": "2010-07-16"}}})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(fake.Close)
	client := tmdb.New(tmdb.Options{Token: "test", BaseURL: fake.URL})
	lists := &importlist.Service{DB: db, TMDB: client, Movies: &sync.MovieService{DB: db, TMDB: client}}
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, TMDB: client, ImportLists: lists}))
	t.Cleanup(srv.Close)
	seedTestMovie(t, db) // Inception is already tracked

	_, body := get(t, srv, "/settings/import-lists")
	if !strings.Contains(body, `data-section="import-lists"`) || !strings.Contains(body, "implementation=tmdb_popular") {
		t.Fatalf("want the Import Lists panel, got:\n%s", body)
	}
	_, body = get(t, srv, "/settings/import-lists/new?implementation=tmdb_popular")
	if !strings.Contains(body, "Add Import List - TMDB Popular") || !strings.Contains(body, `name="limit"`) {
		t.Fatalf("want the popular form, got:\n%s", body)
	}
	form := url.Values{"implementation": {"tmdb_popular"}, "media_type": {"movie"}, "name": {"Popular movies"}, "enabled": {"on"}, "limit": {"10"}, "auto_add": {"on"}, "monitored": {"on"}}
	_, body = postForm(t, srv, "/settings/import-lists/test", form)
	if !strings.Contains(body, "Found 2 titles, 1 not yet in the library: The Matrix (1999)") {
		t.Fatalf("want a preview naming the new title, got:\n%s", body)
	}
	resp, body := postForm(t, srv, "/settings/import-lists", form)
	if resp.Header.Get("HX-Redirect") != "/settings/import-lists" {
		t.Fatalf("want the list saved, got:\n%s", body)
	}
	saved, _ := store.ListImportLists(t.Context(), db)
	if len(saved) != 1 || saved[0].Settings["limit"] != "10" || !saved[0].AutoAdd || saved[0].MediaType != "movie" {
		t.Fatalf("want the list saved with its settings, got %+v", saved)
	}
	postForm(t, srv, "/settings/import-lists/exclusions", url.Values{"media_type": {"movie"}, "tmdb_id": {"603"}, "title": {"The Matrix"}})
	_, body = postForm(t, srv, "/settings/import-lists/test", form)
	if !strings.Contains(body, "Found 2 titles, 0 not yet in the library") {
		t.Fatalf("want the exclusion honoured, got:\n%s", body)
	}
	_, body = get(t, srv, "/settings/import-lists")
	if !strings.Contains(body, "The Matrix") || !strings.Contains(body, "Popular movies") {
		t.Fatalf("want the exclusion and the list shown, got:\n%s", body)
	}
}
