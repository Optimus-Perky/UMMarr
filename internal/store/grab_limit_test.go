package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestGrabWindowStart(t *testing.T) {
	now := time.Date(2026, 9, 16, 6, 30, 0, 0, time.UTC)
	for _, c := range []struct {
		resetHour int
		want      string
	}{
		{0, "2026-09-16 00:00"},  // already past today's reset
		{7, "2026-09-15 07:00"},  // today's reset hasn't come round yet
		{6, "2026-09-16 06:00"},  // reset half an hour ago
		{99, "2026-09-16 00:00"}, // nonsense falls back to midnight
	} {
		if got := store.GrabWindowStart(now, c.resetHour).Format("2006-01-02 15:04"); got != c.want {
			t.Errorf("reset hour %d: got %s, want %s", c.resetHour, got, c.want)
		}
	}
}

func TestGrabUsage(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	movieID := seedMovieTitled(t, db, "Inception", "27205", seedQualityProfile(t, db), seedRootFolder(t, db, "movie"))
	id, err := store.CreateIndexer(ctx, db, store.Indexer{Name: "IPT", Implementation: "Torznab", BaseURL: "http://ix", APIPath: "/api", Priority: store.DefaultIndexerPriority, Categories: []int{5000}, GrabLimit: 150, GrabLimitResetHour: 1})
	if err != nil {
		t.Fatal(err)
	}
	ix, err := store.GetIndexer(ctx, db, id)
	if err != nil || ix.GrabLimit != 150 || ix.GrabLimitResetHour != 1 {
		t.Fatalf("want the limit stored, got %+v (%v)", ix, err)
	}
	now := time.Now().UTC()
	since := store.GrabWindowStart(now, ix.GrabLimitResetHour)
	for _, added := range []time.Time{since.Add(-time.Hour), since.Add(time.Minute), now.Add(-time.Minute)} {
		if _, err := db.ExecContext(ctx, `INSERT INTO grabs (movie_id, release_title, indexer, indexer_id, protocol, download_client, status, added) VALUES (?, 'x', 'IPT', ?, 'torrent', 'deluge', 'grabbed', ?)`,
			movieID, id, added.Format("2006-01-02 15:04:05")); err != nil {
			t.Fatal(err)
		}
	}
	used, window, err := store.GrabUsage(ctx, db, ix, now)
	if err != nil || used != 2 || !window.Equal(since) {
		t.Fatalf("want the two grabs since the reset counted, got %d since %s (%v)", used, window, err)
	}
	ix.GrabLimit, ix.GrabLimitResetHour = 0, 0
	if err := store.UpdateIndexer(ctx, db, ix); err != nil {
		t.Fatal(err)
	}
	if again, _ := store.GetIndexer(ctx, db, id); again.GrabLimit != 0 || again.GrabLimitResetHour != 0 {
		t.Fatalf("want the limit cleared, got %+v", again)
	}
}
