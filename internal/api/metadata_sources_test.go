package api_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// TestMetadataSources_Settings: Settings -> Metadata lists each media type's
// providers, the order can be changed, and providers can be added and removed.
func TestMetadataSources_Settings(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)

	_, body := get(t, srv, "/settings/metadata")
	for _, want := range []string{"Metadata Sources", "TVmaze", "MusicBrainz", "OMDb", "TheTVDB", "+ TheTVDB"} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q on the Metadata tab", want)
		}
	}

	providers, _ := store.ListMetadataProviders(t.Context(), db)
	byName := map[string]store.MetadataProvider{}
	for _, p := range providers {
		if p.MediaType == "series" {
			byName[p.Implementation] = p
		}
	}
	if byName["tvdb"].ID == 0 {
		t.Fatalf("want TheTVDB seeded for TV, got %+v", providers)
	}
	if byName["tvdb"].Enabled {
		t.Errorf("want TheTVDB off until it has a key")
	}

	// Move TVmaze above TMDB.
	resp, _ := postForm(t, srv, "/settings/metadata-sources/move", url.Values{"id": {itoa(byName["tvmaze"].ID)}, "direction": {"up"}})
	if resp.Header.Get("HX-Redirect") != "/settings/metadata" {
		t.Fatalf("want the move saved, got %d", resp.StatusCode)
	}
	orders, _ := store.GetMetadataSourceOrders(t.Context(), db)
	if strings.Join(orders["series"], ",") != "tvmaze,tmdb" {
		t.Fatalf("want TVmaze first for TV, got %v", orders["series"])
	}

	// Give TheTVDB a key and a PIN; it becomes part of the order.
	postForm(t, srv, "/settings/metadata-sources", url.Values{"id": {itoa(byName["tvdb"].ID)}, "media_type": {"series"}, "implementation": {"tvdb"},
		"name": {"TheTVDB"}, "enabled": {"on"}, "api_key": {"tvdb-key"}, "extra_pin": {"1234"}})
	saved, _ := store.GetMetadataProvider(t.Context(), db, byName["tvdb"].ID)
	if !saved.Enabled || saved.APIKey != "tvdb-key" || saved.Pin() != "1234" {
		t.Fatalf("want the key and PIN saved, got %+v", saved)
	}
	if orders, _ = store.GetMetadataSourceOrders(t.Context(), db); !slices.Contains(orders["series"], "tvdb") {
		t.Errorf("want TheTVDB in the TV order once enabled, got %v", orders["series"])
	}

	// A second TheTVDB can be added, then removed.
	_, body = get(t, srv, "/settings/metadata-sources/new?media_type=series&implementation=tvdb")
	if !strings.Contains(body, "Subscriber PIN") {
		t.Fatalf("want the PIN field on the add form, got:\n%s", body)
	}
	postForm(t, srv, "/settings/metadata-sources", url.Values{"media_type": {"series"}, "implementation": {"tvdb"}, "name": {"TheTVDB (spare key)"}, "enabled": {"on"}, "api_key": {"second"}})
	after, _ := store.ListMetadataProviders(t.Context(), db)
	var spare store.MetadataProvider
	for _, p := range after {
		if p.Name == "TheTVDB (spare key)" {
			spare = p
		}
	}
	if spare.ID == 0 {
		t.Fatalf("want the second TheTVDB added, got %+v", after)
	}
	if resp := deleteRequest(t, srv, "/settings/metadata-sources/"+itoa(spare.ID)); resp.StatusCode != 200 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if _, err := store.GetMetadataProvider(t.Context(), db, spare.ID); err == nil {
		t.Errorf("want the spare removed")
	}
}

// TestMetadataSources_ItemDialog: a movie's Metadata dialog says where each
// field came from and offers refresh and fix match; the page has the buttons.
func TestMetadataSources_ItemDialog(t *testing.T) {
	db := openTestDB(t)
	movieID := seedTestMovie(t, db)
	srv := newTestServerWithDB(t, db)

	_, body := get(t, srv, "/movies/"+itoa(movieID))
	if !strings.Contains(body, "/movies/"+itoa(movieID)+"/refresh-metadata") || !strings.Contains(body, "/movies/"+itoa(movieID)+"/metadata") {
		t.Fatalf("want Refresh metadata and Metadata on the movie page, got:\n%s", body)
	}
	_, body = get(t, srv, "/movies/"+itoa(movieID)+"/metadata")
	for _, want := range []string{"Metadata - Inception", "Matched to TMDB 27205", "Change match…", "Poster", "TMDB → OMDb", "Where the details came from"} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q in the dialog, got:\n%s", want, body)
		}
	}
	if _, body = postForm(t, srv, "/movies/"+itoa(movieID)+"/refresh-metadata", nil); !strings.Contains(body, "TMDB isn&#39;t configured") {
		t.Errorf("want refresh without TMDB to say so, got:\n%s", body)
	}
}

// TestChoosePoster: the picked poster is used everywhere and survives a
// refresh, as Plex's poster picker does.
func TestChoosePoster(t *testing.T) {
	db := openTestDB(t)
	movieID := seedTestMovie(t, db)
	srv := newTestServerWithDB(t, db)
	id := itoa(movieID)

	resp, body := postForm(t, srv, "/metadata/movie/"+id+"/poster", url.Values{"url": {"https://example.test/chosen.jpg"}})
	if resp.Header.Get("HX-Refresh") != "true" {
		t.Fatalf("want the page refreshed, got %d:\n%s", resp.StatusCode, body)
	}
	detail, _, _ := store.GetMovieDetail(t.Context(), db, movieID)
	if detail.PosterURL != "https://example.test/chosen.jpg" {
		t.Fatalf("want the chosen poster on the movie, got %q", detail.PosterURL)
	}
	movies, _ := store.ListMovies(t.Context(), db)
	if len(movies) != 1 || movies[0].PosterURL != "https://example.test/chosen.jpg" {
		t.Errorf("want it on the library list too, got %+v", movies)
	}
	_, body = get(t, srv, "/movies/"+id+"/metadata")
	if !strings.Contains(body, "chosen by hand") || !strings.Contains(body, "poster-choice chosen") {
		t.Errorf("want the dialog showing it as chosen, got:\n%s", body)
	}

	// Clearing it goes back to the providers' own poster.
	postForm(t, srv, "/metadata/movie/"+id+"/poster", url.Values{"url": {""}})
	if detail, _, _ = store.GetMovieDetail(t.Context(), db, movieID); detail.PosterURL == "https://example.test/chosen.jpg" {
		t.Errorf("want the override cleared")
	}
	if resp = postFormResponse(t, srv, "/metadata/nonsense/1/poster", url.Values{"url": {"x"}}); resp.StatusCode != 400 {
		t.Errorf("want an unknown item kind refused, got %d", resp.StatusCode)
	}
}

func postFormResponse(t *testing.T, srv *httptest.Server, path string, form url.Values) *http.Response {
	t.Helper()
	resp, _ := postForm(t, srv, path, form)
	return resp
}

// TestEpisodeNames_Buttons: the series page offers a name search for the
// whole series and per season.
func TestEpisodeNames_Buttons(t *testing.T) {
	db := openTestDB(t)
	seriesID := seedTestSeries(t, db)
	srv := newTestServerWithDB(t, db)
	id := itoa(seriesID)

	_, body := get(t, srv, "/tv/"+id)
	if !strings.Contains(body, "/tv/"+id+"/episode-names") || !strings.Contains(body, "Find Episode Names") {
		t.Fatalf("want a series-wide name search, got:\n%s", body)
	}
	if !strings.Contains(body, "/tv/"+id+"/seasons/1/episode-names") {
		t.Fatalf("want a per-season name search, got:\n%s", body)
	}
	// Without TMDB configured it says so rather than failing silently.
	_, body = postForm(t, srv, "/tv/"+id+"/seasons/1/episode-names", nil)
	if !strings.Contains(body, "TMDB isn&#39;t configured") {
		t.Errorf("want the missing provider reported, got:\n%s", body)
	}
}
