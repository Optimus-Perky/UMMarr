package api_test

import (
	"net/url"
	"strings"
	"testing"
)

// The artist and album pages now carry the same management actions the
// series page has: Organize & Rename, monitoring, Edit and Delete. These
// check the buttons are on the page, the dialogs they open render, and the
// two that change data actually change it.

func TestArtistPage_HasRenameAndMonitoringActions(t *testing.T) {
	db := openTestDB(t)
	artistID, _ := seedTestArtist(t, db)
	srv := newTestServerWithDB(t, db)
	id := itoa(artistID)

	_, body := get(t, srv, "/music/artists/"+id)
	for _, want := range []string{
		`hx-get="/music/artists/` + id + `/rename-preview"`,
		`hx-get="/music/artists/` + id + `/monitor"`,
		">Preview Rename<", ">Album Monitoring<",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q on the artist page", want)
		}
	}

	// The rename dialog renders, and says there's nothing to do rather
	// than erroring on a library with no files.
	status, body := get(t, srv, "/music/artists/"+id+"/rename-preview")
	if status != 200 {
		t.Fatalf("rename preview = %d", status)
	}
	if !strings.Contains(body, "Organize &amp; Rename") || !strings.Contains(body, `hx-post="/music/artists/`+id+`/rename"`) {
		t.Errorf("want the rename dialog posting back to the artist, got:\n%s", body)
	}

	// The monitoring dialog offers Lidarr's options and applies one.
	status, body = get(t, srv, "/music/artists/"+id+"/monitor")
	if status != 200 || !strings.Contains(body, "All Albums") || !strings.Contains(body, "Latest Album") {
		t.Fatalf("want the album monitoring options, got %d:\n%s", status, body)
	}
}

func TestArtistMonitorApply_ChangesAlbumMonitoring(t *testing.T) {
	db := openTestDB(t)
	artistID, albumID := seedTestArtist(t, db)
	srv := newTestServerWithDB(t, db)

	monitored := func() bool {
		var m bool
		if err := db.QueryRow(`SELECT monitored FROM albums WHERE id = ?`, albumID).Scan(&m); err != nil {
			t.Fatalf("read album: %v", err)
		}
		return m
	}
	if !monitored() {
		t.Fatal("want the seeded album monitored to start with")
	}
	resp, body := postForm(t, srv, "/music/artists/"+itoa(artistID)+"/monitor", url.Values{"monitor": {"none"}})
	if resp.StatusCode != 200 {
		t.Fatalf("apply = %d: %s", resp.StatusCode, body)
	}
	if monitored() {
		t.Error("want None to unmonitor the artist's albums")
	}
	if _, _ = postForm(t, srv, "/music/artists/"+itoa(artistID)+"/monitor", url.Values{"monitor": {"all"}}); !monitored() {
		t.Error("want All Albums to monitor them again")
	}
}

func TestAlbumPage_HasEditRenameAndDelete(t *testing.T) {
	db := openTestDB(t)
	_, albumID := seedTestArtist(t, db)
	srv := newTestServerWithDB(t, db)
	id := itoa(albumID)

	_, body := get(t, srv, "/music/albums/"+id)
	for _, want := range []string{
		`hx-get="/music/albums/` + id + `/rename-preview"`,
		`hx-get="/music/albums/` + id + `/edit"`,
		`hx-get="/music/albums/` + id + `/delete"`,
		">Preview Rename<", ">Edit<", ">Delete<",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q on the album page", want)
		}
	}

	status, body := get(t, srv, "/music/albums/"+id+"/edit")
	if status != 200 || !strings.Contains(body, `name="monitored"`) {
		t.Fatalf("want the album edit dialog, got %d:\n%s", status, body)
	}
	status, body = get(t, srv, "/music/albums/"+id+"/delete")
	if status != 200 || !strings.Contains(body, `name="delete_files"`) {
		t.Fatalf("want the album delete dialog, got %d:\n%s", status, body)
	}
}

func TestAlbumEditSave_TogglesMonitoring(t *testing.T) {
	db := openTestDB(t)
	_, albumID := seedTestArtist(t, db)
	srv := newTestServerWithDB(t, db)

	// An unticked checkbox sends nothing, which is how the form says
	// "unmonitored".
	resp, body := postForm(t, srv, "/music/albums/"+itoa(albumID)+"/edit", url.Values{})
	if resp.StatusCode != 200 {
		t.Fatalf("save = %d: %s", resp.StatusCode, body)
	}
	var monitored bool
	if err := db.QueryRow(`SELECT monitored FROM albums WHERE id = ?`, albumID).Scan(&monitored); err != nil {
		t.Fatalf("read album: %v", err)
	}
	if monitored {
		t.Error("want the album unmonitored after saving with the box unticked")
	}
}

func TestAlbumDelete_RemovesTheAlbumAndReturnsToTheArtist(t *testing.T) {
	db := openTestDB(t)
	artistID, albumID := seedTestArtist(t, db)
	srv := newTestServerWithDB(t, db)

	resp, body := deleteForm(t, srv, "/music/albums/"+itoa(albumID), url.Values{})
	if resp.StatusCode != 200 {
		t.Fatalf("delete = %d: %s", resp.StatusCode, body)
	}
	if want := "/music/artists/" + itoa(artistID); resp.Header.Get("HX-Redirect") != want {
		t.Errorf("want a redirect back to %s, got %q", want, resp.Header.Get("HX-Redirect"))
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM albums WHERE id = ?`, albumID).Scan(&count); err != nil {
		t.Fatalf("count albums: %v", err)
	}
	if count != 0 {
		t.Error("want the album row gone")
	}
	var artists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM artists WHERE id = ?`, artistID).Scan(&artists); err != nil {
		t.Fatalf("count artists: %v", err)
	}
	if artists != 1 {
		t.Error("want the artist kept when only an album is deleted")
	}
}
