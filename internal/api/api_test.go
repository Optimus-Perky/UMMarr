package api_test

import (
	"database/sql"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// newTestServer builds a router against a fresh temp-SQLite DB, with no
// provider clients configured - safe for every test here since none of
// them exercise the search/add endpoints, which are the only handlers
// that touch a provider client at all.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	db := openTestDB(t)
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db}))
	t.Cleanup(srv.Close)
	return srv
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// newTestServerWithIndexer builds a router with a fake Torznab indexer (added
// as "SomeIndexer" for movies, TV and music) and a fake Deluge wired in, for
// the release-search/grab/activity routes that need them - every other test
// here uses the plain newTestServer instead.
func newTestServerWithIndexer(t *testing.T, indexerHandler, delugeHandler http.HandlerFunc) (srv *httptest.Server, db *sql.DB, indexerURL string) {
	t.Helper()
	db = openTestDB(t)

	indexerSrv := httptest.NewServer(indexerHandler)
	t.Cleanup(indexerSrv.Close)

	delugeSrv := httptest.NewServer(delugeHandler)
	t.Cleanup(delugeSrv.Close)

	if _, err := store.CreateIndexer(t.Context(), db, store.Indexer{
		Name: "SomeIndexer", Implementation: "Torznab", Priority: 25, BaseURL: indexerSrv.URL, APIPath: "/api",
		EnableRSS: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true,
		Categories: []int{2000, 5000, 3000}, MinimumSeeders: 1,
	}); err != nil {
		t.Fatalf("create indexer: %v", err)
	}

	importService := &sync.ImportService{DB: db}
	indexers := &sync.IndexerService{DB: db}
	download := &sync.DownloadService{
		DB: db, Import: importService, Indexers: indexers,
		BootstrapDelugeBaseURL: delugeSrv.URL, BootstrapDelugePassword: "x",
	}
	srv = httptest.NewServer(api.NewRouter(api.Deps{
		DB:       db,
		Indexer:  indexers,
		Download: download,
		Import:   importService,
		Search:   &sync.SearchService{DB: db, Indexers: indexers, Download: download},
	}))
	t.Cleanup(srv.Close)
	return srv, db, indexerSrv.URL
}

func torznabFeed(items ...string) string {
	return `<?xml version="1.0"?><rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>` +
		strings.Join(items, "") + `</channel></rss>`
}

func torznabItem(guid, title, extra string) string {
	return `<item><title>` + html.EscapeString(title) + `</title><guid>` + guid + `</guid>` + extra + `</item>`
}

// grabbableIndexer answers every search with one release called title,
// whose download link redirects to a magnet. Capabilities aren't offered, so
// searches are plain text.
func grabbableIndexer(title string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/download":
			w.Header().Set("Location", "magnet:?xt=urn:btih:deadbeef")
			w.WriteHeader(http.StatusFound)
		case r.URL.Query().Get("t") == "caps":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.Write([]byte(torznabFeed(torznabItem("abc", title, `<link>http://`+r.Host+`/download</link>`))))
		}
	}
}

var grabIndexerID = regexp.MustCompile(`name="indexer_id" value="(\d+)"`)

// grabFromSearch runs the search at searchPath and posts its first result's
// Grab form to grabPath, as clicking Grab does.
func grabFromSearch(t *testing.T, srv *httptest.Server, searchPath, grabPath string) *http.Response {
	t.Helper()
	status, body := get(t, srv, searchPath)
	m := grabIndexerID.FindStringSubmatch(body)
	if status != http.StatusOK || m == nil || !strings.Contains(body, `name="guid" value="abc"`) {
		t.Fatalf("want a grabbable result from %s, got %d:\n%s", searchPath, status, body)
	}
	resp, err := http.PostForm(srv.URL+grabPath, url.Values{"indexer_id": {m[1]}, "guid": {"abc"}})
	if err != nil {
		t.Fatalf("grab: %v", err)
	}
	return resp
}

func seedTestMovie(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	return seedTestMovieWithRootFolder(t, db, "/media/movies")
}

// seedTestMovieWithRootFolder is seedTestMovie but with a caller-chosen
// root folder path - needed by tests that actually copy a file into the
// movie's resolved folder (the fixed "/media/movies" seedTestMovie uses
// isn't writable in this sandbox), so those pass a real t.TempDir().
func seedTestMovieWithRootFolder(t *testing.T, db *sql.DB, rootFolder string) int64 {
	t.Helper()
	rootFolderID, err := store.CreateRootFolder(t.Context(), db, rootFolder, "movie")
	if err != nil {
		t.Fatalf("create root folder: %v", err)
	}
	qualityProfileID, err := store.CreateQualityProfile(t.Context(), db, "Any")
	if err != nil {
		t.Fatalf("create quality profile: %v", err)
	}
	metadataID, err := store.UpsertMovieMetadata(t.Context(), db, metadata.MovieMetadata{
		Title:       metadata.Field[string]{Value: "Inception", Provider: "tmdb"},
		Year:        metadata.Field[int]{Value: 2010, Provider: "tmdb"},
		ExternalIDs: map[string]string{"tmdb": "27205"},
	})
	if err != nil {
		t.Fatalf("upsert movie_metadata: %v", err)
	}
	movieID, err := store.UpsertMovie(t.Context(), db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert movie: %v", err)
	}
	return movieID
}

func get(t *testing.T, srv *httptest.Server, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body for %s: %v", path, err)
	}
	return resp.StatusCode, string(body)
}

func TestPages_RenderDistinctContent(t *testing.T) {
	srv := newTestServer(t)

	cases := []struct {
		path      string
		wantTitle string
	}{
		{"/", "<title>UMMarr · Home</title>"},
		{"/movies", "<title>UMMarr · Movies</title>"},
		{"/tv", "<title>UMMarr · TV Series</title>"},
		{"/music", "<title>UMMarr · Music</title>"},
		{"/activity", "<title>UMMarr · Activity</title>"},
		{"/settings", "<title>UMMarr · Settings</title>"},
	}
	for _, c := range cases {
		status, body := get(t, srv, c.path)
		if status != http.StatusOK {
			t.Errorf("%s: want 200, got %d", c.path, status)
		}
		if !strings.Contains(body, c.wantTitle) {
			t.Errorf("%s: want title %q in body, got:\n%s", c.path, c.wantTitle, body)
		}
	}
}

func TestSettings_CreateRootFolderAndQualityProfile(t *testing.T) {
	srv := newTestServer(t)

	resp, err := http.PostForm(srv.URL+"/settings/root-folders", map[string][]string{
		"path": {"/media/movies"}, "media_type": {"movie"},
	})
	if err != nil {
		t.Fatalf("create root folder: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("HX-Redirect"); got != "/settings/media-management" {
		t.Fatalf("want HX-Redirect /settings/media-management, got %q", got)
	}

	resp2, err := http.PostForm(srv.URL+"/settings/quality-profiles", map[string][]string{"name": {"HD-1080p"}})
	if err != nil {
		t.Fatalf("create quality profile: %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp2.StatusCode)
	}

	_, body := get(t, srv, "/settings/media-management")
	if !strings.Contains(body, "/media/movies") {
		t.Fatalf("want created root folder to appear on /settings, got:\n%s", body)
	}
	_, body = get(t, srv, "/settings/profiles")
	if !strings.Contains(body, "HD-1080p") {
		t.Fatalf("want created quality profile to appear on /settings, got:\n%s", body)
	}
}

// TestSettings_CreateRootFolder_BareNameResolvesUnderDataMount proves the
// fix for "a new folder shouldn't need /data/ typed in front of it": a
// bare subfolder name (no leading "/") gets resolved
// under the shared /data media mount automatically, while an already-
// absolute path (see TestSettings_CreateRootFolderAndQualityProfile
// above) is still accepted verbatim.
func TestSettings_CreateRootFolder_BareNameResolvesUnderDataMount(t *testing.T) {
	srv := newTestServer(t)

	if _, err := http.PostForm(srv.URL+"/settings/root-folders", map[string][]string{
		"path": {"Movies"}, "media_type": {"movie"},
	}); err != nil {
		t.Fatalf("create root folder: %v", err)
	}

	_, body := get(t, srv, "/settings/media-management")
	if !strings.Contains(body, "/data/Movies") {
		t.Fatalf("want the bare name resolved to /data/Movies, got:\n%s", body)
	}
}

func TestMoviesPage_NoRootFolderNotice(t *testing.T) {
	srv := newTestServer(t)

	_, body := get(t, srv, "/movies")
	if !strings.Contains(body, "No movie library folder configured yet") {
		t.Fatalf("want the no-root-folder notice when none exist, got:\n%s", body)
	}
	if strings.Contains(body, `hx-get="/movies/search"`) {
		t.Fatalf("want the search box hidden when no root folder exists")
	}
}

func TestMoviesPage_ShowsSearchOnceRootFolderExists(t *testing.T) {
	srv := newTestServer(t)

	if _, err := http.PostForm(srv.URL+"/settings/root-folders", map[string][]string{
		"path": {"/media/movies"}, "media_type": {"movie"},
	}); err != nil {
		t.Fatalf("create root folder: %v", err)
	}

	_, body := get(t, srv, "/movies")
	if !strings.Contains(body, `hx-get="/movies/search"`) {
		t.Fatalf("want the search box present once a root folder exists, got:\n%s", body)
	}
}

func TestMovieReleases_SearchesIndexersAndRendersResults(t *testing.T) {
	var gotQuery string
	indexerHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotQuery = r.URL.Query().Get("q")
		w.Write([]byte(torznabFeed(torznabItem("abc", "Inception 2010 1080p", `
			<link>http://example.invalid/download?apikey=indexer-secret</link>
			<comments>http://example.invalid/info/abc#comments</comments>
			<size>12345</size>
			<torznab:attr name="seeders" value="10"/>
			<torznab:attr name="peers" value="12"/>
			<torznab:attr name="category" value="2040"/>`))))
	}
	srv, db, _ := newTestServerWithIndexer(t, indexerHandler, func(w http.ResponseWriter, r *http.Request) {})
	movieID := seedTestMovie(t, db)

	status, body := get(t, srv, "/movies/"+itoa(movieID)+"/releases")
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d", status)
	}
	if gotQuery != "Inception 2010" {
		t.Fatalf("want a text search for the title and year, got %q", gotQuery)
	}
	if !strings.Contains(body, "Inception 2010 1080p") || !strings.Contains(body, "SomeIndexer") {
		t.Fatalf("want the release to appear in results, got:\n%s", body)
	}
	if !strings.Contains(body, `href="http://example.invalid/info/abc"`) {
		t.Fatalf("want the title linked to the release's info page, got:\n%s", body)
	}
	if !strings.Contains(body, "Movies/HD") || !strings.Contains(body, "10 / 2") {
		t.Fatalf("want the release's category and seeders / leechers shown, got:\n%s", body)
	}
	if strings.Contains(body, "indexer-secret") {
		t.Fatalf("want download links (and the API keys in them) kept out of the page, got:\n%s", body)
	}
	if !strings.Contains(body, `data-sort="text"`) || !strings.Contains(body, `data-sort="number"`) {
		t.Fatalf("want sortable column headers, got:\n%s", body)
	}
}

// TestMovieReleases_ShowsParsedQualityAndReleaseGroup proves the live
// search table surfaces internal/releaseparse's output (display only,
// not persisted - see TestMovieDetail_ShowsQualityAndReleaseGroupAfterImport
// for the persisted-file case).
func TestMovieReleases_ShowsParsedQualityAndReleaseGroup(t *testing.T) {
	srv, db, _ := newTestServerWithIndexer(t, grabbableIndexer("Inception 2010 BluRay 1080p x265-hallowed"), func(w http.ResponseWriter, r *http.Request) {})
	movieID := seedTestMovie(t, db)

	_, body := get(t, srv, "/movies/"+itoa(movieID)+"/releases")
	if !strings.Contains(body, "hallowed") {
		t.Fatalf("want the parsed release group shown, got:\n%s", body)
	}
	if !strings.Contains(body, "Bluray-1080p") {
		t.Fatalf("want the parsed quality chip shown, got:\n%s", body)
	}
}

func TestMovieReleases_FailingIndexerShowsItsError(t *testing.T) {
	failing := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }
	srv, db, _ := newTestServerWithIndexer(t, failing, func(w http.ResponseWriter, r *http.Request) {})
	movieID := seedTestMovie(t, db)

	_, body := get(t, srv, "/movies/"+itoa(movieID)+"/releases")
	if !strings.Contains(body, "SomeIndexer: indexer returned HTTP 502") {
		t.Fatalf("want the failing indexer named with its error, got:\n%s", body)
	}
	if strings.Contains(body, "No releases found") {
		t.Fatalf("want a failure not shown as an empty search, got:\n%s", body)
	}
}

func TestMovieReleases_NoIndexers(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	movieID := seedTestMovie(t, db)

	_, body := get(t, srv, "/movies/"+itoa(movieID)+"/releases")
	if !strings.Contains(body, "no indexers are enabled") {
		t.Fatalf("want a pointer to Settings -> Indexers, got:\n%s", body)
	}
}

func TestMovieGrab_SendsToDelugeAndRedirectsToActivity(t *testing.T) {
	var sawDownloadLocation bool
	delugeHandler := func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
			ID     int    `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&call)
		switch call.Method {
		case "auth.login":
			json.NewEncoder(w).Encode(map[string]any{"result": true, "error": nil, "id": call.ID})
		case "core.add_torrent_magnet":
			if opts, ok := call.Params[1].(map[string]any); ok {
				if _, ok := opts["download_location"]; ok {
					sawDownloadLocation = true
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"result": "deadbeef", "error": nil, "id": call.ID})
		}
	}
	srv, db, _ := newTestServerWithIndexer(t, grabbableIndexer("Inception 2010 1080p"), delugeHandler)
	movieID := seedTestMovie(t, db)

	resp := grabFromSearch(t, srv, "/movies/"+itoa(movieID)+"/releases", "/movies/"+itoa(movieID)+"/grab")
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("HX-Redirect") != "" {
		t.Fatalf("want to stay on the page after a grab, got a redirect to %q", resp.Header.Get("HX-Redirect"))
	}

	grabs, err := store.ListGrabs(t.Context(), db)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	if len(grabs) != 1 || grabs[0].ReleaseTitle != "Inception 2010 1080p" || !grabs[0].DownloadClientID.Valid ||
		grabs[0].Indexer != "SomeIndexer" || !grabs[0].IndexerID.Valid || grabs[0].GrabbedBy != "interactive" {
		t.Fatalf("want 1 recorded interactive grab from SomeIndexer with a download client id, got %+v", grabs)
	}
	if sawDownloadLocation {
		t.Fatalf("want no download_location override sent to Deluge - grabs must land in Deluge's own default download dir so imports can copy (not move) without breaking seeding")
	}
}

func TestMovieGrab_UnknownResultIsRefused(t *testing.T) {
	srv, db, _ := newTestServerWithIndexer(t, grabbableIndexer("Inception 2010 1080p"), func(w http.ResponseWriter, r *http.Request) {})
	movieID := seedTestMovie(t, db)

	resp, err := http.PostForm(srv.URL+"/movies/"+itoa(movieID)+"/grab", url.Values{"indexer_id": {"1"}, "guid": {"never-searched"}})
	if err != nil {
		t.Fatalf("grab: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "search again") {
		t.Fatalf("want a grab of a release that wasn't in a search refused, got %d: %s", resp.StatusCode, body)
	}
	if grabs, _ := store.ListGrabs(t.Context(), db); len(grabs) != 0 {
		t.Fatalf("want nothing grabbed, got %+v", grabs)
	}
}

func TestActivityQueue_RendersGrabs(t *testing.T) {
	srv, db, _ := newTestServerWithIndexer(t, func(w http.ResponseWriter, r *http.Request) {}, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"result": true, "error": nil, "id": 1})
	})
	movieID := seedTestMovie(t, db)
	if _, err := store.InsertGrab(t.Context(), db, store.Grab{
		MovieID: sql.NullInt64{Int64: movieID, Valid: true}, ReleaseTitle: "Inception 2010 1080p",
		Indexer: "SomeIndexer", Protocol: "torrent", DownloadClient: "deluge", Status: "imported",
	}); err != nil {
		t.Fatalf("insert grab: %v", err)
	}

	_, body := get(t, srv, "/activity/queue")
	if !strings.Contains(body, "Inception 2010 1080p") || !strings.Contains(body, "Imported") {
		t.Fatalf("want the grab to appear in the queue, got:\n%s", body)
	}
}

func TestActivityQueue_RendersNeedsExtractionWithRetryButton(t *testing.T) {
	srv, db, _ := newTestServerWithIndexer(t, func(w http.ResponseWriter, r *http.Request) {}, func(w http.ResponseWriter, r *http.Request) {})
	movieID := seedTestMovie(t, db)
	grabID, err := store.InsertGrab(t.Context(), db, store.Grab{
		MovieID: sql.NullInt64{Int64: movieID, Valid: true}, ReleaseTitle: "Inception 2010 1080p",
		Indexer: "SomeIndexer", Protocol: "torrent", DownloadClient: "deluge",
		DownloadClientID: sql.NullString{String: "deadbeef", Valid: true}, Status: "needs_extraction",
	})
	if err != nil {
		t.Fatalf("insert grab: %v", err)
	}

	_, body := get(t, srv, "/activity/queue")
	if !strings.Contains(body, "Needs extraction") {
		t.Fatalf("want the needs_extraction chip, got:\n%s", body)
	}
	if !strings.Contains(body, "hx-post=\"/activity/grabs/"+itoa(grabID)+"/retry\"") {
		t.Fatalf("want a Check again retry button, got:\n%s", body)
	}
}

func TestRetryGrab_ReattemptsImport(t *testing.T) {
	downloadDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(downloadDir, "movie.mkv"), []byte("movie bytes"), 0o644); err != nil {
		t.Fatalf("write extracted file: %v", err)
	}

	delugeHandler := func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&call)
		switch call.Method {
		case "auth.login":
			json.NewEncoder(w).Encode(map[string]any{"result": true, "error": nil, "id": call.ID})
		case "core.get_torrents_status":
			json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"deadbeef": map[string]any{"save_path": downloadDir, "is_finished": true},
				},
				"error": nil, "id": call.ID,
			})
		}
	}
	srv, db, _ := newTestServerWithIndexer(t, func(w http.ResponseWriter, r *http.Request) {}, delugeHandler)
	movieID := seedTestMovieWithRootFolder(t, db, t.TempDir())
	grabID, err := store.InsertGrab(t.Context(), db, store.Grab{
		MovieID: sql.NullInt64{Int64: movieID, Valid: true}, ReleaseTitle: "Inception 2010 1080p",
		Indexer: "SomeIndexer", Protocol: "torrent", DownloadClient: "deluge",
		DownloadClientID: sql.NullString{String: "deadbeef", Valid: true}, Status: "needs_extraction",
	})
	if err != nil {
		t.Fatalf("insert grab: %v", err)
	}

	resp, err := http.Post(srv.URL+"/activity/grabs/"+itoa(grabID)+"/retry", "", nil)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	grabs, err := store.ListGrabs(t.Context(), db)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	if len(grabs) != 1 || grabs[0].Status != "imported" {
		t.Fatalf("want the grab to now be imported, got %+v", grabs)
	}
}

func TestDownloadCompleted_RefreshesMatchingGrab(t *testing.T) {
	delugeHandler := func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&call)
		switch call.Method {
		case "auth.login":
			json.NewEncoder(w).Encode(map[string]any{"result": true, "error": nil, "id": call.ID})
		case "core.get_torrents_status":
			json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"deadbeef": map[string]any{"save_path": t.TempDir(), "is_finished": true, "state": "Seeding"},
				},
				"error": nil, "id": call.ID,
			})
		}
	}
	srv, db, _ := newTestServerWithIndexer(t, func(w http.ResponseWriter, r *http.Request) {}, delugeHandler)
	movieID := seedTestMovie(t, db)
	if _, err := store.InsertGrab(t.Context(), db, store.Grab{
		MovieID: sql.NullInt64{Int64: movieID, Valid: true}, ReleaseTitle: "Inception 2010 1080p",
		Indexer: "SomeIndexer", Protocol: "torrent", DownloadClient: "deluge",
		DownloadClientID: sql.NullString{String: "deadbeef", Valid: true}, Status: "downloading",
	}); err != nil {
		t.Fatalf("insert grab: %v", err)
	}

	resp, err := http.Post(srv.URL+"/downloads/deadbeef/completed", "", nil)
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "import_failed") {
		t.Fatalf("want import_failed status (no video file in the empty temp dir), got %q", body)
	}

	grabs, err := store.ListGrabs(t.Context(), db)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	if len(grabs) != 1 || grabs[0].Status != "import_failed" {
		t.Fatalf("want the grab refreshed to import_failed, got %+v", grabs)
	}
}

func TestDownloadCompleted_UnknownHashReturns404(t *testing.T) {
	srv, _, _ := newTestServerWithIndexer(t, func(w http.ResponseWriter, r *http.Request) {}, func(w http.ResponseWriter, r *http.Request) {})

	resp, err := http.Post(srv.URL+"/downloads/nonexistent/completed", "", nil)
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}

// TestMovieReleases_ShowWhyReleasesWouldBeRejected: every result is judged
// for the movie, rejected ones say why and come after the approved ones, and
// they can still be grabbed by hand, as in Radarr.
func TestMovieReleases_ShowWhyReleasesWouldBeRejected(t *testing.T) {
	indexerHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(torznabFeed(
			torznabItem("wrong", "Interstellar 2014 1080p BluRay", `<torznab:attr name="seeders" value="50"/>`),
			torznabItem("subs", "Inception 2010 KORSUB 1080p BluRay", `<torznab:attr name="seeders" value="50"/>`),
			torznabItem("good", "Inception 2010 720p BluRay", `<torznab:attr name="seeders" value="5"/>`),
		)))
	}
	srv, db, _ := newTestServerWithIndexer(t, indexerHandler, func(w http.ResponseWriter, r *http.Request) {})
	movieID := seedTestMovie(t, db)

	_, body := get(t, srv, "/movies/"+itoa(movieID)+"/releases")
	good, wrong, subs := strings.Index(body, `value="good"`), strings.Index(body, `value="wrong"`), strings.Index(body, `value="subs"`)
	if good < 0 || wrong < 0 || subs < 0 || good > wrong || good > subs {
		t.Fatalf("want all three results with the approved one first, got:\n%s", body)
	}
	if !strings.Contains(body, "<li>Wrong movie: release is for Interstellar (2014)</li>") || !strings.Contains(body, "<li>Hardcoded subs found: KORSUB</li>") {
		t.Fatalf("want each rejection's reason listed, got:\n%s", body)
	}
	if strings.Count(body, `class="release-rejections"`) != 2 {
		t.Fatalf("want only the two rejected releases marked, got:\n%s", body)
	}
	if strings.Count(body, "disabled") != 0 {
		t.Fatalf("want rejected releases still grabbable by hand, got:\n%s", body)
	}
}

func TestHome_ShowsTheBanner(t *testing.T) {
	srv := newTestServer(t)
	_, body := get(t, srv, "/")
	if !strings.Contains(body, `<img class="home-banner" src="/static/banner.png"`) {
		t.Fatalf("want the branding banner on the home page")
	}
	if status, _ := get(t, srv, "/static/banner.png"); status != http.StatusOK {
		t.Fatalf("want the banner served, got %d", status)
	}
}
