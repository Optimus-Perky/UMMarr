package api_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// newTestServerWithDB is newTestServer but against a caller-supplied db,
// for tests that need to seed data (via a helper taking *sql.DB) before or
// interleaved with issuing requests.
func newTestServerWithDB(t *testing.T, db *sql.DB) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Import: &sync.ImportService{DB: db}}))
	t.Cleanup(srv.Close)
	return srv
}

func seedTestSeries(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	return seedTestSeriesWithRootFolder(t, db, "/media/tv")
}

// seedTestSeriesWithRootFolder is seedTestSeries but with a caller-chosen
// root folder path - needed by tests that actually write a file into the
// series' resolved folder, since the fixed "/media/tv" isn't writable in
// this sandbox (same reasoning as seedTestMovieWithRootFolder).
func seedTestSeriesWithRootFolder(t *testing.T, db *sql.DB, rootFolder string) int64 {
	t.Helper()
	ctx := t.Context()
	rootFolderID, err := store.CreateRootFolder(ctx, db, rootFolder, "series")
	if err != nil {
		t.Fatalf("create root folder: %v", err)
	}
	qualityProfileID, err := store.CreateQualityProfile(ctx, db, "Any")
	if err != nil {
		t.Fatalf("create quality profile: %v", err)
	}
	metadataID, err := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{
		Title:       metadata.Field[string]{Value: "Breaking Bad", Provider: "tmdb"},
		ExternalIDs: map[string]string{"tmdb": "1396"},
	})
	if err != nil {
		t.Fatalf("upsert series_metadata: %v", err)
	}
	seriesID, err := store.UpsertSeries(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert series: %v", err)
	}
	seasonID, err := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: 1})
	if err != nil {
		t.Fatalf("upsert season: %v", err)
	}
	if _, err := store.UpsertEpisode(ctx, db, seriesID, seasonID, 1, metadata.EpisodeMetadata{
		EpisodeNumber: 1, Title: metadata.Field[string]{Value: "Pilot", Provider: "tmdb"},
	}); err != nil {
		t.Fatalf("upsert episode: %v", err)
	}
	return seriesID
}

func seedTestAlbum(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	ctx := t.Context()
	rootFolderID, err := store.CreateRootFolder(ctx, db, "/media/music", "music")
	if err != nil {
		t.Fatalf("create root folder: %v", err)
	}
	qualityProfileID, err := store.CreateQualityProfile(ctx, db, "Any")
	if err != nil {
		t.Fatalf("create quality profile: %v", err)
	}
	artistMetadataID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name:        metadata.Field[string]{Value: "Daft Punk", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "daft-punk-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert artist_metadata: %v", err)
	}
	if _, err := store.UpsertArtist(ctx, db, artistMetadataID, qualityProfileID, rootFolderID, true); err != nil {
		t.Fatalf("upsert artist: %v", err)
	}
	albumID, _, err := store.UpsertAlbum(ctx, db, artistMetadataID, metadata.AlbumMetadata{
		Title:       metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "homework-rg-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert album: %v", err)
	}
	releaseID, err := store.UpsertAlbumRelease(ctx, db, albumID, metadata.ReleaseMetadata{
		Title:       metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "homework-release-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert album_release: %v", err)
	}
	if _, err := store.UpsertTrack(ctx, db, releaseID, artistMetadataID, metadata.TrackSource{
		Number: "1", Title: "Daftendirekt", DurationMs: 210000, MediumNumber: 1,
	}); err != nil {
		t.Fatalf("upsert track: %v", err)
	}
	return albumID
}

func TestMovieDetail_RendersTitleAndFindReleaseWhenNoFile(t *testing.T) {
	srv, db, _ := newTestServerWithIndexer(t, func(w http.ResponseWriter, r *http.Request) {}, func(w http.ResponseWriter, r *http.Request) {})
	movieID := seedTestMovie(t, db)

	status, body := get(t, srv, "/movies/"+itoa(movieID))
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d", status)
	}
	if !strings.Contains(body, "Inception") {
		t.Fatalf("want the movie's title rendered, got:\n%s", body)
	}
	if !strings.Contains(body, "No file yet") || !strings.Contains(body, "Find release") {
		t.Fatalf("want a no-file notice with a Find release button, got:\n%s", body)
	}
}

func TestMovieDetail_UnknownIDReturns404(t *testing.T) {
	srv := newTestServer(t)
	status, _ := get(t, srv, "/movies/999")
	if status != http.StatusNotFound {
		t.Fatalf("want 404 for an unknown movie id, got %d", status)
	}
}

func TestSeriesDetail_RendersSeasonsAndEpisodes(t *testing.T) {
	dbConn := openTestDB(t)
	srv := newTestServerWithDB(t, dbConn)
	seriesID := seedTestSeries(t, dbConn)

	status, body := get(t, srv, "/tv/"+itoa(seriesID))
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d", status)
	}
	if !strings.Contains(body, "Breaking Bad") {
		t.Fatalf("want the series' title rendered, got:\n%s", body)
	}
	if !strings.Contains(body, "Season 1") || !strings.Contains(body, "Pilot") {
		t.Fatalf("want season 1 and its episode rendered, got:\n%s", body)
	}
	if !strings.Contains(body, "Missing") {
		t.Fatalf("want the un-filed episode's status chip rendered, got:\n%s", body)
	}
}

func TestAlbumDetail_RendersTracks(t *testing.T) {
	dbConn := openTestDB(t)
	srv := newTestServerWithDB(t, dbConn)
	albumID := seedTestAlbum(t, dbConn)

	status, body := get(t, srv, "/music/albums/"+itoa(albumID))
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d", status)
	}
	if !strings.Contains(body, "Homework") || !strings.Contains(body, "Daftendirekt") {
		t.Fatalf("want the album title and its track rendered, got:\n%s", body)
	}
	if !strings.Contains(body, "3:30") {
		t.Fatalf("want the track's duration formatted as mm:ss, got:\n%s", body)
	}
}

// TestMovieRefresh_FindsFileAlreadyOnDisk is the API-level proof for the
// "the file is there but there's no way to point UMMarr at it" case: a file
// sitting in the movie's own folder that never went through a grab/
// import shows up after clicking Refresh, with no restart/re-add needed.
func TestMovieRefresh_FindsFileAlreadyOnDisk(t *testing.T) {
	dbConn := openTestDB(t)
	srv := newTestServerWithDB(t, dbConn)
	movieID := seedTestMovieWithRootFolder(t, dbConn, t.TempDir())

	moviePath, err := store.ResolveMoviePath(t.Context(), dbConn, movieID)
	if err != nil {
		t.Fatalf("resolve movie path: %v", err)
	}
	if err := os.MkdirAll(moviePath, 0o755); err != nil {
		t.Fatalf("mkdir movie path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moviePath, "Inception.2010.1080p.WEB.mkv"), []byte("bytes"), 0o644); err != nil {
		t.Fatalf("write movie file: %v", err)
	}

	_, before := get(t, srv, "/movies/"+itoa(movieID))
	if !strings.Contains(before, "No file yet") {
		t.Fatalf("want the movie to show as missing before refresh, got:\n%s", before)
	}

	resp, err := http.Post(srv.URL+"/movies/"+itoa(movieID)+"/refresh", "", nil)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("HX-Redirect"); got != "/movies/"+itoa(movieID)+"?scanned=1" {
		t.Fatalf("want HX-Redirect back to the detail page saying 1 file was picked up, got %q", got)
	}

	_, after := get(t, srv, "/movies/"+itoa(movieID))
	if strings.Contains(after, "No file yet") {
		t.Fatalf("want the file found by refresh to be reflected, got:\n%s", after)
	}
}

// TestMovieDetail_ShowsQualityAndReleaseGroupAfterImport proves the
// Files table reflects quality/release-group once Refresh (or any other
// import path) has parsed it from the found file's own name.
func TestMovieDetail_ShowsQualityAndReleaseGroupAfterImport(t *testing.T) {
	dbConn := openTestDB(t)
	srv := newTestServerWithDB(t, dbConn)
	movieID := seedTestMovieWithRootFolder(t, dbConn, t.TempDir())

	moviePath, err := store.ResolveMoviePath(t.Context(), dbConn, movieID)
	if err != nil {
		t.Fatalf("resolve movie path: %v", err)
	}
	if err := os.MkdirAll(moviePath, 0o755); err != nil {
		t.Fatalf("mkdir movie path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moviePath, "Inception 2010 BluRay 1080p x265-hallowed.mkv"), []byte("bytes"), 0o644); err != nil {
		t.Fatalf("write movie file: %v", err)
	}
	if _, err := http.Post(srv.URL+"/movies/"+itoa(movieID)+"/refresh", "", nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	_, body := get(t, srv, "/movies/"+itoa(movieID))
	if !strings.Contains(body, "hallowed") {
		t.Fatalf("want the release group shown, got:\n%s", body)
	}
	if !strings.Contains(body, "Bluray-1080p") {
		t.Fatalf("want the quality chip shown, got:\n%s", body)
	}
}

// TestSeriesRefresh_FindsEpisodeAlreadyOnDisk mirrors
// TestMovieRefresh_FindsFileAlreadyOnDisk for series.
func TestSeriesRefresh_FindsEpisodeAlreadyOnDisk(t *testing.T) {
	dbConn := openTestDB(t)
	srv := newTestServerWithDB(t, dbConn)
	seriesID := seedTestSeriesWithRootFolder(t, dbConn, t.TempDir())

	seriesPath, err := store.ResolveSeriesPath(t.Context(), dbConn, seriesID)
	if err != nil {
		t.Fatalf("resolve series path: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(seriesPath, "Season 01"), 0o755); err != nil {
		t.Fatalf("mkdir season path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seriesPath, "Season 01", "Show.S01E01.mkv"), []byte("bytes"), 0o644); err != nil {
		t.Fatalf("write episode file: %v", err)
	}

	resp, err := http.Post(srv.URL+"/tv/"+itoa(seriesID)+"/refresh", "", nil)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("HX-Redirect"); got != "/tv/"+itoa(seriesID)+"?scanned=1" {
		t.Fatalf("want HX-Redirect back to the detail page saying 1 file was picked up, got %q", got)
	}

	_, body := get(t, srv, "/tv/"+itoa(seriesID))
	if !strings.Contains(body, `<span class="quality-pill">Unknown</span>`) {
		t.Fatalf("want the found episode shown with an Unknown quality pill (its name says nothing), got:\n%s", body)
	}
}

// TestEpisodeReleases_SearchesForThatEpisode is the API-level proof for
// searching one individual episode or track: the
// per-episode Find release button must query for that one episode, not
// the whole series/season.
func TestEpisodeReleases_SearchesForThatEpisode(t *testing.T) {
	var gotQuery string
	indexerHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotQuery = r.URL.Query().Get("q")
		w.Write([]byte(torznabFeed()))
	}
	srv, db, _ := newTestServerWithIndexer(t, indexerHandler, func(w http.ResponseWriter, r *http.Request) {})
	seriesID := seedTestSeries(t, db)

	var episodeID int64
	if err := db.QueryRowContext(t.Context(), `
		SELECT id FROM episodes WHERE series_id = ? AND season_number = 1 AND episode_number = 1
	`, seriesID).Scan(&episodeID); err != nil {
		t.Fatalf("find episode: %v", err)
	}

	status, _ := get(t, srv, fmt.Sprintf("/tv/%d/episodes/%d/releases", seriesID, episodeID))
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d", status)
	}
	if gotQuery != "Breaking Bad S01E01" {
		t.Fatalf("want query %q, got %q", "Breaking Bad S01E01", gotQuery)
	}
}

func delugeAcceptingMagnets(w http.ResponseWriter, r *http.Request) {
	var call struct {
		Method string `json:"method"`
		ID     int    `json:"id"`
	}
	json.NewDecoder(r.Body).Decode(&call)
	switch call.Method {
	case "auth.login":
		json.NewEncoder(w).Encode(map[string]any{"result": true, "error": nil, "id": call.ID})
	case "core.add_torrent_magnet":
		json.NewEncoder(w).Encode(map[string]any{"result": "deadbeef", "error": nil, "id": call.ID})
	}
}

// TestEpisodeGrab_RecordsEpisodeNumber grabs a release for one specific
// episode and confirms the resulting grab row records it.
func TestEpisodeGrab_RecordsEpisodeNumber(t *testing.T) {
	srv, db, _ := newTestServerWithIndexer(t, grabbableIndexer("Breaking Bad S01E01"), delugeAcceptingMagnets)
	seriesID := seedTestSeries(t, db)
	var episodeID int64
	if err := db.QueryRowContext(t.Context(), `
		SELECT id FROM episodes WHERE series_id = ? AND season_number = 1 AND episode_number = 1
	`, seriesID).Scan(&episodeID); err != nil {
		t.Fatalf("find episode: %v", err)
	}

	resp := grabFromSearch(t, srv, fmt.Sprintf("/tv/%d/episodes/%d/releases", seriesID, episodeID), fmt.Sprintf("/tv/%d/episodes/%d/grab", seriesID, episodeID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("HX-Redirect") != "" {
		t.Fatalf("want to stay on the page after a grab, got a redirect to %q", resp.Header.Get("HX-Redirect"))
	}

	grabs, err := store.ListGrabs(t.Context(), db)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	if len(grabs) != 1 || !grabs[0].EpisodeNumber.Valid || grabs[0].EpisodeNumber.Int64 != 1 {
		t.Fatalf("want 1 grab with episode_number 1, got %+v", grabs)
	}
}

// TestTrackReleases_SearchesForThatTrack mirrors
// TestEpisodeReleases_SearchesForThatEpisode for music.
func TestTrackReleases_SearchesForThatTrack(t *testing.T) {
	var gotQuery string
	indexerHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("t") == "caps" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotQuery = r.URL.Query().Get("q")
		w.Write([]byte(torznabFeed()))
	}
	srv, db, _ := newTestServerWithIndexer(t, indexerHandler, func(w http.ResponseWriter, r *http.Request) {})
	albumID := seedTestAlbum(t, db)

	var trackID int64
	if err := db.QueryRowContext(t.Context(), `SELECT id FROM tracks WHERE title = 'Daftendirekt'`).Scan(&trackID); err != nil {
		t.Fatalf("find track: %v", err)
	}

	status, _ := get(t, srv, fmt.Sprintf("/music/albums/%d/tracks/%d/releases", albumID, trackID))
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d", status)
	}
	if gotQuery != "Daft Punk Daftendirekt" {
		t.Fatalf("want query %q, got %q", "Daft Punk Daftendirekt", gotQuery)
	}
}

// TestTrackGrab_RecordsTrackID grabs a release for one specific track and
// confirms the resulting grab row records it, and that Import routes it
// through importTrack (single-file attach), not importAlbum (positional
// match) - see internal/sync's TestImport_Track_AttachesToExactTrackNotPositionalMatch
// for the import-level proof; this is the end-to-end grab-records-it proof.
func TestTrackGrab_RecordsTrackID(t *testing.T) {
	srv, db, _ := newTestServerWithIndexer(t, grabbableIndexer("Daft Punk - Daftendirekt"), delugeAcceptingMagnets)
	albumID := seedTestAlbum(t, db)
	var trackID int64
	if err := db.QueryRowContext(t.Context(), `SELECT id FROM tracks WHERE title = 'Daftendirekt'`).Scan(&trackID); err != nil {
		t.Fatalf("find track: %v", err)
	}

	resp := grabFromSearch(t, srv, fmt.Sprintf("/music/albums/%d/tracks/%d/releases", albumID, trackID), fmt.Sprintf("/music/albums/%d/tracks/%d/grab", albumID, trackID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	grabs, err := store.ListGrabs(t.Context(), db)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	if len(grabs) != 1 || !grabs[0].TrackID.Valid || grabs[0].TrackID.Int64 != trackID {
		t.Fatalf("want 1 grab with track_id %d, got %+v", trackID, grabs)
	}
}

// TestMovieMonitoredToggle_FlipsAndRerendersChip proves a click on the
// Monitored chip flips the DB value and re-renders just the chip (no
// page reload) reflecting the new state, and that a second click flips
// it back.
func TestMovieMonitoredToggle_FlipsAndRerendersChip(t *testing.T) {
	dbConn := openTestDB(t)
	srv := newTestServerWithDB(t, dbConn)
	movieID := seedTestMovie(t, dbConn)

	resp, err := http.Post(srv.URL+"/movies/"+itoa(movieID)+"/monitored", "", nil)
	if err != nil {
		t.Fatalf("toggle: %v", err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Unmonitored") {
		t.Fatalf("want the chip to flip to Unmonitored, got:\n%s", body)
	}
	monitored, err := movieMonitored(t, dbConn, movieID)
	if err != nil {
		t.Fatalf("read monitored: %v", err)
	}
	if monitored {
		t.Fatalf("want monitored=false persisted after toggle")
	}

	resp, err = http.Post(srv.URL+"/movies/"+itoa(movieID)+"/monitored", "", nil)
	if err != nil {
		t.Fatalf("toggle back: %v", err)
	}
	body = readBody(t, resp)
	if !strings.Contains(body, "Monitored") || strings.Contains(body, "Unmonitored") {
		t.Fatalf("want the chip to flip back to Monitored, got:\n%s", body)
	}
	monitored, err = movieMonitored(t, dbConn, movieID)
	if err != nil {
		t.Fatalf("read monitored: %v", err)
	}
	if !monitored {
		t.Fatalf("want monitored=true persisted after second toggle")
	}
}

// TestSeriesMonitoredToggle_Flips mirrors TestMovieMonitoredToggle_FlipsAndRerendersChip
// for the whole-series chip.
func TestSeriesMonitoredToggle_Flips(t *testing.T) {
	dbConn := openTestDB(t)
	srv := newTestServerWithDB(t, dbConn)
	seriesID := seedTestSeries(t, dbConn)

	resp, err := http.Post(srv.URL+"/tv/"+itoa(seriesID)+"/monitored", "", nil)
	if err != nil {
		t.Fatalf("toggle: %v", err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Unmonitored") {
		t.Fatalf("want the chip to flip to Unmonitored, got:\n%s", body)
	}

	var monitored bool
	if err := dbConn.QueryRowContext(t.Context(), `SELECT monitored FROM series WHERE id = ?`, seriesID).Scan(&monitored); err != nil {
		t.Fatalf("read monitored: %v", err)
	}
	if monitored {
		t.Fatalf("want monitored=false persisted after toggle")
	}
}

// TestSeasonMonitoredToggle_Flips proves the per-season chip toggles
// independently of the whole-series flag.
func TestSeasonMonitoredToggle_Flips(t *testing.T) {
	dbConn := openTestDB(t)
	srv := newTestServerWithDB(t, dbConn)
	seriesID := seedTestSeries(t, dbConn)

	resp, err := http.Post(srv.URL+fmt.Sprintf("/tv/%d/seasons/1/monitored", seriesID), "", nil)
	if err != nil {
		t.Fatalf("toggle: %v", err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Unmonitored") {
		t.Fatalf("want the chip to flip to Unmonitored, got:\n%s", body)
	}

	var monitored bool
	if err := dbConn.QueryRowContext(t.Context(), `SELECT monitored FROM seasons WHERE series_id = ? AND season_number = 1`, seriesID).Scan(&monitored); err != nil {
		t.Fatalf("read monitored: %v", err)
	}
	if monitored {
		t.Fatalf("want monitored=false persisted after toggle")
	}

	var seriesMonitored bool
	if err := dbConn.QueryRowContext(t.Context(), `SELECT monitored FROM series WHERE id = ?`, seriesID).Scan(&seriesMonitored); err != nil {
		t.Fatalf("read series monitored: %v", err)
	}
	if !seriesMonitored {
		t.Fatalf("want the whole-series flag left untouched by a season-level toggle")
	}
}

// TestEpisodeMonitoredToggle_Flips proves the per-episode chip toggles
// independently of its season/series.
func TestEpisodeMonitoredToggle_Flips(t *testing.T) {
	dbConn := openTestDB(t)
	srv := newTestServerWithDB(t, dbConn)
	seriesID := seedTestSeries(t, dbConn)

	var episodeID int64
	if err := dbConn.QueryRowContext(t.Context(), `
		SELECT id FROM episodes WHERE series_id = ? AND season_number = 1 AND episode_number = 1
	`, seriesID).Scan(&episodeID); err != nil {
		t.Fatalf("find episode: %v", err)
	}

	resp, err := http.Post(srv.URL+fmt.Sprintf("/tv/%d/episodes/%d/monitored", seriesID, episodeID), "", nil)
	if err != nil {
		t.Fatalf("toggle: %v", err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "monitor-off") {
		t.Fatalf("want the bookmark to flip to its unmonitored (outlined) form, got:\n%s", body)
	}

	var monitored bool
	if err := dbConn.QueryRowContext(t.Context(), `SELECT monitored FROM episodes WHERE id = ?`, episodeID).Scan(&monitored); err != nil {
		t.Fatalf("read monitored: %v", err)
	}
	if monitored {
		t.Fatalf("want monitored=false persisted after toggle")
	}
}

// TestAlbumMonitoredToggle_Flips mirrors TestMovieMonitoredToggle_FlipsAndRerendersChip
// for albums.
func TestAlbumMonitoredToggle_Flips(t *testing.T) {
	dbConn := openTestDB(t)
	srv := newTestServerWithDB(t, dbConn)
	albumID := seedTestAlbum(t, dbConn)

	resp, err := http.Post(srv.URL+"/music/albums/"+itoa(albumID)+"/monitored", "", nil)
	if err != nil {
		t.Fatalf("toggle: %v", err)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Unmonitored") {
		t.Fatalf("want the chip to flip to Unmonitored, got:\n%s", body)
	}

	var monitored bool
	if err := dbConn.QueryRowContext(t.Context(), `SELECT monitored FROM albums WHERE id = ?`, albumID).Scan(&monitored); err != nil {
		t.Fatalf("read monitored: %v", err)
	}
	if monitored {
		t.Fatalf("want monitored=false persisted after toggle")
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func movieMonitored(t *testing.T, db *sql.DB, movieID int64) (bool, error) {
	t.Helper()
	var monitored bool
	err := db.QueryRowContext(t.Context(), `SELECT monitored FROM movies WHERE id = ?`, movieID).Scan(&monitored)
	return monitored, err
}

// TestSeriesDetail_QualityAndPipelineStatusColumns: Quality has its own
// column and Status says where the episode is - Missing, Downloading or
// Imported - both hideable like the other columns.
// TestSeriesEpisodeStatuses_ReflectsLiveChanges: the series detail page
// polls this endpoint (see series_detail.html's hidden hx-trigger="load,
// every 5s" div) so an episode's row updates itself as a grab completes,
// without a full page reload: episodes should appear as they are picked
// up, rather than needing a page refresh, which collapses every expanded
// season. Every row must
// carry hx-swap-oob="true" (an htmx out-of-band swap replaces just that
// #episode-row-<ID> wherever it already is, leaving everything else on
// the page - collapsed seasons, scroll position, an open search-results
// row - untouched) and reflect whatever the DB says right now.
func TestSeriesEpisodeStatuses_ReflectsLiveChanges(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seriesID := seedTestSeries(t, db)
	var e1 int64
	if err := db.QueryRow(`SELECT id FROM episodes WHERE series_id = ? AND season_number = 1 AND episode_number = 1`, seriesID).Scan(&e1); err != nil {
		t.Fatalf("find E01: %v", err)
	}

	_, body := get(t, srv, fmt.Sprintf("/tv/%d/episodes/status", seriesID))
	if !strings.Contains(body, fmt.Sprintf(`id="episode-row-%d" hx-swap-oob="true"`, e1)) {
		t.Fatalf("want an oob-swapped row for episode %d, got:\n%s", e1, body)
	}
	if !strings.Contains(body, "Missing") {
		t.Fatalf("want E01 shown as Missing before any file, got:\n%s", body)
	}
	if !strings.Contains(body, `id="season-stats-1" hx-swap-oob="true"`) || !strings.Contains(body, "0/1") {
		t.Fatalf("want an oob-swapped season 1 stats badge reading 0/1, got:\n%s", body)
	}

	if _, err := store.AttachEpisodeFile(t.Context(), db, e1, "Season 01/x.mkv", 10); err != nil {
		t.Fatalf("attach: %v", err)
	}

	_, body = get(t, srv, fmt.Sprintf("/tv/%d/episodes/status", seriesID))
	if !strings.Contains(body, fmt.Sprintf(`id="episode-row-%d" hx-swap-oob="true"`, e1)) {
		t.Fatalf("want an oob-swapped row for episode %d after import, got:\n%s", e1, body)
	}
	if !strings.Contains(body, "chip-good") {
		t.Fatalf("want E01 shown as imported (chip-good) after attaching a file, got:\n%s", body)
	}
	if !strings.Contains(body, `id="season-stats-1" hx-swap-oob="true"`) || !strings.Contains(body, "1/1") {
		t.Fatalf("want the season 1 stats badge to have updated to 1/1 too, got:\n%s", body)
	}
}

// TestSeriesDetail_EpisodeRowActionButtons_WithIndexerConfigured is a
// regression test: refactoring the episode row into its own "episode_row"
// partial (for live status updates) added an episodeView.HasIndexer field
// that was never actually populated by episodeViewsForSeries, silently
// defaulting to false and hiding every episode's auto-search/manual-search
// buttons on both the full page and the live-update poll - caught only
// once the auto-search and manual-search buttons went missing from every
// episode row in the running app, since no existing test asserted these
// buttons' presence at all.
func TestSeriesDetail_EpisodeRowActionButtons_WithIndexerConfigured(t *testing.T) {
	srv, db, _ := newTestServerWithIndexer(t, magnetIndexer("irrelevant"), func(w http.ResponseWriter, r *http.Request) {})
	seriesID := seedTestSeries(t, db)
	var e1 int64
	if err := db.QueryRow(`SELECT id FROM episodes WHERE series_id = ? AND season_number = 1 AND episode_number = 1`, seriesID).Scan(&e1); err != nil {
		t.Fatalf("find E01: %v", err)
	}
	wantButtons := []string{
		fmt.Sprintf(`hx-post="/tv/%d/episodes/%d/search"`, seriesID, e1),
		fmt.Sprintf(`hx-get="/tv/%d/episodes/%d/releases"`, seriesID, e1),
	}

	_, page := get(t, srv, "/tv/"+itoa(seriesID))
	for _, want := range wantButtons {
		if !strings.Contains(page, want) {
			t.Errorf("series detail page: want %q (auto/manual search button), got:\n%s", want, page)
		}
	}

	_, poll := get(t, srv, fmt.Sprintf("/tv/%d/episodes/status", seriesID))
	for _, want := range wantButtons {
		if !strings.Contains(poll, want) {
			t.Errorf("episode status poll: want %q (auto/manual search button), got:\n%s", want, poll)
		}
	}
}

func TestSeriesDetail_QualityAndPipelineStatusColumns(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seriesID := seedTestSeries(t, db)
	var seasonID int64
	if err := db.QueryRow(`SELECT id FROM seasons WHERE series_id = ? AND season_number = 1`, seriesID).Scan(&seasonID); err != nil {
		t.Fatalf("find season: %v", err)
	}
	for _, n := range []int{2, 3} {
		if _, err := store.UpsertEpisode(t.Context(), db, seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: n, Title: metadata.Field[string]{Value: "Ep " + itoa(int64(n)), Provider: "tmdb"}}); err != nil {
			t.Fatalf("upsert E%02d: %v", n, err)
		}
	}
	var e1, e2 int64
	if err := db.QueryRow(`SELECT id FROM episodes WHERE series_id = ? AND season_number = 1 AND episode_number = 1`, seriesID).Scan(&e1); err != nil {
		t.Fatalf("find E01: %v", err)
	}
	if err := db.QueryRow(`SELECT id FROM episodes WHERE series_id = ? AND season_number = 1 AND episode_number = 2`, seriesID).Scan(&e2); err != nil {
		t.Fatalf("find E02: %v", err)
	}
	fileID, err := store.AttachEpisodeFile(t.Context(), db, e1, "Season 01/x.mkv", 10)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := store.UpdateEpisodeFileQuality(t.Context(), db, fileID, releaseparse.Parse("Show.S01E01.1080p.WEB-DL.x264-GRP")); err != nil {
		t.Fatalf("quality: %v", err)
	}
	if _, err := store.InsertGrab(t.Context(), db, store.Grab{
		SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}, SeasonNumber: sql.NullInt64{Int64: 1, Valid: true}, EpisodeNumber: sql.NullInt64{Int64: 2, Valid: true},
		ReleaseTitle: "Show S01E02", Indexer: "x", Protocol: "torrent", DownloadClient: "deluge", Status: "downloading",
	}); err != nil {
		t.Fatalf("insert grab: %v", err)
	}

	_, body := get(t, srv, "/tv/"+itoa(seriesID))
	rows := strings.Split(strings.ReplaceAll(body, "\n", ""), "<tr")
	find := func(title string) string {
		for _, r := range rows {
			if strings.Contains(r, ">"+title+"</a></td>") {
				return r
			}
		}
		t.Fatalf("no row for %s", title)
		return ""
	}
	for _, want := range []string{`data-col="quality" data-sort="text">Quality`, `data-col="status" data-sort="text">Status`, `data-col="quality" checked> Quality`, `data-col="status" checked> Status`} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q (own, hideable column)", want)
		}
	}
	if r := find("Pilot"); !strings.Contains(r, `<span class="quality-pill">WEBDL-1080p</span>`) || !strings.Contains(r, ">Imported<") {
		t.Errorf("want E01 shown as Imported with its quality, got %s", r)
	}
	if r := find("Ep 2"); !strings.Contains(r, ">Downloading<") || strings.Contains(r, "quality-pill") {
		t.Errorf("want E02 shown as Downloading with no quality, got %s", r)
	}
	if r := find("Ep 3"); !strings.Contains(r, ">Missing<") {
		t.Errorf("want E03 shown as Missing, got %s", r)
	}
}

// TestDetailPages_TitleAddresses: movie and series pages live at title
// addresses, list pages link to them, and the numeric addresses redirect
// there (keeping any query, so Refresh's notice survives).
func TestDetailPages_TitleAddresses(t *testing.T) {
	db := openTestDB(t)
	tvDB := openTestDB(t) // the two seed helpers share a profile name, so separate databases
	srv := newTestServerWithDB(t, db)
	tvSrv := newTestServerWithDB(t, tvDB)
	movieID := seedTestMovie(t, db)
	seriesID := seedTestSeries(t, tvDB)

	if status, body := get(t, srv, "/movies/inception-2010"); status != http.StatusOK || !strings.Contains(body, "<h1>Inception</h1>") {
		t.Fatalf("want the movie page at its title address, got %d", status)
	}
	if status, body := get(t, tvSrv, "/tv/breaking-bad"); status != http.StatusOK || !strings.Contains(body, "Breaking Bad") {
		t.Fatalf("want the series page at its title address, got %d", status)
	}
	if status, _ := get(t, srv, "/movies/no-such-film-1999"); status != http.StatusNotFound {
		t.Fatalf("want 404 for an unknown title, got %d", status)
	}
	_, movies := get(t, srv, "/movies")
	if !strings.Contains(movies, `href="/movies/inception-2010"`) {
		t.Fatalf("want the Movies page to link by title")
	}
	_, tv := get(t, tvSrv, "/tv")
	if !strings.Contains(tv, `href="/tv/breaking-bad"`) {
		t.Fatalf("want the TV page to link by title")
	}

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(srv.URL + "/movies/" + itoa(movieID) + "?scanned=1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "/movies/inception-2010?scanned=1" {
		t.Fatalf("want the numeric address to redirect to the title one with the query kept, got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, err = noFollow.Get(tvSrv.URL + "/tv/" + itoa(seriesID))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.Header.Get("Location") != "/tv/breaking-bad" {
		t.Fatalf("want /tv/breaking-bad, got %q", resp.Header.Get("Location"))
	}
}

// TestAlbumPages_TitleAddresses: album pages live at /music/albums/{artist}/{album}.
func TestAlbumPages_TitleAddresses(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	albumID := seedTestAlbum(t, db)

	if status, body := get(t, srv, "/music/albums/daft-punk/homework"); status != http.StatusOK || !strings.Contains(body, "Homework") {
		t.Fatalf("want the album page at its title address, got %d", status)
	}
	if status, _ := get(t, srv, "/music/albums/daft-punk/discovery"); status != http.StatusNotFound {
		t.Fatalf("want 404 for an unknown album, got %d", status)
	}
	if _, page := get(t, srv, "/music"); !strings.Contains(page, `href="/music/albums/daft-punk/homework"`) {
		t.Fatalf("want the Music page to link by artist and album")
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(srv.URL + "/music/albums/" + itoa(albumID))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "/music/albums/daft-punk/homework" {
		t.Fatalf("want the numeric address to redirect, got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}
