package sync

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// A grab is refused once the indexer's Grab Limit for the day is used up,
// before UMMarr asks the tracker for anything.
func TestGrabLimit_StopsGrabsUntilTheReset(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	movieID := seedMovie(t, db)
	indexerID, err := store.CreateIndexer(ctx, db, store.Indexer{Name: "IPTorrents", Implementation: "Torznab", BaseURL: "http://ix", APIPath: "/api", Priority: store.DefaultIndexerPriority, Categories: []int{2000}, GrabLimit: 2, GrabLimitResetHour: 1})
	if err != nil {
		t.Fatal(err)
	}
	svc := &DownloadService{DB: db}
	release := newznab.Release{Title: "Some Movie 1080p", Indexer: "IPTorrents", IndexerID: indexerID, Protocol: newznab.ProtocolTorrent, MagnetURL: "magnet:?xt=urn:btih:abc"}

	// Under the limit the grab gets as far as looking for a download client.
	if _, err := svc.GrabMovie(ctx, movieID, release, PurposeInteractive); err == nil || strings.Contains(err.Error(), "grab limit") {
		t.Fatalf("want the grab attempted while under the limit, got %v", err)
	}
	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO grabs (movie_id, release_title, indexer, indexer_id, protocol, download_client, status, added) VALUES (?, 'x', 'IPTorrents', ?, 'torrent', 'deluge', 'grabbed', ?)`,
			movieID, indexerID, now.Format("2006-01-02 15:04:05")); err != nil {
			t.Fatal(err)
		}
	}
	_, err = svc.GrabMovie(ctx, movieID, release, PurposeInteractive)
	if err == nil || !strings.Contains(err.Error(), "IPTorrents's grab limit of 2 is used up (2 grabbed since") || !strings.Contains(err.Error(), "resets at 01:00 UTC") {
		t.Fatalf("want the limit refusal naming the indexer and reset, got %v", err)
	}

	// Grabs from before the reset don't count, and no limit means no check.
	if _, err := db.ExecContext(ctx, `UPDATE grabs SET added = ?`, store.GrabWindowStart(now, 1).Add(-time.Hour).Format("2006-01-02 15:04:05")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GrabMovie(ctx, movieID, release, PurposeInteractive); err == nil || strings.Contains(err.Error(), "grab limit") {
		t.Fatalf("want grabs before the reset ignored, got %v", err)
	}
	ix, _ := store.GetIndexer(ctx, db, indexerID)
	ix.GrabLimit = 0
	store.UpdateIndexer(ctx, db, ix)
	if _, err := db.ExecContext(ctx, `UPDATE grabs SET added = ?`, now.Format("2006-01-02 15:04:05")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GrabMovie(ctx, movieID, release, PurposeInteractive); err == nil || strings.Contains(err.Error(), "grab limit") {
		t.Fatalf("want no limit to mean no check, got %v", err)
	}
}
