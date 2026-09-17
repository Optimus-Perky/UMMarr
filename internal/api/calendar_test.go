package api_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestCalendar_MonthGridAndToggles(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()
	seriesID := seedTestSeries(t, db)
	var seasonID int64
	db.QueryRow(`SELECT season_id FROM episodes WHERE series_id = ?`, seriesID).Scan(&seasonID)
	airs := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	store.UpsertEpisode(ctx, db, seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: 9, Title: metadata.Field[string]{Value: "Airs In September", Provider: "tmdb"}, AirDate: metadata.Field[*time.Time]{Value: &airs, Provider: "tmdb"}})
	digital := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	movieMeta, _ := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
		Title: metadata.Field[string]{Value: "Some Film", Provider: "tmdb"}, Year: metadata.Field[int]{Value: 2026, Provider: "tmdb"},
		DigitalRelease: metadata.Field[*time.Time]{Value: &digital, Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "7"},
	})
	var profile, root int64
	db.QueryRow(`SELECT quality_profile_id, root_folder_id FROM series WHERE id = ?`, seriesID).Scan(&profile, &root)
	movieRoot, _ := store.CreateRootFolder(ctx, db, "/media/movies", "movie")
	store.UpsertMovie(ctx, db, movieMeta, profile, movieRoot, true)

	status, body := get(t, srv, "/calendar?month=2026-09")
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d", status)
	}
	for _, want := range []string{
		"<h2>Calendar</h2>", "September 2026", `href="/calendar?month=2026-08"`, `href="/calendar?month=2026-10"`, ">Today</a>",
		`data-kind="movie" checked> Movies (1)`, `data-kind="series" checked> TV (1)`, `data-kind="music" checked> Music (0)`, `data-kind="unmonitored"`,
		`data-kind="cinema" checked> In Cinemas (0)`, `data-kind="digital" checked> Digital (1)`, `data-kind="physical" checked> Physical (0)`, `data-event="digital"`,
		"<span>Mon</span>", `class="calendar-week"`, "/static/calendar.js",
		`data-kind="series"`, "Airs In September", "1x09", `href="/movies/some-film-2026"`, ">Digital</span>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q on the calendar", want)
		}
	}
	// The episode airs on the 17th, so it sits in the same day cell as that date.
	cell := body[strings.Index(body, `>17</div>`):]
	if end := strings.Index(cell, "calendar-day"); end > 0 {
		cell = cell[:end]
	}
	if !strings.Contains(cell, "Airs In September") {
		t.Errorf("want the episode on the 17th, got:\n%s", cell)
	}
	// A month with nothing in it still renders its grid.
	_, empty := get(t, srv, "/calendar?month=2027-02")
	if !strings.Contains(empty, "February 2027") || !strings.Contains(empty, `class="calendar-day`) || strings.Contains(empty, "Airs In September") {
		t.Error("want an empty month to render its own grid")
	}
	if status, _ := get(t, srv, "/calendar?month=nonsense"); status != http.StatusBadRequest {
		t.Errorf("want a bad month refused, got %d", status)
	}

	// A busy day shows the first five and hides the rest behind "+N more".
	for n := 20; n <= 27; n++ {
		store.UpsertEpisode(ctx, db, seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: n, Title: metadata.Field[string]{Value: "Marathon", Provider: "tmdb"}, AirDate: metadata.Field[*time.Time]{Value: &airs, Provider: "tmdb"}})
	}
	_, busy := get(t, srv, "/calendar?month=2026-09")
	day := busy[strings.Index(busy, `>17</div>`):]
	if end := strings.Index(day, "calendar-day"); end > 0 {
		day = day[:end]
	}
	if !strings.Contains(day, `<details class="calendar-more"><summary>+4 more</summary>`) {
		t.Errorf("want the ninth episode of the day behind an expander, got:\n%s", day)
	}
	if shown := strings.Count(day[:strings.Index(day, "calendar-more")], "calendar-entry"); shown != 5 {
		t.Errorf("want five entries before the expander, got %d", shown)
	}
	if _, nav := get(t, srv, "/movies"); !strings.Contains(nav, `href="/calendar"`) {
		t.Error("want a Calendar link in the sidebar")
	}
}
