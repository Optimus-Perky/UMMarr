package api_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// magnetIndexer answers every search with the given releases, each
// downloadable as a magnet.
func magnetIndexer(titles ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var items []string
		for i, title := range titles {
			guid := "r" + itoa(int64(i))
			items = append(items, torznabItem(guid, title, `<torznab:attr name="magneturl" value="magnet:?xt=urn:btih:`+guid+`"/><torznab:attr name="seeders" value="20"/>`))
		}
		w.Write([]byte(torznabFeed(items...)))
	}
}

func TestMovieSearch_GrabsAndReports(t *testing.T) {
	srv, db, _ := newTestServerWithIndexer(t, magnetIndexer("Inception 2010 720p BluRay", "Inception 2010 1080p BluRay"), delugeAcceptingMagnets)
	movieID := seedTestMovie(t, db)

	resp, body := postForm(t, srv, "/movies/"+itoa(movieID)+"/search", nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Grabbed Inception 2010 1080p BluRay from SomeIndexer") {
		t.Fatalf("want the best release grabbed and reported, got %d:\n%s", resp.StatusCode, body)
	}
	grabs, _ := store.ListGrabs(t.Context(), db)
	if len(grabs) != 1 || grabs[0].GrabbedBy != "automatic" {
		t.Fatalf("want one automatic grab, got %+v", grabs)
	}

	_, body = postForm(t, srv, "/movies/"+itoa(movieID)+"/search", nil)
	if !strings.Contains(body, "Nothing grabbed: 2 releases found") || !strings.Contains(body, "already in the download queue") {
		t.Fatalf("want a second search to explain why nothing was grabbed, got:\n%s", body)
	}
}

func TestSearchAllMissing_ShowsProgress(t *testing.T) {
	srv, db, _ := newTestServerWithIndexer(t, magnetIndexer("Inception 2010 1080p BluRay"), delugeAcceptingMagnets)
	seedTestMovie(t, db)
	if _, err := db.Exec(`UPDATE movie_metadata SET physical_release = '2010-12-07'`); err != nil {
		t.Fatalf("set release date: %v", err)
	}

	_, body := postForm(t, srv, "/movies/search-missing", nil)
	if !strings.Contains(body, `id="search-missing-movie"`) {
		t.Fatalf("want the progress element, got:\n%s", body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(body, "Searched 1, grabbed 1") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		_, body = get(t, srv, "/movies/search-missing")
	}
	if !strings.Contains(body, "Searched 1, grabbed 1") {
		t.Fatalf("want the finished run reported, got:\n%s", body)
	}
	_, page := get(t, srv, "/movies")
	if !strings.Contains(page, "Search all missing") {
		t.Fatalf("want the Search all missing button on the Movies page")
	}
}

func TestIndexerOptions_SaveAndValidate(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	values := url.Values{
		"minimum_age": {"30"}, "retention": {"3000"}, "maximum_size": {"20000"}, "availability_delay": {"-7"},
		"rss_sync_interval": {"5"}, "whitelisted_hardcoded_subs": {"nlsub"}, "prefer_indexer_flags": {"on"},
	}

	_, body := postForm(t, srv, "/settings/indexers/options", values)
	if !strings.Contains(body, "Nothing was saved") || !strings.Contains(body, "10 to 120 minutes") {
		t.Fatalf("want an RSS interval of 5 refused, got:\n%s", body)
	}
	if s, _ := store.GetIndexerSettings(t.Context(), db); s.MinimumAge != 0 {
		t.Fatalf("want nothing saved, got %+v", s)
	}

	values.Set("rss_sync_interval", "15")
	resp, _ := postForm(t, srv, "/settings/indexers/options", values)
	if resp.Header.Get("HX-Redirect") != "/settings/indexers" {
		t.Fatalf("want a redirect after saving, got %d", resp.StatusCode)
	}
	s, _ := store.GetIndexerSettings(t.Context(), db)
	if s.MinimumAge != 30 || s.Retention != 3000 || s.MaximumSize != 20000 || s.AvailabilityDelay != -7 || s.RSSSyncInterval != 15 ||
		!s.PreferIndexerFlags || s.AllowHardcodedSubs || s.WhitelistedHardcodedSubs != "nlsub" {
		t.Fatalf("want the options saved, got %+v", s)
	}
	_, page := get(t, srv, "/settings/indexers")
	for _, want := range []string{`name="retention" value="3000"`, `name="availability_delay" value="-7"`, "Run RSS Sync now", "please follow the rules set forth by them"} {
		if !strings.Contains(page, want) {
			t.Errorf("want the Options section to contain %q", want)
		}
	}
}

// TestAddSearchRoutesStayOnTheAddSearch guards the add-a-movie/series/artist
// search boxes: automatic search handlers share the "search" name, and a
// rename once pointed GET /movies/search at the wrong one.
func TestAddSearchRoutesStayOnTheAddSearch(t *testing.T) {
	srv := newTestServer(t)
	// An empty query renders the add search's empty results without asking a
	// metadata provider; an automatic search handler would refuse the path.
	for _, path := range []string{"/movies/search", "/tv/search", "/music/search"} {
		if status, body := get(t, srv, path); status != http.StatusOK || strings.Contains(body, "invalid") {
			t.Errorf("%s: want the add search's empty results, got %d:\n%s", path, status, body)
		}
	}
}

// TestRefresh_SaysWhatTheScanFound: the Refresh (scan folder) button used to
// reload the page silently, so it looked like it did nothing.
func TestRefresh_SaysWhatTheScanFound(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	movieID := seedTestMovieWithRootFolder(t, db, t.TempDir())

	resp, _ := postForm(t, srv, "/movies/"+itoa(movieID)+"/refresh", nil)
	redirect := resp.Header.Get("HX-Redirect")
	if !strings.HasSuffix(redirect, "?scanned=0") {
		t.Fatalf("want the redirect to carry the scan result, got %q", redirect)
	}
	_, body := get(t, srv, redirect)
	if !strings.Contains(body, "Folder scanned: nothing new found") {
		t.Fatalf("want the page to say the folder was scanned, got:\n%s", body)
	}
	if _, body := get(t, srv, "/movies/"+itoa(movieID)); strings.Contains(body, "Folder scanned") {
		t.Fatalf("want no notice on a plain page load")
	}
}

// TestGrab_StaysOnThePage: grabbing replaces the result's row with a Grabbed
// line and, for an episode, updates its Status chip in place - no jump to
// Activity.
func TestGrab_StaysOnThePage(t *testing.T) {
	srv, db, _ := newTestServerWithIndexer(t, grabbableIndexer("Breaking Bad S01E01 1080p"), delugeAcceptingMagnets)
	seriesID := seedTestSeries(t, db)
	var episodeID int64
	if err := db.QueryRow(`SELECT id FROM episodes WHERE series_id = ?`, seriesID).Scan(&episodeID); err != nil {
		t.Fatalf("episode: %v", err)
	}
	resp := grabFromSearch(t, srv, fmt.Sprintf("/tv/%d/episodes/%d/releases", seriesID, episodeID), fmt.Sprintf("/tv/%d/episodes/%d/grab", seriesID, episodeID))
	body := readBody(t, resp)
	if resp.Header.Get("HX-Redirect") != "" || !strings.Contains(body, "Grabbed Breaking Bad S01E01 1080p from SomeIndexer") {
		t.Fatalf("want a Grabbed row and no redirect, got %q:\n%s", resp.Header.Get("HX-Redirect"), body)
	}
	if !strings.Contains(body, fmt.Sprintf(`id="episode-status-%d" hx-swap-oob="true"><span class="chip chip-info">Downloading</span>`, episodeID)) {
		t.Fatalf("want the episode's Status chip updated in place, got:\n%s", body)
	}
	_, page := get(t, srv, "/"+"tv/"+itoa(seriesID))
	if !strings.Contains(page, "release-dialog.js") || !strings.Contains(page, `id="release-dialog-body"`) || !strings.Contains(page, fmt.Sprintf(`id="episode-status-%d"`, episodeID)) {
		t.Fatalf("want the dialog, its script and an addressable status cell on the page")
	}
}

// TestEpisodeReleases_OpensSonarrStyleDialog: Find release renders Sonarr's
// interactive search dialog - a heading naming the episode, Details, History
// and Search tabs, and a results table with languages, flags and coloured
// peers.
func TestEpisodeReleases_OpensSonarrStyleDialog(t *testing.T) {
	indexer := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(torznabFeed(
			torznabItem("a", "Breaking Bad S01E01 GERMAN DL 1080p WEB-DL x264-GRP", `<torznab:attr name="seeders" value="19"/><torznab:attr name="downloadvolumefactor" value="0"/>`),
			torznabItem("b", "Breaking Bad S01E01 720p HDTV x264-GRP", `<torznab:attr name="seeders" value="0"/>`),
		)))
	}
	srv, db, _ := newTestServerWithIndexer(t, indexer, func(w http.ResponseWriter, r *http.Request) {})
	seriesID := seedTestSeries(t, db)
	var episodeID int64
	if err := db.QueryRow(`SELECT id FROM episodes WHERE series_id = ?`, seriesID).Scan(&episodeID); err != nil {
		t.Fatalf("episode: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO grabs (series_id, season_number, episode_number, release_title, indexer, protocol, download_client, status, grabbed_by) VALUES (?, 1, 1, 'Breaking Bad S01E01 old grab', 'OldIndexer', 'torrent', 'deluge', 'imported', 'interactive')`, seriesID); err != nil {
		t.Fatalf("grab: %v", err)
	}

	_, body := get(t, srv, fmt.Sprintf("/tv/%d/episodes/%d/releases", seriesID, episodeID))
	for _, want := range []string{
		"<h2>Breaking Bad - 1x01 - Pilot</h2>", `data-tab="details"`, `data-tab="history"`, `data-tab="search"`,
		"<dt>Airs</dt>", "Breaking Bad S01E01 old grab", "OldIndexer", `class="release-filter"`,
		"German, English", "Freeleech", `class="peers peers-ok">19 / 0`, `class="peers peers-none">0 / 0`, `<option value="custom">Custom filter…</option>`, `class="custom-filter-slot"`,
		`data-title="Breaking Bad S01E01 GERMAN DL 1080p WEB-DL x264-GRP"`, `data-seeders="19"`, `data-languages="German, English"`, `data-flags="Freeleech"`,
		`data-approved="0"`, `data-protocol="torrent"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("want the dialog to contain %q", want)
		}
	}
}

// TestEpisodeReleases_OnlyShowsQualityProfileMatches: asked for TV
// search to only show results matching the (series') quality profile,
// rather than showing every release with a rejection badge as other
// rejection reasons do - unlike TestEpisodeReleases_OpensSonarrStyleDialog
// above, a release outside the profile shouldn't appear at all, not just
// get marked unapproved.
func TestEpisodeReleases_OnlyShowsQualityProfileMatches(t *testing.T) {
	indexer := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(torznabFeed(
			torznabItem("allowed", "Breaking Bad S01E01 1080p WEB-DL x264-GRP", `<torznab:attr name="seeders" value="19"/>`),
			torznabItem("disallowed", "Breaking Bad S01E01 720p HDTV x264-GRP", `<torznab:attr name="seeders" value="19"/>`),
		)))
	}
	srv, db, _ := newTestServerWithIndexer(t, indexer, func(w http.ResponseWriter, r *http.Request) {})
	seriesID := seedTestSeries(t, db)
	var episodeID, profileID int64
	if err := db.QueryRow(`SELECT id FROM episodes WHERE series_id = ?`, seriesID).Scan(&episodeID); err != nil {
		t.Fatalf("episode: %v", err)
	}
	if err := db.QueryRow(`SELECT quality_profile_id FROM series WHERE id = ?`, seriesID).Scan(&profileID); err != nil {
		t.Fatalf("series profile: %v", err)
	}

	items, err := store.GetQualityProfileItems(t.Context(), db, profileID)
	if err != nil {
		t.Fatalf("get profile items: %v", err)
	}
	for i := range items {
		if items[i].Quality == "HDTV-720p" {
			items[i].Allowed = false
		}
	}
	if err := store.UpdateQualityProfileItems(t.Context(), db, profileID, items); err != nil {
		t.Fatalf("update profile items: %v", err)
	}

	_, body := get(t, srv, fmt.Sprintf("/tv/%d/episodes/%d/releases", seriesID, episodeID))
	if !strings.Contains(body, "1080p WEB-DL") {
		t.Errorf("want the profile-allowed release shown, got:\n%s", body)
	}
	if strings.Contains(body, "720p HDTV") {
		t.Errorf("want the profile-disallowed release hidden entirely, not just marked unapproved, got:\n%s", body)
	}
}

// TestSeasonReleases_SeasonPackFilter: Sonarr's Season Pack / Not Season Pack
// filters, with a season search opening on Season Pack.
func TestSeasonReleases_SeasonPackFilter(t *testing.T) {
	indexer := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(torznabFeed(
			torznabItem("pack", "Breaking Bad S01 1080p BluRay x264-GRP", `<torznab:attr name="seeders" value="9"/>`),
			torznabItem("ep", "Breaking Bad S01E01 1080p BluRay x264-GRP", `<torznab:attr name="seeders" value="9"/>`),
		)))
	}
	srv, db, _ := newTestServerWithIndexer(t, indexer, func(w http.ResponseWriter, r *http.Request) {})
	seriesID := seedTestSeries(t, db)

	_, body := get(t, srv, fmt.Sprintf("/tv/%d/releases?season=1", seriesID))
	for _, want := range []string{
		`<option value="season-pack" selected>Season packs</option>`, `<option value="not-season-pack">Not season packs</option>`,
		`<span class="chip chip-muted">Season pack</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q in a season search", want)
		}
	}
	rows := strings.Split(body, "<tr")
	for _, want := range []struct{ title, pack string }{{"Breaking Bad S01 1080p BluRay x264-GRP", "1"}, {"Breaking Bad S01E01 1080p BluRay x264-GRP", "0"}} {
		found := false
		for _, row := range rows {
			if strings.Contains(row, `data-title="`+want.title+`"`) {
				found = true
				if !strings.Contains(row, `data-seasonpack="`+want.pack+`"`) {
					t.Errorf("want %s marked seasonpack=%s", want.title, want.pack)
				}
			}
		}
		if !found {
			t.Errorf("want a row for %s", want.title)
		}
	}
	_, body = get(t, srv, fmt.Sprintf("/tv/%d/releases", seriesID))
	if !strings.Contains(body, `<option value="all" selected>All</option>`) || strings.Contains(body, `value="season-pack" selected`) {
		t.Errorf("want a whole-series search to open on All")
	}
}
