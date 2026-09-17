package api_test

import (
	"strings"
	"testing"
)

// TestMoviesPage_SortAndFilterToolbar: the Movies page carries Radarr's
// sort keys and filters, and every card the keys the toolbar sorts and
// filters by.
func TestMoviesPage_SortAndFilterToolbar(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seedTestMovie(t, db)

	_, body := get(t, srv, "/movies")
	for _, want := range []string{
		`library-sort" name="sort"`, `<option value="tmdb">TMDb Rating</option>`, `<option value="physical">Physical Release</option>`,
		`library-filter" name="filter"`, `<option value="wanted">Wanted</option>`, `<option value="custom">Custom filter…</option>`,
		`data-sorttitle="inception"`, `data-letter="I"`, `data-year="2010"`, `data-monitored="1"`, `data-hasfile="0"`, `data-profile="Any"`,
		`class="letter-bar" data-library="movies"`, `/static/library-grid.js`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("want %q on the Movies page, got:\n%s", want, body)
		}
	}
}

// TestSortKeys_IgnoresLeadingArticles: "The Matrix" sorts under M, as in
// Radarr, on every library page.
func TestSortKeys_IgnoresLeadingArticles(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seedTestMovie(t, db)
	if _, err := db.Exec(`UPDATE movie_metadata SET title = 'The Inception'`); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, srv, "/movies")
	if !strings.Contains(body, `data-sorttitle="inception"`) || !strings.Contains(body, `data-letter="I"`) {
		t.Fatalf("want The Inception to sort as inception under I, got:\n%s", body)
	}
}
