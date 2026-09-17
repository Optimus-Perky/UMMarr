package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

type rpcCall struct {
	Method string `json:"method"`
	ID     int    `json:"id"`
}

// newTestDelugeServer starts a fake Deluge JSON-RPC server and returns its
// URL, to be wired in as DownloadService.BootstrapDelugeBaseURL -
// DownloadService builds its own *deluge.Client per call (see
// delugeClient in download.go), so tests no longer construct one directly.
func newTestDelugeServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

func insertTestGrab(t *testing.T, db *sql.DB, movieID int64, status string, lastChecked sql.NullTime) int64 {
	t.Helper()
	id, err := store.InsertGrab(context.Background(), db, store.Grab{
		MovieID: sql.NullInt64{Int64: movieID, Valid: true}, ReleaseTitle: "Inception 2010 1080p",
		Indexer: "SomeIndexer", Protocol: "torrent", DownloadClient: "deluge",
		DownloadClientID: sql.NullString{String: "deadbeef", Valid: true}, Status: status,
	})
	if err != nil {
		t.Fatalf("insert grab: %v", err)
	}
	if lastChecked.Valid {
		if _, err := db.ExecContext(context.Background(), `UPDATE grabs SET last_checked = ? WHERE id = ?`, lastChecked.Time, id); err != nil {
			t.Fatalf("set last_checked: %v", err)
		}
	}
	return id
}

func TestRefreshQueue_SkipsGrabsCheckedWithinGracePeriod(t *testing.T) {
	var gotTorrentsStatusCall bool
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	insertTestGrab(t, db, movieID, "downloading", sql.NullTime{Time: time.Now(), Valid: true})

	delugeURL := newTestDelugeServer(t, func(w http.ResponseWriter, r *http.Request) {
		var call rpcCall
		json.NewDecoder(r.Body).Decode(&call)
		if call.Method == "core.get_torrents_status" {
			gotTorrentsStatusCall = true
		}
		json.NewEncoder(w).Encode(map[string]any{"result": true, "error": nil, "id": call.ID})
	})

	svc := &DownloadService{DB: db, BootstrapDelugeBaseURL: delugeURL, BootstrapDelugePassword: "x", PollGracePeriod: time.Hour}
	if _, err := svc.RefreshQueue(context.Background()); err != nil {
		t.Fatalf("RefreshQueue: %v", err)
	}
	if gotTorrentsStatusCall {
		t.Fatalf("want no get_torrents_status call for a grab checked within the grace period")
	}
}

func TestRefreshQueue_PollsOverdueOrNeverCheckedGrabs(t *testing.T) {
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	neverChecked := insertTestGrab(t, db, movieID, "downloading", sql.NullTime{})
	longOverdue := insertTestGrab(t, db, movieID, "downloading", sql.NullTime{Time: time.Now().Add(-time.Hour), Valid: true})

	delugeURL := newTestDelugeServer(t, func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
			ID     int    `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&call)
		switch call.Method {
		case "auth.login":
			json.NewEncoder(w).Encode(map[string]any{"result": true, "error": nil, "id": call.ID})
		case "core.get_torrents_status":
			json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"deadbeef": map[string]any{"state": "Downloading", "is_finished": false}},
				"error":  nil, "id": call.ID,
			})
		}
	})

	svc := &DownloadService{DB: db, BootstrapDelugeBaseURL: delugeURL, BootstrapDelugePassword: "x", PollGracePeriod: time.Minute}
	if _, err := svc.RefreshQueue(context.Background()); err != nil {
		t.Fatalf("RefreshQueue: %v", err)
	}

	for _, id := range []int64{neverChecked, longOverdue} {
		grab, found, err := store.GetGrab(context.Background(), db, id)
		if err != nil || !found {
			t.Fatalf("get grab %d: found=%v err=%v", id, found, err)
		}
		if !grab.LastChecked.Valid {
			t.Fatalf("want grab %d's last_checked bumped after being polled", id)
		}
	}
}

// TestCheckWithTimeout: a stuck grab check (modeling a hung file copy -
// see checkOneGrab's doc comment for the live incident this fixes)
// mustn't block RefreshQueue's ticker goroutine forever. checkWithTimeout
// gives up waiting after timeout and reports ok=false, while the
// goroutine itself keeps running - proven here by the slow case's work
// func still sending on its (buffered) channel after checkWithTimeout has
// already returned, without a send ever blocking or leaking a panic.
func TestCheckWithTimeout(t *testing.T) {
	g := store.Grab{ID: 1, ReleaseTitle: "Test"}

	t.Run("fast work completes within the timeout", func(t *testing.T) {
		want := store.Grab{ID: 1, ReleaseTitle: "Test", Status: "imported"}
		got, ok := checkWithTimeout(g, time.Second, func() store.Grab { return want })
		if !ok || got.Status != "imported" {
			t.Fatalf("got (%+v, %v), want (%+v, true)", got, ok, want)
		}
	})

	t.Run("slow work times out instead of blocking the caller", func(t *testing.T) {
		started := make(chan struct{})
		start := time.Now()
		_, ok := checkWithTimeout(g, 20*time.Millisecond, func() store.Grab {
			close(started)
			time.Sleep(200 * time.Millisecond) // models a hung disk copy
			return g
		})
		elapsed := time.Since(start)
		<-started // the goroutine did start - this really is "slow", not "never ran"
		if ok {
			t.Fatalf("want ok=false when work outlasts the timeout")
		}
		if elapsed > 100*time.Millisecond {
			t.Fatalf("want checkWithTimeout to return around the 20ms timeout, took %s", elapsed)
		}
	})
}

// newTestGrabDeps starts a fake indexer (whose download link redirects to a
// magnet) and a fake Deluge, adds the indexer, and returns a DownloadService
// wired to both plus a release called title from that indexer.
func newTestGrabDeps(t *testing.T, db *sql.DB, title string) (*DownloadService, newznab.Release) {
	t.Helper()
	indexerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "magnet:?xt=urn:btih:deadbeef")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(indexerSrv.Close)
	delugeURL := newTestDelugeServer(t, delugeAcceptingMagnets)
	indexerID := addTestIndexer(t, db, "SomeIndexer", indexerSrv.URL, nil)
	svc := &DownloadService{
		DB: db, Indexers: &IndexerService{DB: db},
		BootstrapDelugeBaseURL: delugeURL, BootstrapDelugePassword: "x",
	}
	return svc, newznab.Release{
		GUID: "abc", Title: title, Indexer: "SomeIndexer", IndexerID: indexerID, Protocol: "torrent",
		DownloadURL: indexerSrv.URL + "/download",
	}
}

func delugeAcceptingMagnets(w http.ResponseWriter, r *http.Request) {
	var call rpcCall
	json.NewDecoder(r.Body).Decode(&call)
	switch call.Method {
	case "auth.login":
		json.NewEncoder(w).Encode(map[string]any{"result": true, "error": nil, "id": call.ID})
	case "core.add_torrent_magnet":
		json.NewEncoder(w).Encode(map[string]any{"result": "deadbeef", "error": nil, "id": call.ID})
	}
}

func TestGrabMovie_RecordsIndexerAndWhatGrabbedIt(t *testing.T) {
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	svc, release := newTestGrabDeps(t, db, "Inception 2010 1080p")

	grabID, err := svc.GrabMovie(context.Background(), movieID, release, PurposeRSS)
	if err != nil {
		t.Fatalf("grab movie: %v", err)
	}
	grab, found, err := store.GetGrab(context.Background(), db, grabID)
	if err != nil || !found {
		t.Fatalf("get grab: found=%v err=%v", found, err)
	}
	if grab.IndexerID.Int64 != release.IndexerID || grab.Indexer != "SomeIndexer" || grab.GrabbedBy != PurposeRSS || grab.DownloadClientID.String != "deadbeef" {
		t.Fatalf("want the grab to record its indexer and that RSS grabbed it, got %+v", grab)
	}
}

// TestDownloadService_DelugeSettingsTakeEffectWithoutRestart: GrabMovie must
// use Deluge settings saved via store.UpdateDownloadClientSettings (the same
// call Settings -> Download client makes), on a DownloadService instance that
// existed before they were saved.
func TestDownloadService_DelugeSettingsTakeEffectWithoutRestart(t *testing.T) {
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	svc, release := newTestGrabDeps(t, db, "Inception 2010 1080p")
	delugeURL := svc.BootstrapDelugeBaseURL
	svc.BootstrapDelugeBaseURL = ""

	if _, err := svc.GrabMovie(context.Background(), movieID, release, PurposeInteractive); err == nil {
		t.Fatalf("want an error grabbing with no Deluge configured")
	}
	if err := store.UpdateDownloadClientSettings(context.Background(), db, delugeURL, "the-password"); err != nil {
		t.Fatalf("update download client settings: %v", err)
	}
	if _, err := svc.GrabMovie(context.Background(), movieID, release, PurposeInteractive); err != nil {
		t.Fatalf("grab movie after saving settings, same DownloadService instance: %v", err)
	}
}

func TestGrab_RefusesUsenetReleases(t *testing.T) {
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	svc, release := newTestGrabDeps(t, db, "Inception 2010 1080p")
	release.Protocol = "usenet"
	if _, err := svc.GrabMovie(context.Background(), movieID, release, PurposeInteractive); err == nil || !strings.Contains(err.Error(), "no enabled usenet download client") {
		t.Fatalf("want a usenet release refused without a usenet client, got %v", err)
	}
}

// TestGrabEpisode_RecordsSeasonAndEpisodeNumber proves the per-episode
// grab (searching one individual episode or track) records exactly which
// episode was grabbed, not just the series.
func TestGrabEpisode_RecordsSeasonAndEpisodeNumber(t *testing.T) {
	db := openTestDB(t)
	seriesID := seedSeries(t, db)
	svc, release := newTestGrabDeps(t, db, "Breaking Bad S01E03")

	grabID, err := svc.GrabEpisode(context.Background(), seriesID, 1, 3, release, PurposeInteractive)
	if err != nil {
		t.Fatalf("grab episode: %v", err)
	}

	grab, found, err := store.GetGrab(context.Background(), db, grabID)
	if err != nil || !found {
		t.Fatalf("get grab: found=%v err=%v", found, err)
	}
	if !grab.SeriesID.Valid || grab.SeriesID.Int64 != seriesID {
		t.Fatalf("want series_id %d, got %+v", seriesID, grab.SeriesID)
	}
	if !grab.SeasonNumber.Valid || grab.SeasonNumber.Int64 != 1 {
		t.Fatalf("want season_number 1, got %+v", grab.SeasonNumber)
	}
	if !grab.EpisodeNumber.Valid || grab.EpisodeNumber.Int64 != 3 {
		t.Fatalf("want episode_number 3, got %+v", grab.EpisodeNumber)
	}
}

// TestGrabTrack_RecordsTrackID mirrors
// TestGrabEpisode_RecordsSeasonAndEpisodeNumber for music.
func TestGrabTrack_RecordsTrackID(t *testing.T) {
	db := openTestDB(t)
	albumID := seedAlbumWithRelease(t, db, 3)
	var trackID int64
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM tracks WHERE title = 'Track 1'`).Scan(&trackID); err != nil {
		t.Fatalf("find track 1: %v", err)
	}
	svc, release := newTestGrabDeps(t, db, "Daft Punk - Daftendirekt")

	grabID, err := svc.GrabTrack(context.Background(), albumID, trackID, release, PurposeInteractive)
	if err != nil {
		t.Fatalf("grab track: %v", err)
	}

	grab, found, err := store.GetGrab(context.Background(), db, grabID)
	if err != nil || !found {
		t.Fatalf("get grab: found=%v err=%v", found, err)
	}
	if !grab.AlbumID.Valid || grab.AlbumID.Int64 != albumID {
		t.Fatalf("want album_id %d, got %+v", albumID, grab.AlbumID)
	}
	if !grab.TrackID.Valid || grab.TrackID.Int64 != trackID {
		t.Fatalf("want track_id %d, got %+v", trackID, grab.TrackID)
	}
}

// TestRetryImport_OnlyScansItsOwnTorrentFolder is the regression test for a
// retry that swept an entire shared download root into one grab. Deluge's
// save_path is the folder torrents are saved INTO, not the torrent's own
// directory, so every other completed download sits alongside it. Here the
// unrelated torrent holds the larger video file, so a retry that scans the
// root would import that one instead.
const ownTorrentContent = "the movie we grabbed"

func TestRetryImport_OnlyScansItsOwnTorrentFolder(t *testing.T) {
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	grabID := insertTestGrab(t, db, movieID, "import_failed", sql.NullTime{})

	saveRoot := t.TempDir()
	writeDownloadFile(t, saveRoot, "Inception.2010.1080p/Inception.2010.1080p.mkv", ownTorrentContent)
	writeDownloadFile(t, saveRoot, "Unrelated.Show.S01E01/Unrelated.Show.S01E01.mkv", "a much larger file belonging to a different torrent entirely")

	delugeURL := newTestDelugeServer(t, func(w http.ResponseWriter, r *http.Request) {
		var call rpcCall
		json.NewDecoder(r.Body).Decode(&call)
		switch call.Method {
		case "auth.login":
			json.NewEncoder(w).Encode(map[string]any{"result": true, "error": nil, "id": call.ID})
		case "core.get_torrents_status":
			json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{"deadbeef": map[string]any{
					"name": "Inception.2010.1080p", "save_path": saveRoot,
					"state": "Seeding", "is_finished": true,
				}},
				"error": nil, "id": call.ID,
			})
		}
	})

	svc := &DownloadService{
		DB: db, BootstrapDelugeBaseURL: delugeURL, BootstrapDelugePassword: "x",
		Import: &ImportService{DB: db},
	}
	grab, found, err := store.GetGrab(context.Background(), db, grabID)
	if err != nil || !found {
		t.Fatalf("get grab: found=%v err=%v", found, err)
	}
	svc.RetryImport(context.Background(), grab)

	// The imported row records the RENAMED destination, so the filename
	// can't tell the two apart - the size can, and the unrelated torrent's
	// file is deliberately the larger one (importMovie picks the largest
	// video file it is handed).
	var size int64
	if err := db.QueryRow(`SELECT size FROM movie_files`).Scan(&size); err != nil {
		t.Fatalf("read movie_files (nothing imported?): %v", err)
	}
	if want := int64(len(ownTorrentContent)); size != want {
		t.Fatalf("want the grabbed torrent's own %d-byte file imported, got %d bytes - the retry reached outside its own folder", want, size)
	}
}

// TestGrab_RecordsHistory: a grab lands in history, named for its item.
func TestGrab_RecordsHistory(t *testing.T) {
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	svc, release := newTestGrabDeps(t, db, "Inception 2010 1080p")
	svc.Events = &Events{DB: db}
	if _, err := svc.GrabMovie(context.Background(), movieID, release, PurposeInteractive); err != nil {
		t.Fatal(err)
	}
	events, _, _ := store.ListHistory(context.Background(), db, store.HistoryFilter{})
	if len(events) != 1 || events[0].Event != store.EventGrabbed || !strings.Contains(events[0].Title, "Inception") || events[0].Detail != "Inception 2010 1080p" || events[0].Source != PurposeInteractive {
		t.Fatalf("want a grabbed event for Inception, got %+v", events)
	}
}
