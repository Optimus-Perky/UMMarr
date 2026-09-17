package api_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// TestHistoryPage: events show newest first with their item linked, and the
// filters narrow them.
func TestHistoryPage(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	movieID := seedTestMovie(t, db)
	store.RecordEvent(t.Context(), db, store.Event{Event: store.EventGrabbed, MediaType: "movie", MovieID: sql.NullInt64{Int64: movieID, Valid: true}, Title: "Inception (2010)", Detail: "Inception.2010.1080p", Source: "interactive"})
	store.RecordEvent(t.Context(), db, store.Event{Event: store.EventUpgraded, MediaType: "movie", MovieID: sql.NullInt64{Int64: movieID, Valid: true}, Title: "Inception (2010)", Detail: "Inception.2010.2160p", Quality: "Remux-2160p"})

	_, body := get(t, srv, "/history")
	for _, want := range []string{`href="/history"`, "Upgraded", "Grabbed", `href="/movies/` + itoa(movieID) + `"`, "Remux-2160p", "Inception.2010.1080p"} {
		if !strings.Contains(body, want) {
			t.Fatalf("want %q on the History page, got:\n%s", want, body)
		}
	}
	if strings.Index(body, "Upgraded") > strings.Index(body, "Grabbed") {
		t.Fatalf("want newest first")
	}
	_, body = get(t, srv, "/history?event=grabbed")
	if !strings.Contains(body, "Inception.2010.1080p") || strings.Contains(body, "Inception.2010.2160p") {
		t.Fatalf("want the event filter applied, got:\n%s", body)
	}
}
