package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// TestScanLibrary_ImportsNewMovieFolders: Scan folder on a movie library
// also brings in folders UMMarr doesn't track yet, in the background, and
// the scan result polls its progress until it reports what was added.
func TestScanLibrary_ImportsNewMovieFolders(t *testing.T) {
	db := openTestDB(t)
	fakeTMDB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/movie":
			json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"id": 27205, "title": "Inception", "release_date": "2010-07-16"}}})
		case "/movie/27205":
			json.NewEncoder(w).Encode(map[string]any{"id": 27205, "title": "Inception", "release_date": "2010-07-16", "external_ids": map[string]any{"imdb_id": "tt1375666"}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fakeTMDB.Close)
	movies := &sync.MovieService{DB: db, TMDB: tmdb.New(tmdb.Options{Token: "test", BaseURL: fakeTMDB.URL})}
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Import: &sync.ImportService{DB: db, Movies: movies}}))
	t.Cleanup(srv.Close)

	root := t.TempDir()
	if _, err := store.CreateRootFolder(t.Context(), db, root, "movie"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateQualityProfile(t.Context(), db, "Any"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Inception (2010)"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Inception (2010)", "Inception (2010).mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, body := postForm(t, srv, "/settings/scan-library", url.Values{"media_type": {"movie"}})
	if !strings.Contains(body, `id="library-import-status"`) || !strings.Contains(body, root) {
		t.Fatalf("want the scan result to show the folder's import progress, got:\n%s", body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, body = get(t, srv, "/settings/scan-library/status")
		if strings.Contains(body, "1 new folder(s): 1 movie(s) added") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("want the import to finish with 1 added, last status:\n%s", body)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if strings.Contains(body, `hx-trigger="every 3s"`) {
		t.Fatalf("want polling to stop once finished, got:\n%s", body)
	}
	list, _ := store.ListMovies(t.Context(), db)
	if len(list) != 1 || list[0].Title != "Inception" || !list[0].HasFile {
		t.Fatalf("want Inception added with its file, got %+v", list)
	}
}
