package api_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func postForm(t *testing.T, srv *httptest.Server, path string, values url.Values) (*http.Response, string) {
	t.Helper()
	resp, err := http.PostForm(srv.URL+path, values)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp, readBody(t, resp)
}

func validIndexerForm() url.Values {
	return url.Values{
		"implementation": {"Torznab"}, "name": {"IPTorrents"},
		"enable_rss": {"on"}, "enable_interactive_search": {"on"},
		"base_url": {"http://prowlarr:9696/3/"}, "api_path": {"/api"}, "api_key": {"first-key"},
		"categories": {"2000", "5000"}, "anime_categories": {"5070"},
		"priority": {"10"}, "minimum_seeders": {"2"}, "seed_ratio": {"1.5"}, "seed_time": {""},
		"required_flags": {"1"},
	}
}

func TestIndexerSettings_AddEditDelete(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)

	_, form := get(t, srv, "/settings/indexers/new?implementation=Torznab")
	for _, want := range []string{`name="categories" value="2000" checked`, `name="categories" value="5000" checked`, `name="categories" value="3000" checked`, "Seed Ratio", "Indexer Priority"} {
		if !strings.Contains(form, want) {
			t.Errorf("want the new Torznab form to contain %q", want)
		}
	}
	if status, _ := get(t, srv, "/settings/indexers/new?implementation=Cardigann"); status != http.StatusBadRequest {
		t.Errorf("want an unknown implementation refused, got %d", status)
	}

	_, body := postForm(t, srv, "/settings/indexers", url.Values{"implementation": {"Torznab"}, "base_url": {"prowlarr"}, "priority": {"99"}, "minimum_seeders": {"1"}})
	for _, want := range []string{"Nothing was saved", "Name is required.", "starting with http://", "Pick at least one category.", "from 1 (highest) to 50"} {
		if !strings.Contains(body, want) {
			t.Errorf("want the invalid form re-rendered with %q, got:\n%s", want, body)
		}
	}
	if list, _ := store.ListIndexers(t.Context(), db); len(list) != 0 {
		t.Fatalf("want nothing saved from an invalid form, got %+v", list)
	}

	resp, _ := postForm(t, srv, "/settings/indexers", validIndexerForm())
	if resp.Header.Get("HX-Redirect") != "/settings/indexers" {
		t.Fatalf("want a redirect back to Settings after saving, got %d", resp.StatusCode)
	}
	list, _ := store.ListIndexers(t.Context(), db)
	if len(list) != 1 {
		t.Fatalf("want 1 indexer, got %+v", list)
	}
	ix := list[0]
	if ix.Name != "IPTorrents" || ix.APIKey != "first-key" || ix.Priority != 10 || ix.MinimumSeeders != 2 || !ix.SeedRatio.Valid ||
		ix.SeedRatio.Float64 != 1.5 || ix.SeedTime.Valid || len(ix.Categories) != 2 || ix.AnimeCategories[0] != 5070 ||
		!ix.EnableRSS || ix.EnableAutomaticSearch || !ix.EnableInteractiveSearch || ix.RequiredFlags[0] != 1 {
		t.Fatalf("want the form's values saved, got %+v", ix)
	}
	id := strconv.FormatInt(ix.ID, 10)

	_, page := get(t, srv, "/settings/indexers")
	if !strings.Contains(page, "IPTorrents") || !strings.Contains(page, "Priority: 10") || !strings.Contains(page, "Movies · TV") || strings.Contains(page, "first-key") {
		t.Fatalf("want the indexer card without its key, got:\n%s", page)
	}
	_, edit := get(t, srv, "/settings/indexers/"+id+"/edit")
	if strings.Contains(edit, "first-key") || !strings.Contains(edit, "leave blank to keep") || !strings.Contains(edit, `value="1.5"`) {
		t.Fatalf("want the edit form to hide the key but keep the other values, got:\n%s", edit)
	}

	values := validIndexerForm()
	values.Set("api_key", "")
	values.Set("name", "IPTorrents (renamed)")
	postForm(t, srv, "/settings/indexers/"+id, values)
	ix, _ = store.GetIndexer(t.Context(), db, ix.ID)
	if ix.Name != "IPTorrents (renamed)" || ix.APIKey != "first-key" {
		t.Fatalf("want the rename saved and a blank key to keep the saved one, got %+v", ix)
	}

	if resp := deleteRequest(t, srv, "/settings/indexers/"+id); resp.Header.Get("HX-Redirect") != "/settings/indexers" {
		t.Fatalf("want delete to redirect to Settings, got %d", resp.StatusCode)
	}
	if list, _ := store.ListIndexers(t.Context(), db); len(list) != 0 {
		t.Fatalf("want the indexer deleted, got %+v", list)
	}
}

func TestIndexerSettings_TestButtonUsesSavedKey(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	indexer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") != "saved-key" {
			w.Write([]byte(`<error code="100" description="Incorrect user credentials"/>`))
			return
		}
		w.Write([]byte(torznabFeed(torznabItem("a", "Something", ""))))
	}))
	defer indexer.Close()

	values := validIndexerForm()
	values.Set("base_url", indexer.URL)
	values.Set("api_key", "wrong-key")
	_, body := postForm(t, srv, "/settings/indexers/test", values)
	if !strings.Contains(body, "invalid API key") {
		t.Fatalf("want the indexer's rejection shown, got:\n%s", body)
	}

	values.Set("api_key", "saved-key")
	postForm(t, srv, "/settings/indexers", values)
	list, _ := store.ListIndexers(t.Context(), db)
	values.Set("api_key", "")
	values.Set("id", strconv.FormatInt(list[0].ID, 10))
	_, body = postForm(t, srv, "/settings/indexers/test", values)
	if !strings.Contains(body, "Test passed.") {
		t.Fatalf("want a blank key field to test with the saved key, got:\n%s", body)
	}
}

func TestIndexerSettings_TestAllRecordsFailures(t *testing.T) {
	srv, db, _ := newTestServerWithIndexer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }, func(w http.ResponseWriter, r *http.Request) {})

	_, body := postForm(t, srv, "/settings/indexers/test-all", nil)
	if !strings.Contains(body, "SomeIndexer") || !strings.Contains(body, "HTTP 502") {
		t.Fatalf("want each failing indexer listed with its error, got:\n%s", body)
	}
	list, _ := store.ListIndexers(t.Context(), db)
	if list[0].Failures != 1 || list[0].LastError == "" {
		t.Fatalf("want the failure recorded, got %+v", list[0])
	}
	_, page := get(t, srv, "/settings/indexers")
	if !strings.Contains(page, "not used until") {
		t.Fatalf("want the resting indexer's card to say so, got:\n%s", page)
	}
}

func TestIndexerSettings_ManageIndexers(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	var ids []string
	for _, name := range []string{"One", "Two", "Three"} {
		id, err := store.CreateIndexer(t.Context(), db, store.Indexer{
			Name: name, Implementation: "Torznab", Priority: 25, BaseURL: "http://example.invalid/" + name, APIPath: "/api",
			EnableRSS: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true, Categories: []int{2000},
		})
		if err != nil {
			t.Fatalf("create indexer: %v", err)
		}
		ids = append(ids, strconv.FormatInt(id, 10))
	}

	_, body := postForm(t, srv, "/settings/indexers/bulk", url.Values{"enable_rss": {"disable"}})
	if !strings.Contains(body, "Select at least one indexer.") {
		t.Fatalf("want a selection required, got:\n%s", body)
	}
	postForm(t, srv, "/settings/indexers/bulk", url.Values{"ids": {ids[0], ids[1]}, "enable_rss": {"disable"}, "priority": {"5"}})
	postForm(t, srv, "/settings/indexers/bulk", url.Values{"ids": {ids[2]}, "action": {"delete"}})

	list, _ := store.ListIndexers(t.Context(), db)
	if len(list) != 2 {
		t.Fatalf("want Three deleted, got %+v", list)
	}
	for _, ix := range list {
		if ix.EnableRSS || !ix.EnableAutomaticSearch || ix.Priority != 5 {
			t.Fatalf("want RSS off and priority 5 with automatic search untouched, got %+v", ix)
		}
	}
}
