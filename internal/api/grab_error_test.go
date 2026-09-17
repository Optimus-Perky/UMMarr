package api_test

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestGrab_ExpiredReleaseShowsARow: a Grab whose search result is no longer
// cached (e.g. UMMarr restarted between the search and the click) answers
// 422 with a results-table row saying so, which the page swaps in place of
// the release's row, instead of a bare paragraph htmx never showed.
func TestGrab_ExpiredReleaseShowsARow(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seriesID := seedTestSeries(t, db)
	var episodeID int64
	db.QueryRow(`SELECT id FROM episodes WHERE series_id = ?`, seriesID).Scan(&episodeID)
	movieDB := openTestDB(t)
	movieSrv := newTestServerWithDB(t, movieDB)
	movieID := seedTestMovie(t, movieDB)

	for _, c := range []struct {
		srv  *httptest.Server
		path string
	}{{srv, "/tv/" + itoa(seriesID) + "/episodes/" + itoa(episodeID) + "/grab"}, {movieSrv, "/movies/" + itoa(movieID) + "/grab"}, {srv, "/tv/" + itoa(seriesID) + "/grab"}} {
		path := c.path
		resp, body := postForm(t, c.srv, path, url.Values{"indexer_id": {"2"}, "guid": {"not-a-real-release"}})
		if resp.StatusCode != 422 || !strings.HasPrefix(body, `<tr><td colspan="15"><p class="notice grab-error">Couldn't grab: this search result has expired - search again`) {
			t.Errorf("%s: want a 422 table row explaining the expired result, got %d:\n%s", path, resp.StatusCode, body)
		}
	}
	var grabs int
	db.QueryRow(`SELECT COUNT(*) FROM grabs`).Scan(&grabs)
	if grabs != 0 {
		t.Fatalf("an expired result must not grab anything, got %d grabs", grabs)
	}
	_, page := get(t, srv, "/movies")
	if !strings.Contains(page, `<script src="/static/htmx-errors.js"></script>`) {
		t.Fatal("want every page to load the script that shows 422 messages")
	}
	_, js := get(t, srv, "/static/htmx-errors.js")
	if !strings.Contains(js, "htmx:beforeSwap") || !strings.Contains(js, "status !== 422") {
		t.Fatalf("want the 422 swap handler served, got:\n%s", js)
	}
}
