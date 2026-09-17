package api_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestMoviesPage_ViewsAndEditor(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	movieID := seedTestMovie(t, db)
	db.Exec(`UPDATE movie_metadata SET overview = 'Dreams within dreams.'`)
	_, body := get(t, srv, "/movies")
	for _, want := range []string{
		`class="release-filter library-view"`, `<option value="overview">Overview</option>`, `<option value="table">Table</option>`,
		`class="btn btn-sm btn-ghost library-select-toggle"`, `onclick="ummarrLibraryOptions('movies')"`, `/static/library-views.js`,
		`class="table-head"`, `data-col="title" data-sort="sorttitle">Title</span>`, `class="card-check" value="` + itoa(movieID) + `"`,
		`data-col="profile">Any</span>`, `<p class="overview-text">Dreams within dreams.</p>`, `data-col="availability">Released</span>`,
		`id="overview-options"`, `data-prefix="ov-"`, `id="table-options"`, `data-prefix="col-"`,
		`class="editor-bar" data-library="movies" hidden`, `id="movies-editor-edit"`, `name="minimum_availability"`, `<option value="inCinemas">In Cinemas</option>`,
		`name="root_folder_id"`, `name="tag_mode"`, `id="movies-editor-delete"`, `name="add_exclusion"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q on the Movies page", want)
		}
	}
}

func TestTVPage_ViewsEditorAndSeasonPassLink(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seedTestSeries(t, db)
	db.Exec(`UPDATE series_metadata SET network = 'AMC', overview = 'A chemistry teacher turns.'`)
	_, body := get(t, srv, "/tv")
	for _, want := range []string{
		`href="/tv/seasonpass">Season Pass</a>`, `class="release-filter library-view"`, `data-col="network">AMC</span>`, `data-col="seasons">1</span>`,
		`<p class="overview-text">A chemistry teacher turns.</p>`, `id="tv-editor-edit"`, `name="series_type"`, `name="season_folder"`,
		`id="tv-editor-monitor"`, `<option value="pilot" title="Monitor only the first episode of the first season">Pilot Episode</option>`, `id="tv-editor-delete"`,
		`data-prefs="ummarr-table-tv"`, `name="missing"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q on the TV page", want)
		}
	}
}

func TestMoviesEditor_EditMoveDeleteSearch(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()
	rootA, rootB := t.TempDir(), t.TempDir()
	m1 := seedTestMovieWithRootFolder(t, db, rootA)
	var profile, rootAID int64
	db.QueryRow(`SELECT quality_profile_id, root_folder_id FROM movies WHERE id = ?`, m1).Scan(&profile, &rootAID)
	meta, _ := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{Title: metadata.Field[string]{Value: "Heat", Provider: "tmdb"}, Year: metadata.Field[int]{Value: 1995, Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "949"}})
	m2, _ := store.UpsertMovie(ctx, db, meta, profile, rootAID, true)
	rootBID, err := store.CreateRootFolder(ctx, db, rootB, "movie")
	if err != nil {
		t.Fatal(err)
	}
	hd, _ := store.CreateQualityProfile(ctx, db, "HD")
	d1, _, _ := store.GetMovieDetail(ctx, db, m1)
	os.MkdirAll(d1.Path.String, 0o755)
	os.WriteFile(filepath.Join(d1.Path.String, "movie.mkv"), []byte("x"), 0o644)

	_, body := postForm(t, srv, "/movies/editor", url.Values{"monitored": {"false"}})
	if !strings.Contains(body, "Select at least one movie.") {
		t.Fatalf("want nothing done without a selection, got:\n%s", body)
	}
	_, body = postForm(t, srv, "/movies/editor", url.Values{"id": {itoa(m1)}, "minimum_availability": {"preDB"}})
	if !strings.Contains(body, "unknown minimum availability") {
		t.Fatalf("want a bad availability refused, got:\n%s", body)
	}
	resp, body := postForm(t, srv, "/movies/editor", url.Values{
		"id": {itoa(m1), itoa(m2)}, "monitored": {"false"}, "quality_profile_id": {itoa(hd)}, "minimum_availability": {"inCinemas"},
		"tags": {"4K, kids"}, "tag_mode": {"add"}, "root_folder_id": {itoa(rootBID)}, "move_files": {"on"},
	})
	if resp.Header.Get("HX-Redirect") != "/movies?edited=2" {
		t.Fatalf("want a redirect reporting 2 edited, got %q:\n%s", resp.Header.Get("HX-Redirect"), body)
	}
	for _, id := range []int64{m1, m2} {
		var monitored bool
		var gotProfile, gotRoot int64
		var availability, tags string
		db.QueryRow(`SELECT monitored, quality_profile_id, root_folder_id, minimum_availability, tags FROM movies WHERE id = ?`, id).Scan(&monitored, &gotProfile, &gotRoot, &availability, &tags)
		labels, _ := store.TagLabels(ctx, db, tags)
		if monitored || gotProfile != hd || gotRoot != rootBID || availability != "inCinemas" || strings.Join(labels, ",") != "4k,kids" {
			t.Errorf("movie %d: monitored=%v profile=%d root=%d availability=%s tags=%v", id, monitored, gotProfile, gotRoot, availability, labels)
		}
	}
	if _, err := os.Stat(filepath.Join(rootB, filepath.Base(d1.Path.String), "movie.mkv")); err != nil {
		t.Fatalf("want the movie folder moved into the new library folder: %v", err)
	}
	if _, raw := get(t, srv, "/movies?edited=2"); !strings.Contains(raw, "2 movies updated.") {
		t.Error("want the page to report the edit")
	}

	_, body = postForm(t, srv, "/movies/editor/search", url.Values{"id": {itoa(m1)}})
	if !strings.Contains(body, "Automatic search isn") {
		t.Errorf("want search explained as unavailable without an indexer, got:\n%s", body)
	}

	resp, body = postForm(t, srv, "/movies/editor/delete", url.Values{"id": {itoa(m2)}, "add_exclusion": {"on"}})
	if resp.Header.Get("HX-Redirect") != "/movies?deleted=1&kept=0" {
		t.Fatalf("want a redirect reporting 1 removed, got %q:\n%s", resp.Header.Get("HX-Redirect"), body)
	}
	var left, excluded int
	db.QueryRow(`SELECT COUNT(*) FROM movies`).Scan(&left)
	db.QueryRow(`SELECT COUNT(*) FROM import_list_exclusions WHERE media_type = 'movie' AND tmdb_id = 949`).Scan(&excluded)
	if left != 1 || excluded != 1 {
		t.Fatalf("want Heat removed and excluded, got %d movies left, %d exclusions", left, excluded)
	}
	if _, raw := get(t, srv, "/movies?deleted=1&kept=0"); !strings.Contains(raw, "1 movie removed.") {
		t.Error("want the page to report the removal")
	}
}

func TestSeriesEditor_MonitorAndEdit(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seriesID := seedTestSeries(t, db)
	resp, body := postForm(t, srv, "/tv/editor/monitor", url.Values{"id": {itoa(seriesID)}, "monitor": {"none"}})
	if resp.Header.Get("HX-Redirect") != "/tv?edited=1" {
		t.Fatalf("want a redirect, got %q:\n%s", resp.Header.Get("HX-Redirect"), body)
	}
	var monitored int
	db.QueryRow(`SELECT COUNT(*) FROM episodes WHERE series_id = ? AND monitored`, seriesID).Scan(&monitored)
	if monitored != 0 {
		t.Fatalf("want every episode unmonitored, %d still are", monitored)
	}
	resp, _ = postForm(t, srv, "/tv/editor", url.Values{"id": {itoa(seriesID)}, "series_type": {"daily"}, "season_folder": {"false"}})
	if resp.Header.Get("HX-Redirect") != "/tv?edited=1" {
		t.Fatalf("want a redirect, got %q", resp.Header.Get("HX-Redirect"))
	}
	s, _ := store.GetSeriesSettings(t.Context(), db, seriesID)
	if s.SeriesType != "daily" || s.SeasonFolder {
		t.Fatalf("want the type and season folder saved, got %+v", s)
	}
	if _, raw := get(t, srv, "/tv?edited=1"); !strings.Contains(raw, "1 series updated.") {
		t.Error("want the TV page to report the edit")
	}
}

func TestSeasonPass(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seriesID := seedTestSeries(t, db)
	status, body := get(t, srv, "/tv/seasonpass")
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d", status)
	}
	for _, want := range []string{"<h2>Season Pass</h2>", "Breaking Bad", `class="season-chip on"`, `S1 <span class="season-chip-count">0/1</span>`, `name="monitor"`, `name="id" value="` + itoa(seriesID) + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q on Season Pass, got:\n%s", want, body)
		}
	}
	_, body = postForm(t, srv, "/tv/seasonpass/toggle?series="+itoa(seriesID)+"&season=1", url.Values{})
	if !strings.Contains(body, `class="season-chip"`) || strings.Contains(body, "season-chip on") {
		t.Fatalf("want the chip turned off, got:\n%s", body)
	}
	var seasonOn bool
	db.QueryRow(`SELECT monitored FROM seasons WHERE series_id = ? AND season_number = 1`, seriesID).Scan(&seasonOn)
	if seasonOn {
		t.Fatal("want season 1 unmonitored")
	}
	if status, _ := func() (int, string) {
		resp, b := postForm(t, srv, "/tv/seasonpass/toggle?series="+itoa(seriesID)+"&season=9", url.Values{})
		return resp.StatusCode, b
	}(); status != http.StatusNotFound {
		t.Errorf("want a season that doesn't exist to 404, got %d", status)
	}

	_, body = postForm(t, srv, "/tv/seasonpass", url.Values{"id": {itoa(seriesID)}})
	if !strings.Contains(body, "Choose what to change") {
		t.Fatalf("want a save with nothing chosen refused, got:\n%s", body)
	}
	resp, body := postForm(t, srv, "/tv/seasonpass", url.Values{"id": {itoa(seriesID)}, "monitor": {"all"}, "monitored": {"false"}})
	if resp.Header.Get("HX-Redirect") != "/tv/seasonpass?saved=1" {
		t.Fatalf("want a redirect, got %q:\n%s", resp.Header.Get("HX-Redirect"), body)
	}
	var seriesOn bool
	db.QueryRow(`SELECT monitored FROM series WHERE id = ?`, seriesID).Scan(&seriesOn)
	db.QueryRow(`SELECT monitored FROM seasons WHERE series_id = ? AND season_number = 1`, seriesID).Scan(&seasonOn)
	if seriesOn || !seasonOn {
		t.Fatalf("want the series unmonitored and All Episodes turning season 1 back on, got series=%v season=%v", seriesOn, seasonOn)
	}
	if _, raw := get(t, srv, "/tv/seasonpass?saved=1"); !strings.Contains(raw, "Season Pass saved for 1 series.") {
		t.Error("want the save reported")
	}
}
