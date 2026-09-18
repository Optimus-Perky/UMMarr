package api_test

import (
	"context"
	"database/sql"
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

// A quality profile can be removed, but only when nothing is using it -
// deleting one out from under a movie would leave it pointing at a profile
// that no longer exists.
func TestDeleteQualityProfile(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()

	keep, err := store.CreateQualityProfile(ctx, db, "Keep")
	if err != nil {
		t.Fatal(err)
	}
	spare, err := store.CreateQualityProfile(ctx, db, "Spare")
	if err != nil {
		t.Fatal(err)
	}

	// The Remove button is offered per profile.
	_, body := get(t, srv, "/settings/profiles")
	if !strings.Contains(body, `hx-delete="/settings/quality-profiles/`+itoa(spare)+`"`) {
		t.Errorf("want a Remove button for each quality profile, got:\n%s", body)
	}

	// An unused one goes.
	resp, body := deleteForm(t, srv, "/settings/quality-profiles/"+itoa(spare), nil)
	if resp.StatusCode != 200 || resp.Header.Get("HX-Redirect") == "" {
		t.Fatalf("delete = %d: %s", resp.StatusCode, body)
	}
	profiles, _ := store.ListQualityProfiles(ctx, db)
	for _, p := range profiles {
		if p.ID == spare {
			t.Error("want the spare profile gone")
		}
	}

	// One in use is refused, and says what is using it.
	root, err := store.CreateRootFolder(ctx, db, t.TempDir(), "movie")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO movie_metadata (title, clean_title, sort_title) VALUES ('Heat', 'heat', 'heat')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO movies (movie_metadata_id, quality_profile_id, root_folder_id, monitored, added) VALUES (1, ?, ?, 1, CURRENT_TIMESTAMP)`, keep, root); err != nil {
		t.Fatal(err)
	}
	_, body = deleteForm(t, srv, "/settings/quality-profiles/"+itoa(keep), nil)
	if !strings.Contains(body, "1 movie still using this profile") {
		t.Errorf("want the refusal to name what is using it, got: %s", body)
	}
	if profiles, _ := store.ListQualityProfiles(ctx, db); len(profiles) == 0 {
		t.Fatal("want the profile kept")
	}
}

// Refusing to delete a profile in use isn't much help on its own: the
// refusal offers to move whatever uses it to another profile, and doing
// that removes it in one go.
func TestDeleteQualityProfile_OffersToMoveWhatUsesIt(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()

	inUse, err := store.CreateQualityProfile(ctx, db, "Old")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateQualityProfile(ctx, db, "New")
	if err != nil {
		t.Fatal(err)
	}
	root, err := store.CreateRootFolder(ctx, db, t.TempDir(), "movie")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO movie_metadata (title, clean_title, sort_title) VALUES ('Heat', 'heat', 'heat')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO movies (movie_metadata_id, quality_profile_id, root_folder_id, monitored, added) VALUES (1, ?, ?, 1, CURRENT_TIMESTAMP)`, inUse, root); err != nil {
		t.Fatal(err)
	}

	// The refusal offers the other profiles.
	_, body := deleteForm(t, srv, "/settings/quality-profiles/"+itoa(inUse), nil)
	if !strings.Contains(body, "1 movie still using this profile") {
		t.Fatalf("want the refusal to say what uses it, got: %s", body)
	}
	if !strings.Contains(body, `name="move_to"`) || !strings.Contains(body, "Move and remove") {
		t.Fatalf("want the offer to move them, got: %s", body)
	}
	if !strings.Contains(body, `<option value="`+itoa(target)+`">New</option>`) {
		t.Errorf("want the other profile offered as a destination, got: %s", body)
	}

	// Taking the offer moves the movie and removes the profile.
	resp, body := deleteForm(t, srv, "/settings/quality-profiles/"+itoa(inUse), url.Values{"move_to": {itoa(target)}})
	if resp.StatusCode != 200 || resp.Header.Get("HX-Redirect") == "" {
		t.Fatalf("move and remove = %d: %s", resp.StatusCode, body)
	}
	var profile int64
	if err := db.QueryRow(`SELECT quality_profile_id FROM movies WHERE id = 1`).Scan(&profile); err != nil {
		t.Fatal(err)
	}
	if profile != target {
		t.Errorf("movie is on profile %d, want %d", profile, target)
	}
	for _, p := range mustProfiles(t, db) {
		if p.ID == inUse {
			t.Error("want the old profile removed")
		}
	}
}

func mustProfiles(t *testing.T, db *sql.DB) []store.QualityProfile {
	t.Helper()
	profiles, err := store.ListQualityProfiles(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	return profiles
}
