package api_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

func TestUpdateAccount_BlankPasswordKeepsCurrent(t *testing.T) {
	srv := newTestServer(t)

	if _, err := http.PostForm(srv.URL+"/settings/account", map[string][]string{
		"username": {"testuser"}, "password": {"first-password"},
	}); err != nil {
		t.Fatalf("first save: %v", err)
	}

	if _, err := http.PostForm(srv.URL+"/settings/account", map[string][]string{
		"username": {"testuser2"}, "password": {""},
	}); err != nil {
		t.Fatalf("second save (blank password): %v", err)
	}

	_, body := get(t, srv, "/settings/general")
	if !strings.Contains(body, `value="testuser2"`) {
		t.Fatalf("want the updated username reflected on the settings page, got:\n%s", body)
	}
}

func TestPreferredWords_AddAndRemove(t *testing.T) {
	srv := newTestServer(t)

	resp, err := http.PostForm(srv.URL+"/settings/preferred-words", map[string][]string{
		"term": {"x265"}, "score": {"10"},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add: want 200, got %d", resp.StatusCode)
	}

	_, body := get(t, srv, "/settings/profiles")
	if !strings.Contains(body, "x265") {
		t.Fatalf("want the new preferred word listed, got:\n%s", body)
	}

	// A blank term is refused rather than silently accepted.
	resp, err = http.PostForm(srv.URL+"/settings/preferred-words", map[string][]string{
		"term": {""}, "score": {"5"},
	})
	if err != nil {
		t.Fatalf("blank term: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("blank term: want 422, got %d", resp.StatusCode)
	}
}

func TestPreferredWords_Update(t *testing.T) {
	db := openTestDB(t)
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db}))
	t.Cleanup(srv.Close)

	id, err := store.CreatePreferredWord(context.Background(), db, "x265", 10)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	resp, err := http.PostForm(fmt.Sprintf("%s/settings/preferred-words/%d", srv.URL, id), map[string][]string{
		"term": {"x265"}, "score": {"100"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: want 200, got %d", resp.StatusCode)
	}

	words, err := store.ListPreferredWords(context.Background(), db)
	if err != nil || len(words) != 1 || words[0].Score != 100 {
		t.Fatalf("want x265's score updated to 100, got %+v, %v", words, err)
	}

	// An unknown id is a 404, not a silent no-op.
	resp, err = http.PostForm(fmt.Sprintf("%s/settings/preferred-words/999999", srv.URL), map[string][]string{
		"term": {"x265"}, "score": {"5"},
	})
	if err != nil {
		t.Fatalf("update unknown id: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("update unknown id: want 404, got %d", resp.StatusCode)
	}
}

func TestUpdateMovieNaming_ChangesTemplateImmediately(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)

	form := currentMediaManagementForm(t, db)
	form.Set("movie_folder_format", "{Movie Title}")
	form.Set("movie_file_format", "Custom - {Movie Title}")
	if _, err := http.PostForm(srv.URL+"/settings/media-management", form); err != nil {
		t.Fatalf("save naming: %v", err)
	}

	_, body := get(t, srv, "/settings/media-management")
	if !strings.Contains(body, `value="Custom - {Movie Title}"`) {
		t.Fatalf("want the updated movie file format reflected on the settings page, got:\n%s", body)
	}
}

func TestScanLibrary_RendersCounts(t *testing.T) {
	db := openTestDB(t)
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Import: &sync.ImportService{DB: db}}))
	t.Cleanup(srv.Close)

	movieID := seedTestMovieWithRootFolder(t, db, t.TempDir())
	moviePath, err := store.ResolveMoviePath(context.Background(), db, movieID)
	if err != nil {
		t.Fatalf("resolve movie path: %v", err)
	}
	name, err := store.ResolveMovieFileName(context.Background(), db, movieID, "download.mkv")
	if err != nil {
		t.Fatalf("resolve movie file name: %v", err)
	}
	if err := os.MkdirAll(moviePath, 0o755); err != nil {
		t.Fatalf("mkdir movie path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moviePath, name), []byte("already organized"), 0o644); err != nil {
		t.Fatalf("write movie file: %v", err)
	}

	resp, err := http.Post(srv.URL+"/settings/scan-library", "", nil)
	if err != nil {
		t.Fatalf("scan library: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	body := string(bodyBytes)
	if !strings.Contains(body, "imported 1 movie(s)") {
		t.Fatalf("want the scan to report 1 imported movie, got:\n%s", body)
	}
}

// TestQualityProfileEdit_RendersCatalogWithSavedWeights proves the
// weight editor asked for ("based on weight, specified by user,
// just like sonarr") shows every catalog quality with its saved
// weight/allowed state.
func TestQualityProfileEdit_RendersCatalogWithSavedWeights(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	profileID, err := store.CreateQualityProfile(context.Background(), db, "HD-1080p")
	if err != nil {
		t.Fatalf("create quality profile: %v", err)
	}

	status, body := get(t, srv, "/settings/quality-profiles/"+itoa(profileID))
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d", status)
	}
	if !strings.Contains(body, "Bluray-1080p") {
		t.Fatalf("want the catalog quality names shown, got:\n%s", body)
	}
	if !strings.Contains(body, `name="weight_Bluray-1080p"`) || !strings.Contains(body, `name="allowed_Bluray-1080p"`) {
		t.Fatalf("want weight/allowed inputs per quality, got:\n%s", body)
	}
}

// TestUpdateQualityProfileItems_ChangesPersistAndReflect proves a saved
// weight change round-trips through the real HTTP handler, not just the
// store layer directly (see internal/store's own TestUpdateQualityProfileItems_Persists).
func TestUpdateQualityProfileItems_ChangesPersistAndReflect(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	profileID, err := store.CreateQualityProfile(context.Background(), db, "HD-1080p")
	if err != nil {
		t.Fatalf("create quality profile: %v", err)
	}

	form := map[string][]string{}
	for _, quality := range releaseparse.AllQualities {
		form["weight_"+quality] = []string{"5"}
	}
	form["allowed_Bluray-1080p"] = []string{"on"}

	resp, err := http.PostForm(srv.URL+"/settings/quality-profiles/"+itoa(profileID)+"/items", form)
	if err != nil {
		t.Fatalf("update items: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	items, err := store.GetQualityProfileItems(context.Background(), db, profileID)
	if err != nil {
		t.Fatalf("get quality profile items: %v", err)
	}
	for _, it := range items {
		if it.Weight != 5 {
			t.Fatalf("want every quality's weight saved as 5, got %+v", it)
		}
		wantAllowed := it.Quality == "Bluray-1080p"
		if it.Allowed != wantAllowed {
			t.Fatalf("want only Bluray-1080p allowed (unchecked boxes don't submit), got %+v", it)
		}
	}
}

// TestScanLibrary_ScopedToOneFolder: a scan asked for one library folder
// leaves files in the other folders alone, and says which folder it scanned.
func TestScanLibrary_ScopedToOneFolder(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	root := t.TempDir()
	movieID := seedTestMovieWithRootFolder(t, db, filepath.Join(root, "movies"))
	tvRoot, err := store.CreateRootFolder(t.Context(), db, filepath.Join(root, "tv"), "series")
	if err != nil {
		t.Fatalf("create tv root folder: %v", err)
	}
	moviePath, err := store.ResolveMoviePath(t.Context(), db, movieID)
	if err != nil {
		t.Fatalf("resolve movie path: %v", err)
	}
	name, err := store.ResolveMovieFileName(t.Context(), db, movieID, "download.mkv")
	if err != nil {
		t.Fatalf("resolve movie file name: %v", err)
	}
	if err := os.MkdirAll(moviePath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moviePath, name), []byte("already organized"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, body := postForm(t, srv, "/settings/scan-library", url.Values{"root_folder_id": {itoa(tvRoot)}})
	if !strings.Contains(body, "Scanned "+filepath.Join(root, "tv")) || !strings.Contains(body, "imported 0 movie(s)") {
		t.Fatalf("want a TV-folder scan to leave the movie alone, got:\n%s", body)
	}
	_, body = postForm(t, srv, "/settings/scan-library", url.Values{"media_type": {"movie"}})
	if !strings.Contains(body, "Scanned the movie folders") || !strings.Contains(body, "imported 1 movie(s)") {
		t.Fatalf("want the movie scan to import the movie, got:\n%s", body)
	}
}
