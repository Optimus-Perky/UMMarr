package store_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestHistory(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, e := range []store.Event{
		{Event: store.EventGrabbed, MediaType: "movie", MovieID: sql.NullInt64{Int64: 1, Valid: true}, Title: "Inception (2010)", Detail: "Inception 2010 1080p", Source: "rss"},
		{Event: store.EventImported, MediaType: "movie", MovieID: sql.NullInt64{Int64: 1, Valid: true}, Title: "Inception (2010)", Detail: "Inception (2010).mkv", Quality: "Bluray-1080p"},
		{Event: store.EventAdded, MediaType: "series", SeriesID: sql.NullInt64{Int64: 2, Valid: true}, Title: "Breaking Bad"},
	} {
		if _, err := store.RecordEvent(ctx, db, e); err != nil {
			t.Fatal(err)
		}
	}
	all, total, err := store.ListHistory(ctx, db, store.HistoryFilter{})
	if err != nil || total != 3 || len(all) != 3 || all[0].Event != store.EventAdded {
		t.Fatalf("want 3 events newest first, got %d %+v (%v)", total, all, err)
	}
	movies, total, _ := store.ListHistory(ctx, db, store.HistoryFilter{MediaType: "movie", Event: store.EventImported})
	if total != 1 || movies[0].Quality != "Bluray-1080p" {
		t.Fatalf("want the one movie import, got %+v", movies)
	}
	found, total, _ := store.ListHistory(ctx, db, store.HistoryFilter{Search: "breaking"})
	if total != 1 || found[0].SeriesID.Int64 != 2 {
		t.Fatalf("want the search to find the series event, got %+v", found)
	}
	page, total, _ := store.ListHistory(ctx, db, store.HistoryFilter{Limit: 2, Offset: 2})
	if total != 3 || len(page) != 1 || page[0].Event != store.EventGrabbed {
		t.Fatalf("want paging to reach the oldest event, got %+v", page)
	}
}
