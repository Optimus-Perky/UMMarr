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

// The artist and album pages poll for row updates, the way the series page
// does: each row comes back as an htmx out-of-band swap, so a grab landing
// updates the row in place instead of reloading the page and closing an
// open Find release row.
func TestMusicPages_PollRowsOutOfBand(t *testing.T) {
	db := openTestDB(t)
	artistID, albumID := seedTestArtist(t, db)
	srv := newTestServerWithDB(t, db)

	_, body := get(t, srv, "/music/artists/"+itoa(artistID))
	if !strings.Contains(body, `hx-get="/music/artists/`+itoa(artistID)+`/albums/status"`) {
		t.Error("want the artist page polling its album rows")
	}
	status, rows := get(t, srv, "/music/artists/"+itoa(artistID)+"/albums/status")
	if status != 200 {
		t.Fatalf("album status = %d", status)
	}
	if !strings.Contains(rows, `id="artist-album-row-`+itoa(albumID)+`"`) || !strings.Contains(rows, `hx-swap-oob="true"`) {
		t.Errorf("want the album row as an out-of-band swap, got:\n%s", rows)
	}

	// The album page only polls once it has tracks to update, so give it
	// one.
	trackID := seedTestTrack(t, db, albumID)
	_, body = get(t, srv, "/music/albums/"+itoa(albumID))
	if !strings.Contains(body, `hx-get="/music/albums/`+itoa(albumID)+`/tracks/status"`) {
		t.Error("want the album page polling its track rows")
	}
	status, rows = get(t, srv, "/music/albums/"+itoa(albumID)+"/tracks/status")
	if status != 200 {
		t.Fatalf("track status = %d: %s", status, rows)
	}
	if !strings.Contains(rows, `id="track-row-`+itoa(trackID)+`"`) || !strings.Contains(rows, `hx-swap-oob="true"`) {
		t.Errorf("want the track row as an out-of-band swap, got:\n%s", rows)
	}
}

func TestAlbumManageTracks_ListsFilesAndRemaps(t *testing.T) {
	db := openTestDB(t)
	_, albumID := seedTestArtist(t, db)
	srv := newTestServerWithDB(t, db)
	id := itoa(albumID)

	_, body := get(t, srv, "/music/albums/"+id)
	if !strings.Contains(body, `hx-get="/music/albums/`+id+`/manage-tracks"`) || !strings.Contains(body, "Manage Track Files") {
		t.Error("want Manage Track Files on the album page")
	}
	status, body := get(t, srv, "/music/albums/"+id+"/manage-tracks")
	if status != 200 {
		t.Fatalf("manage tracks = %d", status)
	}
	// A seeded album has no files, and the dialog should say so rather
	// than rendering an empty table with a Delete button.
	if !strings.Contains(body, "no files yet") {
		t.Errorf("want the empty case explained, got:\n%s", body)
	}
}

// Album Pass is Season Pass for music: every artist with a chip per album,
// clickable to monitor, plus a bar that applies a monitor option to the
// ticked artists.
func TestAlbumPass_ListsAlbumsAndTogglesOne(t *testing.T) {
	db := openTestDB(t)
	artistID, albumID := seedTestArtist(t, db)
	srv := newTestServerWithDB(t, db)

	_, body := get(t, srv, "/music")
	if !strings.Contains(body, `href="/music/albumpass"`) {
		t.Error("want Album Pass linked from the Music page")
	}
	status, body := get(t, srv, "/music/albumpass")
	if status != 200 {
		t.Fatalf("album pass = %d", status)
	}
	if !strings.Contains(body, "Daft Punk") || !strings.Contains(body, "Homework") {
		t.Errorf("want the artist and its album listed, got:\n%s", body)
	}

	monitored := func() bool {
		var m bool
		if err := db.QueryRow(`SELECT monitored FROM albums WHERE id = ?`, albumID).Scan(&m); err != nil {
			t.Fatalf("read album: %v", err)
		}
		return m
	}
	resp, chip := postForm(t, srv, "/music/albumpass/toggle?artist="+itoa(artistID)+"&album="+itoa(albumID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("toggle = %d: %s", resp.StatusCode, chip)
	}
	if monitored() {
		t.Error("want the album unmonitored after one click")
	}
	if !strings.Contains(chip, "click to monitor") {
		t.Errorf("want the chip re-rendered in its new state, got: %s", chip)
	}
}

func TestAlbumPassSave_AppliesAMonitorOptionToTickedArtists(t *testing.T) {
	db := openTestDB(t)
	artistID, albumID := seedTestArtist(t, db)
	srv := newTestServerWithDB(t, db)

	resp, body := postForm(t, srv, "/music/albumpass", url.Values{"id": {itoa(artistID)}, "monitor": {"none"}})
	if resp.StatusCode != 200 {
		t.Fatalf("save = %d: %s", resp.StatusCode, body)
	}
	var monitored bool
	if err := db.QueryRow(`SELECT monitored FROM albums WHERE id = ?`, albumID).Scan(&monitored); err != nil {
		t.Fatalf("read album: %v", err)
	}
	if monitored {
		t.Error("want None applied to the ticked artist's albums")
	}
	if _, body := postForm(t, srv, "/music/albumpass", url.Values{"monitor": {"all"}}); !strings.Contains(body, "Tick at least one artist") {
		t.Errorf("want an empty selection refused, got: %s", body)
	}
}
