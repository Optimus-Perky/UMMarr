package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func arrRequest(t *testing.T, srv *httptest.Server, method, path, key string, body any) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if key != "" {
		req.Header.Set("X-Api-Key", key)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

type arrField struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type arrIndexer struct {
	ID                      int64      `json:"id"`
	Name                    string     `json:"name"`
	Implementation          string     `json:"implementation"`
	ConfigContract          string     `json:"configContract"`
	Protocol                string     `json:"protocol"`
	Priority                int        `json:"priority"`
	EnableRss               bool       `json:"enableRss"`
	EnableAutomaticSearch   bool       `json:"enableAutomaticSearch"`
	EnableInteractiveSearch bool       `json:"enableInteractiveSearch"`
	Tags                    []int      `json:"tags"`
	Fields                  []arrField `json:"fields"`
}

func (ix *arrIndexer) field(name string) *arrField {
	for i := range ix.Fields {
		if ix.Fields[i].Name == name {
			return &ix.Fields[i]
		}
	}
	return nil
}

func TestArrAPI_NeedsTheAPIKey(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	key, err := store.EnsureAPIKey(t.Context(), db)
	if err != nil {
		t.Fatalf("api key: %v", err)
	}

	for name, k := range map[string]string{"no key": "", "wrong key": "not-the-key"} {
		resp, _ := arrRequest(t, srv, http.MethodGet, "/api/v3/indexer", k, nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: want 401, got %d", name, resp.StatusCode)
		}
	}
	resp, body := arrRequest(t, srv, http.MethodGet, "/api/v3/system/status", key, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"version":"5.`) || resp.Header.Get("X-Application-Version") == "" {
		t.Fatalf("want the status with a Radarr 5 version header, got %d %s", resp.StatusCode, body)
	}
	resp, _ = arrRequest(t, srv, http.MethodGet, "/api/v3/indexer?apikey="+key, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want the apikey query accepted too, got %d", resp.StatusCode)
	}
}

// TestArrAPI_ProwlarrSync replays what Prowlarr's Radarr app does
// (RadarrV3Proxy / Radarr.cs): read the schema, add an indexer (retrying with
// forceSave when the test fails), read it back and compare it (the API key
// comes back masked), update it, and remove it.
func TestArrAPI_ProwlarrSync(t *testing.T) {
	indexer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(torznabFeed())) // answers, but with nothing: Radarr's test fails
	}))
	defer indexer.Close()
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	key, _ := store.EnsureAPIKey(t.Context(), db)
	prowlarrURL := indexer.URL

	// An entry converted from the old Prowlarr connection for the same indexer.
	convertedID, err := store.CreateIndexer(t.Context(), db, store.Indexer{
		Name: "IPTorrents (Prowlarr)", Implementation: "Torznab", Priority: 25, BaseURL: prowlarrURL + "/3/", APIPath: "/api", APIKey: "prowlarr-key",
		EnableInteractiveSearch: true, Categories: []int{2000, 5000, 3000}, Converted: true,
	})
	if err != nil {
		t.Fatalf("create converted indexer: %v", err)
	}

	resp, body := arrRequest(t, srv, http.MethodGet, "/api/v3/indexer/schema", key, nil)
	var schemas []arrIndexer
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &schemas) != nil || len(schemas) != 2 {
		t.Fatalf("want two schemas, got %d %s", resp.StatusCode, body)
	}
	var torznab arrIndexer
	for _, s := range schemas {
		if s.Implementation == "Torznab" {
			torznab = s
		}
	}
	for _, name := range []string{"baseUrl", "apiPath", "apiKey", "categories", "minimumSeeders", "seedCriteria.seedRatio", "seedCriteria.seedTime", "rejectBlocklistedTorrentHashesWhileGrabbing", "multiLanguages", "removeYear", "requiredFlags", "additionalParameters"} {
		if torznab.field(name) == nil {
			t.Errorf("want the Torznab schema to have %q", name)
		}
	}
	if torznab.ConfigContract != "TorznabSettings" {
		t.Errorf("want Radarr's config contract, got %q", torznab.ConfigContract)
	}

	// BuildRadarrIndexer: the schema's sync fields with Prowlarr's values.
	add := arrIndexer{
		Name: "IPTorrents (Prowlarr)", Implementation: "Torznab", ConfigContract: "TorznabSettings", Priority: 10,
		EnableRss: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true, Tags: []int{},
		Fields: []arrField{
			{"baseUrl", prowlarrURL + "/3/"}, {"apiPath", "/api"}, {"apiKey", "prowlarr-key"},
			{"categories", []int{2000, 2040, 5000, 5040, 3000}}, {"minimumSeeders", 2},
			{"seedCriteria.seedRatio", 1.5}, {"seedCriteria.seedTime", nil}, {"rejectBlocklistedTorrentHashesWhileGrabbing", false},
			{"multiLanguages", []int{}}, {"removeYear", false}, {"requiredFlags", []int{}}, {"additionalParameters", nil},
		},
	}
	resp, body = arrRequest(t, srv, http.MethodPost, "/api/v3/indexer", key, add)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "no results in the configured categories") {
		t.Fatalf("want the failing test refused with 400, got %d %s", resp.StatusCode, body)
	}
	resp, body = arrRequest(t, srv, http.MethodPost, "/api/v3/indexer?forceSave=true", key, add)
	var created arrIndexer
	if resp.StatusCode != http.StatusCreated || json.Unmarshal(body, &created) != nil || created.ID == 0 {
		t.Fatalf("want the forced add created, got %d %s", resp.StatusCode, body)
	}
	if f := created.field("apiKey"); f == nil || f.Value != "********" {
		t.Errorf("want the API key masked, got %+v", f)
	}
	if f := created.field("multiLanguages"); f == nil {
		t.Errorf("want fields UMMarr doesn't use returned as sent")
	}
	saved, _ := store.GetIndexer(t.Context(), db, created.ID)
	if !saved.Synced || saved.APIKey != "prowlarr-key" || saved.Priority != 10 || saved.MinimumSeeders != 2 || saved.SeedRatio.Float64 != 1.5 || len(saved.Categories) != 5 {
		t.Fatalf("want the synced indexer saved with Prowlarr's values, got %+v", saved)
	}
	if _, err := store.GetIndexer(t.Context(), db, convertedID); err == nil {
		t.Fatalf("want the converted entry removed now the synced one covers all its categories")
	}

	resp, body = arrRequest(t, srv, http.MethodGet, "/api/v3/indexer", key, nil)
	var listed []arrIndexer
	if json.Unmarshal(body, &listed) != nil || len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("want the synced indexer listed, got %d %s", resp.StatusCode, body)
	}

	// UpdateIndexer: Prowlarr sends the masked key back and new categories.
	update := listed[0]
	update.field("categories").Value = []int{2000}
	resp, body = arrRequest(t, srv, http.MethodPut, "/api/v3/indexer/"+strconv.FormatInt(created.ID, 10)+"?forceSave=true", key, update)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want the update accepted, got %d %s", resp.StatusCode, body)
	}
	saved, _ = store.GetIndexer(t.Context(), db, created.ID)
	if saved.APIKey != "prowlarr-key" || len(saved.Categories) != 1 {
		t.Fatalf("want the masked key to keep the saved key and the categories updated, got %+v", saved)
	}

	resp, _ = arrRequest(t, srv, http.MethodPost, "/api/v3/indexer/test", key, map[string]any{"id": 0, "name": "Test", "implementation": "Newznab", "fields": []any{}})
	if resp.Header.Get("X-Application-Version") == "" {
		t.Fatalf("want the version header on the app test")
	}

	resp, _ = arrRequest(t, srv, http.MethodDelete, "/api/v3/indexer/"+strconv.FormatInt(created.ID, 10), key, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want delete to succeed, got %d", resp.StatusCode)
	}
	if resp, _ = arrRequest(t, srv, http.MethodGet, "/api/v3/indexer/"+strconv.FormatInt(created.ID, 10), key, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 after delete, got %d", resp.StatusCode)
	}
}

func TestArrAPI_ValidationAndHiddenConvertedEntries(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	key, _ := store.EnsureAPIKey(t.Context(), db)
	convertedID, _ := store.CreateIndexer(t.Context(), db, store.Indexer{Name: "Old", Implementation: "Torznab", Priority: 25, BaseURL: "http://p/1/", APIPath: "/api", Converted: true, Categories: []int{2000}})
	syncedID, _ := store.CreateIndexer(t.Context(), db, store.Indexer{Name: "Taken", Implementation: "Torznab", Priority: 25, BaseURL: "http://p/2/", APIPath: "/api", Synced: true, Categories: []int{2000}})

	if resp, _ := arrRequest(t, srv, http.MethodGet, "/api/v3/indexer/"+strconv.FormatInt(convertedID, 10), key, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want converted entries hidden from the API, got %d", resp.StatusCode)
	}
	_, body := arrRequest(t, srv, http.MethodGet, "/api/v3/indexer", key, nil)
	if strings.Contains(string(body), `"Old"`) || !strings.Contains(string(body), `"Taken"`) {
		t.Fatalf("want only non-converted indexers listed, got %s", body)
	}

	bad := arrIndexer{Name: "Taken", Implementation: "Torznab", EnableRss: true, Fields: []arrField{{"baseUrl", "prowlarr"}, {"apiPath", "api"}}}
	resp, body := arrRequest(t, srv, http.MethodPost, "/api/v3/indexer?forceSave=true", key, bad)
	for _, want := range []string{"Should be unique", "Invalid Url", "valid URL path", "'Categories' must be provided"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("want %q in the 400, got %d %s", want, resp.StatusCode, body)
		}
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
	_ = syncedID
}

func TestSettings_APIKeyIsFetchedNotEmbedded(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	key, _ := store.EnsureAPIKey(t.Context(), db)

	_, page := get(t, srv, "/settings/general")
	if strings.Contains(page, key) || !strings.Contains(page, `id="api-key"`) || !strings.Contains(page, "Sync Categories") {
		t.Fatalf("want the API key panel without the key in the page")
	}
	if _, body := get(t, srv, "/settings/api-key"); body != key {
		t.Fatalf("want the key from the Show endpoint, got %q", body)
	}
	postForm(t, srv, "/settings/api-key/regenerate", nil)
	if _, body := get(t, srv, "/settings/api-key"); body == key || len(body) != 32 {
		t.Fatalf("want a new 32 character key after regenerating, got %q", body)
	}
}
