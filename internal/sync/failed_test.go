package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

const failedHash = "0123456789abcdef0123456789abcdef01234567"

// fakeFailingDeluge reports failedHash in Deluge's Error state and counts
// core.remove_torrent calls.
type fakeFailingDeluge struct {
	mu      gosync.Mutex
	removed []any
}

func (f *fakeFailingDeluge) handle(w http.ResponseWriter, r *http.Request) {
	var call struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
		ID     int    `json:"id"`
	}
	json.NewDecoder(r.Body).Decode(&call)
	reply := func(result any) {
		json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil, "id": call.ID})
	}
	switch call.Method {
	case "auth.login":
		reply(true)
	case "core.get_torrents_status":
		reply(map[string]any{failedHash: map[string]any{"name": "Inception.2010.1080p", "state": "Error", "message": "Tracker gave HTTP 404", "is_finished": false}})
	case "core.remove_torrent":
		f.mu.Lock()
		f.removed = append(f.removed, call.Params)
		f.mu.Unlock()
		reply(true)
	default:
		reply(true)
	}
}

func (f *fakeFailingDeluge) removals() []any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]any(nil), f.removed...)
}

// failedGrabSetup saves a Deluge download client and a downloading grab on it.
func failedGrabSetup(t *testing.T, removeFailed, redownload bool) (*DownloadService, *fakeFailingDeluge, store.Grab, chan store.Grab) {
	t.Helper()
	ctx := context.Background()
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	fake := &fakeFailingDeluge{}
	url := newTestDelugeServer(t, fake.handle)
	clientID, err := store.CreateDownloadClient(ctx, db, store.DownloadClient{Name: "Deluge", Implementation: store.ClientDeluge, Enabled: true, Priority: 1,
		BaseURL: url, Password: "x", RemoveFailed: removeFailed})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := store.UpdateDownloadHandling(ctx, db, store.DownloadHandling{RedownloadFailed: redownload}); err != nil {
		t.Fatalf("download handling: %v", err)
	}
	g := store.Grab{
		MovieID: sql.NullInt64{Int64: movieID, Valid: true}, ReleaseTitle: "Inception 2010 1080p BluRay x264",
		Indexer: "Tracker", Protocol: "torrent", DownloadClient: store.ClientDeluge, Size: sql.NullInt64{Int64: 8 << 30, Valid: true},
		DownloadClientID: sql.NullString{String: failedHash, Valid: true}, DownloadClientRef: sql.NullInt64{Int64: clientID, Valid: true},
		Status: "downloading",
	}
	if g.ID, err = store.InsertGrab(ctx, db, g); err != nil {
		t.Fatalf("insert grab: %v", err)
	}
	searched := make(chan store.Grab, 4)
	svc := &DownloadService{DB: db, Redownload: func(_ context.Context, g store.Grab) { searched <- g }}
	return svc, fake, g, searched
}

func blocklistRows(t *testing.T, db *sql.DB) []store.BlocklistEntry {
	t.Helper()
	rows, err := store.ListAllBlocklist(context.Background(), db)
	if err != nil {
		t.Fatalf("list blocklist: %v", err)
	}
	return rows
}

func waitSearched(searched chan store.Grab) bool {
	select {
	case <-searched:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}

func TestDownloadFailed_BlocklistsRemovesAndSearchesAgain(t *testing.T) {
	svc, fake, g, searched := failedGrabSetup(t, true, true)
	ctx := context.Background()

	got := svc.RefreshGrab(ctx, g)
	if got.Status != "failed" || got.StatusMessage.String != "Tracker gave HTTP 404" {
		t.Fatalf("want the grab failed with Deluge's message, got %q %q", got.Status, got.StatusMessage.String)
	}
	rows := blocklistRows(t, svc.DB)
	if len(rows) != 1 {
		t.Fatalf("want one blocklist entry, got %d", len(rows))
	}
	b := rows[0]
	if b.SourceTitle != g.ReleaseTitle || b.MovieID != g.MovieID || b.InfoHash.String != failedHash || b.Indexer != "Tracker" ||
		b.Message != "Tracker gave HTTP 404" || b.Quality == "" || b.Size.Int64 != 8<<30 {
		t.Errorf("blocklist entry doesn't describe the release: %+v", b)
	}
	if n := len(fake.removals()); n != 1 {
		t.Errorf("want Remove Failed to delete the torrent once, got %d calls", n)
	}
	if !waitSearched(searched) {
		t.Fatalf("want Redownload to search for another release")
	}

	// The next poll sees the same failure; nothing happens twice.
	svc.RefreshGrab(ctx, got)
	if n := len(blocklistRows(t, svc.DB)); n != 1 {
		t.Errorf("want still one blocklist entry, got %d", n)
	}
	if n := len(fake.removals()); n != 1 {
		t.Errorf("want no second removal, got %d", n)
	}
	if waitSearched(searched) {
		t.Errorf("want no second search")
	}
}

func TestDownloadFailed_SettingsOffOnlyBlocklists(t *testing.T) {
	svc, fake, g, searched := failedGrabSetup(t, false, false)
	if got := svc.RefreshGrab(context.Background(), g); got.Status != "failed" {
		t.Fatalf("want failed, got %q", got.Status)
	}
	if n := len(blocklistRows(t, svc.DB)); n != 1 {
		t.Errorf("want the release blocklisted, got %d entries", n)
	}
	if n := len(fake.removals()); n != 0 {
		t.Errorf("want nothing removed from the client with Remove Failed off, got %d", n)
	}
	if waitSearched(searched) {
		t.Errorf("want no search with Redownload off")
	}
}

func TestRemoveGrab(t *testing.T) {
	ctx := context.Background()

	t.Run("remove without blocklisting", func(t *testing.T) {
		svc, fake, g, searched := failedGrabSetup(t, false, false)
		got, err := svc.RemoveGrab(ctx, g, true, BlocklistNone)
		if err != nil || got.Status != "removed" {
			t.Fatalf("want removed, got %q %v", got.Status, err)
		}
		if n := len(fake.removals()); n != 1 {
			t.Errorf("want the torrent removed from Deluge, got %d calls", n)
		}
		if n := len(blocklistRows(t, svc.DB)); n != 0 {
			t.Errorf("want nothing blocklisted, got %d", n)
		}
		if waitSearched(searched) {
			t.Errorf("want no search")
		}
	})

	t.Run("blocklist only", func(t *testing.T) {
		svc, fake, g, searched := failedGrabSetup(t, false, true)
		got, err := svc.RemoveGrab(ctx, g, false, BlocklistOnly)
		if err != nil || got.Status != "failed" {
			t.Fatalf("want failed, got %q %v", got.Status, err)
		}
		if rows := blocklistRows(t, svc.DB); len(rows) != 1 || rows[0].Message != "Manually marked as failed" {
			t.Errorf("want one manual blocklist entry, got %+v", rows)
		}
		if n := len(fake.removals()); n != 0 {
			t.Errorf("want the client left alone, got %d calls", n)
		}
		if waitSearched(searched) {
			t.Errorf("want Blocklist Only not to search, even with Redownload on")
		}
	})

	t.Run("blocklist and search", func(t *testing.T) {
		svc, _, g, searched := failedGrabSetup(t, false, false)
		if _, err := svc.RemoveGrab(ctx, g, false, BlocklistAndSearch); err != nil {
			t.Fatal(err)
		}
		if !waitSearched(searched) {
			t.Errorf("want Blocklist and Search to search, even with Redownload off")
		}
	})
}

func TestGrab_RejectsBlocklistedHash(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	svc, release := newTestGrabDeps(t, db, "Inception 2010 1080p")
	// The fake indexer redirects to magnet:?xt=urn:btih:deadbeef, which isn't
	// a real 40-character hash; give the release one the way an indexer would.
	release.InfoHash = failedHash
	if _, err := store.AddBlocklist(ctx, db, store.BlocklistEntry{MovieID: sql.NullInt64{Int64: movieID, Valid: true},
		SourceTitle: "Inception.2010.RENAMED", Protocol: "torrent", InfoHash: sql.NullString{String: strings.ToUpper(failedHash), Valid: true}}); err != nil {
		t.Fatal(err)
	}

	// Off: the hash doesn't matter.
	if _, err := svc.GrabMovie(ctx, movieID, release, PurposeInteractive); err != nil {
		t.Fatalf("want the grab allowed while the indexer doesn't reject blocklisted hashes, got %v", err)
	}

	ix, err := store.GetIndexer(ctx, db, release.IndexerID)
	if err != nil {
		t.Fatal(err)
	}
	ix.RejectBlocklisted = true
	if err := store.UpdateIndexer(ctx, db, ix); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GrabMovie(ctx, movieID, release, PurposeInteractive); !errors.Is(err, ErrReleaseBlocklisted) {
		t.Fatalf("want ErrReleaseBlocklisted, got %v", err)
	}
}

func TestGrab_RecordsPublishDateAndHash(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	movieID := seedMovie(t, db)
	svc, release := newTestGrabDeps(t, db, "Inception 2010 1080p")
	release.InfoHash = failedHash
	release.PublishDate = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	id, err := svc.GrabMovie(ctx, movieID, release, PurposeInteractive)
	if err != nil {
		t.Fatal(err)
	}
	g, _, err := store.GetGrab(ctx, db, id)
	if err != nil {
		t.Fatal(err)
	}
	if g.InfoHash.String != failedHash || !g.Published.Valid || !g.Published.Time.Equal(release.PublishDate) {
		t.Errorf("want the hash and publish date kept for a later blocklist entry, got %q %v", g.InfoHash.String, g.Published)
	}
}
