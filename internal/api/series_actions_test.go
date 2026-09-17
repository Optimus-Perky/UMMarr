package api_test

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestSeriesHeader_ShowsSonarrStyleInfo(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seriesID := seedTestSeries(t, db)
	if _, err := db.Exec(`UPDATE series_metadata SET runtime = 56, status = 'ended', first_aired = '2021-12-19', last_aired = '2022-02-27', network = 'Paramount+', original_language = 'English', ratings = '{"tmdb": 8.6}'`); err != nil {
		t.Fatalf("set metadata: %v", err)
	}
	var metadataID int64
	if err := db.QueryRow(`SELECT series_metadata_id FROM series WHERE id = ?`, seriesID).Scan(&metadataID); err != nil {
		t.Fatalf("metadata id: %v", err)
	}
	for provider, id := range map[string]string{"tvdb": "393277", "imdb": "tt13111040", "tvmaze": "52393"} {
		if err := store.UpsertExternalID(t.Context(), db, "series", metadataID, provider, id); err != nil {
			t.Fatalf("external id: %v", err)
		}
	}
	var e1 int64
	if err := db.QueryRow(`SELECT id FROM episodes WHERE series_id = ?`, seriesID).Scan(&e1); err != nil {
		t.Fatalf("episode: %v", err)
	}
	if _, err := store.AttachEpisodeFile(t.Context(), db, e1, "Season 01/x.mkv", 10<<30); err != nil {
		t.Fatalf("attach: %v", err)
	}

	_, raw := get(t, srv, "/tv/"+itoa(seriesID))
	body := html.UnescapeString(raw) // html/template writes + as &#43;
	for _, want := range []string{
		"56 Minutes", "86%", "2021-2022", "10.0 GiB", "Paramount+", "English",
		`href="https://thetvdb.com/dereferrer/series/393277"`, `href="https://www.imdb.com/title/tt13111040/"`,
		`href="https://www.tvmaze.com/shows/52393"`, `href="https://trakt.tv/search/tvdb/393277?id_type=show"`,
		"Preview Rename", "Series Monitoring", "Manage Episodes", `hx-get="/tv/` + itoa(seriesID) + `/delete"`, `hx-get="/tv/` + itoa(seriesID) + `/edit"`, `href="/history?series=` + itoa(seriesID) + `"`,
		`id="season-order"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("want the header to contain %q", want)
		}
	}
}

func TestSeriesSeasons_NewestFirst(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seriesID := seedTestSeries(t, db)
	for _, n := range []int{2, 3} {
		if _, err := store.UpsertSeason(t.Context(), db, seriesID, metadata.SeasonMetadata{SeasonNumber: n}); err != nil {
			t.Fatalf("season %d: %v", n, err)
		}
	}
	_, body := get(t, srv, "/tv/"+itoa(seriesID))
	s3, s1 := strings.Index(body, `data-season="3"`), strings.Index(body, `data-season="1"`)
	if s3 < 0 || s1 < 0 || s3 > s1 {
		t.Fatalf("want season 3 rendered before season 1")
	}
}

func TestSeriesRename_PreviewAndOrganize(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	root := t.TempDir()
	seriesID := seedTestSeriesWithRootFolder(t, db, root)
	seriesPath, err := store.ResolveSeriesPath(t.Context(), db, seriesID)
	if err != nil {
		t.Fatalf("series path: %v", err)
	}
	var e1 int64
	if err := db.QueryRow(`SELECT id FROM episodes WHERE series_id = ?`, seriesID).Scan(&e1); err != nil {
		t.Fatalf("episode: %v", err)
	}
	old := filepath.Join(seriesPath, "S1", "Breaking.Bad.S01E01.720p.mkv")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(old, []byte("bytes"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	fileID, err := store.AttachEpisodeFile(t.Context(), db, e1, "S1/Breaking.Bad.S01E01.720p.mkv", 5)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}

	_, preview := get(t, srv, "/tv/"+itoa(seriesID)+"/rename-preview")
	want := "Season 1/Breaking Bad - S01E01 - Pilot.mkv"
	if !strings.Contains(preview, "S1/Breaking.Bad.S01E01.720p.mkv") || !strings.Contains(preview, want) || !strings.Contains(preview, "All paths are relative to") {
		t.Fatalf("want the preview to show old and new names, got:\n%s", preview)
	}
	if !strings.Contains(preview, `name="file_id" value="`+itoa(fileID)+`" checked`) {
		t.Fatalf("want the file ticked by default")
	}

	resp, _ := postForm(t, srv, "/tv/"+itoa(seriesID)+"/rename", url.Values{"file_id": {itoa(fileID)}})
	if got := resp.Header.Get("HX-Redirect"); !strings.HasSuffix(got, "?renamed=1") {
		t.Fatalf("want a redirect saying 1 file was renamed, got %q", got)
	}
	if _, err := os.Stat(filepath.Join(seriesPath, want)); err != nil {
		t.Fatalf("want the file renamed on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(seriesPath, "S1")); !os.IsNotExist(err) {
		t.Fatalf("want the emptied old folder removed")
	}
	var rel string
	if err := db.QueryRow(`SELECT relative_path FROM episode_files WHERE id = ?`, fileID).Scan(&rel); err != nil || rel != want {
		t.Fatalf("want the new path recorded, got %q %v", rel, err)
	}
	if _, preview = get(t, srv, "/tv/"+itoa(seriesID)+"/rename-preview"); !strings.Contains(preview, "already named to match") {
		t.Fatalf("want nothing left to rename, got:\n%s", preview)
	}
	if _, page := get(t, srv, "/tv/"+itoa(seriesID)+"?renamed=1"); !strings.Contains(page, "Organized: 1 file renamed.") {
		t.Fatalf("want the page to report the rename")
	}
}

func TestSeriesDelete_RemovesSeriesAndFiles(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	root := t.TempDir()
	seriesID := seedTestSeriesWithRootFolder(t, db, root)
	seriesPath, err := store.ResolveSeriesPath(t.Context(), db, seriesID)
	if err != nil {
		t.Fatalf("series path: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(seriesPath, "Season 01"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seriesPath, "Season 01", "ep.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var e1 int64
	db.QueryRow(`SELECT id FROM episodes WHERE series_id = ?`, seriesID).Scan(&e1)
	if _, err := store.AttachEpisodeFile(t.Context(), db, e1, "Season 01/ep.mkv", 1); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO grabs (series_id, release_title, indexer, protocol, download_client, status) VALUES (?, 'x', 'x', 'torrent', 'deluge', 'imported')`, seriesID); err != nil {
		t.Fatalf("grab: %v", err)
	}

	resp := deleteRequest(t, srv, "/tv/"+itoa(seriesID)+"?delete_files=on")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("HX-Redirect") != "/tv" {
		t.Fatalf("want a redirect to the TV page, got %d %q", resp.StatusCode, resp.Header.Get("HX-Redirect"))
	}
	if _, err := os.Stat(seriesPath); !os.IsNotExist(err) {
		t.Fatalf("want the series folder deleted from disk")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("want the library folder itself kept: %v", err)
	}
	for _, table := range []string{"series", "seasons", "episodes", "episode_files", "grabs"} {
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
		if n != 0 {
			t.Errorf("want %s emptied, got %d rows", table, n)
		}
	}
	if status, _ := get(t, srv, "/tv/"+itoa(seriesID)); status != http.StatusNotFound {
		t.Fatalf("want 404 after delete, got %d", status)
	}
}

// TestSeriesDelete_KeepsFilesOutsideLibraryFolders: a series whose folder was
// pointed somewhere outside every library folder is removed from UMMarr, but
// nothing on disk is deleted, and the TV page says so.
func TestSeriesDelete_KeepsFilesOutsideLibraryFolders(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seriesID := seedTestSeriesWithRootFolder(t, db, t.TempDir())
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "keep.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := db.Exec(`UPDATE series SET path = ? WHERE id = ?`, elsewhere, seriesID); err != nil {
		t.Fatalf("repoint: %v", err)
	}

	resp := deleteRequest(t, srv, "/tv/"+itoa(seriesID)+"?delete_files=on")
	if resp.Header.Get("HX-Redirect") != "/tv?notice=files-kept" {
		t.Fatalf("want a redirect saying the files were kept, got %q", resp.Header.Get("HX-Redirect"))
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "keep.mkv")); err != nil {
		t.Fatalf("want the file outside the library kept: %v", err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM series`).Scan(&n)
	if n != 0 {
		t.Fatalf("want the series removed from UMMarr")
	}
	if _, page := get(t, srv, "/tv?notice=files-kept"); !strings.Contains(page, "files were left") {
		t.Fatalf("want the TV page to explain the files were kept")
	}
}

// TestEpisodeDetails_OpensDialogOnDetails: clicking an episode opens its
// dialog on the Details tab with airing info, the overview and the file;
// the Search tab loads the search on demand.
func TestEpisodeDetails_OpensDialogOnDetails(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seriesID := seedTestSeries(t, db)
	if _, err := db.Exec(`UPDATE series_metadata SET network = 'AMC', air_time = '21:00'`); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	var episodeID int64
	if err := db.QueryRow(`SELECT id FROM episodes WHERE series_id = ?`, seriesID).Scan(&episodeID); err != nil {
		t.Fatalf("episode: %v", err)
	}
	if _, err := db.Exec(`UPDATE episodes SET air_date = '2008-01-20', overview = 'A chemistry teacher turns to crime.', runtime = 58 WHERE id = ?`, episodeID); err != nil {
		t.Fatalf("episode fields: %v", err)
	}
	fileID, err := store.AttachEpisodeFile(t.Context(), db, episodeID, "Season 1/Breaking Bad - S01E01 - Pilot.mkv", 700<<20)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := store.UpdateEpisodeFileQuality(t.Context(), db, fileID, releaseparse.Parse("Breaking.Bad.S01E01.1080p.BluRay.x264-GRP")); err != nil {
		t.Fatalf("quality: %v", err)
	}

	_, page := get(t, srv, "/tv/"+itoa(seriesID))
	if !strings.Contains(page, fmt.Sprintf(`hx-get="/tv/%d/episodes/%d"`, seriesID, episodeID)) {
		t.Fatalf("want the episode title to open its dialog")
	}
	_, body := get(t, srv, fmt.Sprintf("/tv/%d/episodes/%d", seriesID, episodeID))
	for _, want := range []string{
		"<h2>Breaking Bad - 1x01 - Pilot</h2>", `class="dialog-tab active" data-tab="details"`, `data-pane="search" hidden`,
		"<dd>20 Jan 2008 at 21:00 on AMC</dd>", "<dd>58 minutes</dd>", "A chemistry teacher turns to crime.",
		"Breaking Bad - S01E01 - Pilot.mkv", "700.0 MiB", `<span class="quality-pill">Bluray-1080p</span>`, "<td>x264</td>", "<td>GRP</td>",
		fmt.Sprintf(`hx-get="/tv/%d/episodes/%d/releases"`, seriesID, episodeID), "Search now",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("want the episode dialog to contain %q", want)
		}
	}
	if status, _ := get(t, srv, fmt.Sprintf("/tv/%d/episodes/999999", seriesID)); status != http.StatusNotFound {
		t.Errorf("want 404 for an unknown episode, got %d", status)
	}
}

func TestSeriesEpisodes_NewestFirst(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seriesID := seedTestSeries(t, db)
	var seasonID int64
	if err := db.QueryRow(`SELECT id FROM seasons WHERE series_id = ?`, seriesID).Scan(&seasonID); err != nil {
		t.Fatalf("season: %v", err)
	}
	for _, n := range []int{2, 3} {
		if _, err := store.UpsertEpisode(t.Context(), db, seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: n, Title: metadata.Field[string]{Value: "Ep " + itoa(int64(n)), Provider: "tmdb"}}); err != nil {
			t.Fatalf("episode %d: %v", n, err)
		}
	}
	_, body := get(t, srv, "/tv/"+itoa(seriesID))
	e3, e1 := strings.Index(body, ">Ep 3</a>"), strings.Index(body, ">Pilot</a>")
	if e3 < 0 || e1 < 0 || e3 > e1 {
		t.Fatalf("want episode 3 listed before episode 1")
	}
	if !strings.Contains(body, `<th data-sort="number" data-sort-dir="desc">#</th>`) {
		t.Fatalf("want the # header to start as a descending sort")
	}
}
