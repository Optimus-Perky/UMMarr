package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
	"github.com/Optimus-Perky/UMMarr/internal/tasks"
)

func apiCall(t *testing.T, srv *httptest.Server, key, method, path string, body any) (int, map[string]any, []map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, srv.URL+path, reader)
	if key != "" {
		req.Header.Set("X-Api-Key", key)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var obj map[string]any
	var list []map[string]any
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "{") {
		json.Unmarshal(data, &obj)
	} else if strings.HasPrefix(trimmed, "[") {
		json.Unmarshal(data, &list)
	}
	return resp.StatusCode, obj, list
}

// TestRestAPI: the library is readable and writable over JSON with the API
// key, and commands reach the scheduler.
func TestRestAPI(t *testing.T) {
	db := openTestDB(t)
	client := fixMatchTMDB(t)
	scheduler := tasks.New()
	ran := make(chan struct{}, 1)
	scheduler.Register(&tasks.Task{Name: "RSS sync", Run: func(context.Context) error { ran <- struct{}{}; return nil }})
	events := &sync.Events{DB: db}
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, TMDB: client, Movies: &sync.MovieService{DB: db, TMDB: client}, Import: &sync.ImportService{DB: db, Events: events}, Events: events, Tasks: scheduler}))
	t.Cleanup(srv.Close)
	key, err := store.EnsureAPIKey(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRootFolder(t.Context(), db, t.TempDir(), "movie"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateQualityProfile(t.Context(), db, "Any"); err != nil {
		t.Fatal(err)
	}

	if status, _, _ := apiCall(t, srv, "", http.MethodGet, "/api/v3/movie", nil); status != http.StatusUnauthorized {
		t.Fatalf("want 401 without a key, got %d", status)
	}
	status, list, _ := apiCall(t, srv, key, http.MethodGet, "/api/v3/movie/lookup?term=matrix", nil)
	_ = list
	if status != http.StatusOK {
		t.Fatalf("lookup: %d", status)
	}
	status, movie, _ := apiCall(t, srv, key, http.MethodPost, "/api/v3/movie", map[string]any{"tmdbId": 603})
	if status != http.StatusCreated || movie["title"] != "The Matrix" || movie["tmdbId"] != float64(603) || movie["monitored"] != true {
		t.Fatalf("add: %d %+v", status, movie)
	}
	id := int64(movie["id"].(float64))
	status, _, movies := apiCall(t, srv, key, http.MethodGet, "/api/v3/movie", nil)
	if status != http.StatusOK || len(movies) != 1 || movies[0]["qualityProfileName"] != "Any" {
		t.Fatalf("list: %d %+v", status, movies)
	}
	status, movie, _ = apiCall(t, srv, key, http.MethodPut, "/api/v3/movie/"+itoa(id), map[string]any{"monitored": false})
	if status != http.StatusOK || movie["monitored"] != false {
		t.Fatalf("update: %d %+v", status, movie)
	}
	status, history, _ := apiCall(t, srv, key, http.MethodGet, "/api/v3/history?eventType=added", nil)
	if status != http.StatusOK || history["totalRecords"] != float64(1) {
		t.Fatalf("history: %d %+v", status, history)
	}
	status, cmd, _ := apiCall(t, srv, key, http.MethodPost, "/api/v3/command", map[string]any{"name": "RssSync"})
	if status != http.StatusCreated || cmd["status"] != "started" {
		t.Fatalf("command: %d %+v", status, cmd)
	}
	select {
	case <-ran:
	default:
		<-ran
	}
	if status, _, _ := apiCall(t, srv, key, http.MethodPost, "/api/v3/command", map[string]any{"name": "Nonsense"}); status != http.StatusBadRequest {
		t.Fatalf("want an unknown command refused, got %d", status)
	}
	status, _, profiles := apiCall(t, srv, key, http.MethodGet, "/api/v3/qualityprofile", nil)
	if status != http.StatusOK || len(profiles) != 1 || profiles[0]["name"] != "Any" {
		t.Fatalf("profiles: %d %+v", status, profiles)
	}
	if status, _, _ := apiCall(t, srv, key, http.MethodDelete, "/api/v3/movie/"+itoa(id), nil); status != http.StatusNoContent {
		t.Fatalf("delete: %d", status)
	}
	if status, _, movies := apiCall(t, srv, key, http.MethodGet, "/api/v3/movie", nil); status != http.StatusOK || len(movies) != 0 {
		t.Fatalf("want the movie gone, got %d %+v", status, movies)
	}
	if status, sys, _ := apiCall(t, srv, key, http.MethodGet, "/api/v3/system/status", nil); status != http.StatusOK || sys["appName"] != "UMMarr" {
		t.Fatalf("status: %d %+v", status, sys)
	}
	_, body := get(t, srv, "/api")
	if !strings.Contains(body, "/api/v3/movie/lookup") {
		t.Fatalf("want the API docs page, got:\n%s", body)
	}
}
