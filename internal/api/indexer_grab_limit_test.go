package api_test

import (
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// TestIndexerSettings_GrabLimit: an indexer carries Prowlarr-style Grab
// Limit and the hour the tracker's own counter resets, so UMMarr stops
// grabbing before the tracker starts refusing (IPTorrents: 150 a day).
func TestIndexerSettings_GrabLimit(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()

	_, form := get(t, srv, "/settings/indexers/new?implementation=Torznab")
	for _, want := range []string{`name="grab_limit"`, `name="grab_limit_reset_hour"`, "Grab Limit", "IPTorrents allows 150"} {
		if !strings.Contains(form, want) {
			t.Errorf("want %q on the indexer form", want)
		}
	}

	values := validIndexerForm()
	values.Set("grab_limit", "150")
	values.Set("grab_limit_reset_hour", "1")
	if resp, body := postForm(t, srv, "/settings/indexers", values); resp.Header.Get("HX-Redirect") != "/settings/indexers" {
		t.Fatalf("want the indexer saved, got:\n%s", body)
	}
	list, _ := store.ListIndexers(ctx, db)
	if len(list) != 1 || list[0].GrabLimit != 150 || list[0].GrabLimitResetHour != 1 {
		t.Fatalf("want the limit saved, got %+v", list)
	}

	_, page := get(t, srv, "/settings/indexers")
	if !strings.Contains(page, "Grab limit: 150/day") {
		t.Error("want the limit on the indexer card")
	}
	_, edit := get(t, srv, "/settings/indexers/"+itoa(list[0].ID)+"/edit")
	if !strings.Contains(edit, `name="grab_limit" value="150"`) || !strings.Contains(edit, `name="grab_limit_reset_hour" value="1"`) {
		t.Error("want the saved limit back in the edit form")
	}

	bad := validIndexerForm()
	bad.Set("name", "Other")
	bad.Set("grab_limit", "-5")
	bad.Set("grab_limit_reset_hour", "24")
	_, body := postForm(t, srv, "/settings/indexers", bad)
	if !strings.Contains(body, "Enter a whole number, or 0 for no limit.") || !strings.Contains(body, "Enter the hour of day in UTC, 0 to 23.") {
		t.Errorf("want both limit fields validated, got:\n%s", body)
	}
	if again, _ := store.ListIndexers(ctx, db); len(again) != 1 {
		t.Fatal("want nothing saved from the invalid form")
	}
}
