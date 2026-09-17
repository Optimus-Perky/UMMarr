package api_test

import (
	"strings"
	"testing"
)

// TestPosterOptions: the Movies and TV pages carry Radarr's and Sonarr's
// Poster Options - every optional line is on the card for the browser to
// show or hide, and the dialog offers only what that page has.
func TestPosterOptions(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seedTestMovie(t, db)
	if _, err := db.Exec(`UPDATE movie_metadata SET physical_release = '2010-12-07'`); err != nil {
		t.Fatalf("set release date: %v", err)
	}
	dbTV := openTestDB(t)
	srvTV := newTestServerWithDB(t, dbTV)
	seedTestSeries(t, dbTV)

	_, body := get(t, srv, "/movies")
	for _, want := range []string{`id="poster-options"`, `data-prefs="ummarr-poster-movies"`, `data-opt="profile">Any</div>`, `data-opt="cinema"`, `data-opt="tmdb"`, `name="physical"`, `class="card-progress missing"`, `>Missing</span>`} {
		if !strings.Contains(body, want) {
			t.Fatalf("movies page: want %q, got:\n%s", want, body)
		}
	}
	_, body = get(t, srvTV, "/tv")
	for _, want := range []string{`data-prefs="ummarr-poster-tv"`, `data-opt="profile">Any</div>`, `data-opt="status"`, `name="detailed"`, `class="card-progress`} {
		if !strings.Contains(body, want) {
			t.Fatalf("tv page: want %q, got:\n%s", want, body)
		}
	}
	if strings.Contains(body, `name="cinema"`) {
		t.Fatalf("tv page: want no movie-only options")
	}
}
