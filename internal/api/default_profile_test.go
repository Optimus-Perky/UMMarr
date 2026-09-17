package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// TestDefaultQualityProfile_PreselectedOnAdd: Settings marks one profile as
// the default and the Add form's profile picker starts on it.
func TestDefaultQualityProfile_PreselectedOnAdd(t *testing.T) {
	db := openTestDB(t)
	fakeTMDB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"id": 27205, "title": "Inception", "release_date": "2010-07-16"}}})
	}))
	t.Cleanup(fakeTMDB.Close)
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, TMDB: tmdb.New(tmdb.Options{Token: "test", BaseURL: fakeTMDB.URL})}))
	t.Cleanup(srv.Close)

	first, _ := store.CreateQualityProfile(t.Context(), db, "HD-1080p")
	second, _ := store.CreateQualityProfile(t.Context(), db, "Any")

	_, body := get(t, srv, "/settings/profiles")
	if !strings.Contains(body, `/settings/quality-profiles/`+itoa(second)+`/default`) || strings.Contains(body, `/settings/quality-profiles/`+itoa(first)+`/default`) {
		t.Fatalf("want a Make default button on the second profile only, got:\n%s", body)
	}

	resp, _ := postForm(t, srv, "/settings/quality-profiles/"+itoa(second)+"/default", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("make default: %d", resp.StatusCode)
	}
	_, body = get(t, srv, "/movies/search?q=inception")
	if !strings.Contains(body, `<option value="`+itoa(second)+`" selected>Any</option>`) || strings.Contains(body, `value="`+itoa(first)+`" selected`) {
		t.Fatalf("want the Add form to preselect the new default profile, got:\n%s", body)
	}
}
