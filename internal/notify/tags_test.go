package notify

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// A notification tagged "kids" hears about kids-tagged items only; an
// untagged one hears about everything; a health issue reaches both.
func TestOnEvent_TagsNarrowItemEvents(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	root, _ := store.CreateRootFolder(ctx, db, t.TempDir(), "movie")
	profile, _ := store.CreateQualityProfile(ctx, db, "Any")
	addMovie := func(title string, tags ...string) int64 {
		metaID, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
			Title: metadata.Field[string]{Value: title, Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": title}})
		if err != nil {
			t.Fatal(err)
		}
		id, err := store.UpsertMovie(ctx, db, metaID, profile, root, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(tags) > 0 {
			_ = store.EditMovies(ctx, db, []int64{id}, store.LibraryEdit{TagMode: "replace", Tags: tags})
		}
		return id
	}
	heat := addMovie("Heat")
	frozen := addMovie("Frozen", "kids")
	kids, _ := store.EnsureTags(ctx, db, []string{"kids"})

	var kidsHits, allHits atomic.Int32
	hook := func(counter *atomic.Int32) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { counter.Add(1) }))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	for _, n := range []store.Notification{
		{Name: "Kids", Implementation: store.NotifyWebhook, Enabled: true, OnImport: true, OnHealth: true, Tags: kids, Settings: map[string]string{"url": hook(&kidsHits)}},
		{Name: "All", Implementation: store.NotifyWebhook, Enabled: true, OnImport: true, OnHealth: true, Settings: map[string]string{"url": hook(&allHits)}},
	} {
		if _, err := store.CreateNotification(ctx, db, n); err != nil {
			t.Fatal(err)
		}
	}
	s := &Service{DB: db, Sync: true}
	imported := func(id int64) store.Event {
		return store.Event{Event: store.EventImported, MediaType: "movie", MovieID: sql.NullInt64{Int64: id, Valid: true}, Title: "x"}
	}
	s.OnEvent(ctx, imported(heat))
	if kidsHits.Load() != 0 || allHits.Load() != 1 {
		t.Fatalf("an untagged movie: want only All (kids %d, all %d)", kidsHits.Load(), allHits.Load())
	}
	s.OnEvent(ctx, imported(frozen))
	if kidsHits.Load() != 1 || allHits.Load() != 2 {
		t.Fatalf("a kids movie: want both (kids %d, all %d)", kidsHits.Load(), allHits.Load())
	}
	s.OnEvent(ctx, store.Event{Event: store.EventHealth, Title: "Health issue"})
	if kidsHits.Load() != 2 || allHits.Load() != 3 {
		t.Fatalf("a health issue: want both (kids %d, all %d)", kidsHits.Load(), allHits.Load())
	}
}
