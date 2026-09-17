package api_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestScanReport_ListsIgnoresAndDismisses(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()
	movieRoot, err := store.CreateRootFolder(ctx, db, "/media/movies", "movie")
	if err != nil {
		t.Fatal(err)
	}
	musicRoot, _ := store.CreateRootFolder(ctx, db, "/media/music", "music")
	store.RecordUnmatchedFolder(ctx, db, store.UnmatchedFolder{RootFolderID: movieRoot, Kind: "movie", Path: "/media/movies/Some Film (2019)", Name: "Some Film (2019)"})
	store.RecordUnmatchedFolder(ctx, db, store.UnmatchedFolder{RootFolderID: movieRoot, Kind: "movie", Path: "/media/movies/Twice", Name: "Twice", Reason: store.UnmatchedDuplicate, Detail: "already in the library as /media/movies/Twice (2001)"})
	store.RecordUnmatchedFolder(ctx, db, store.UnmatchedFolder{RootFolderID: musicRoot, Kind: "music", Path: "/media/music/Band/Album", Name: "Band / Album"})

	_, body := get(t, srv, "/library/scan-report")
	for _, want := range []string{
		"<h2>Scan report</h2>", "Some Film (2019)", "/media/movies/Some Film (2019)", "No match", "Already in the library",
		"already in the library as /media/movies/Twice (2001)", "Band / Album", "<td>Artist</td>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q on the scan report", want)
		}
	}
	// Only the unmatched film can be matched: the duplicate is already in the
	// library, and an album folder needs its artist first.
	if matches := strings.Count(body, "/match\" hx-target=\"#release-dialog-body\""); matches != 1 {
		t.Errorf("want Match on the film only, got %d", matches)
	}

	var filmID, albumID int64
	db.QueryRow(`SELECT id FROM unmatched_folders WHERE name = 'Some Film (2019)'`).Scan(&filmID)
	db.QueryRow(`SELECT id FROM unmatched_folders WHERE name = 'Band / Album'`).Scan(&albumID)

	resp, _ := postForm(t, srv, "/library/scan-report/"+itoa(albumID)+"/ignore", url.Values{})
	if resp.Header.Get("HX-Redirect") != "/library/scan-report" {
		t.Fatalf("want a redirect after ignoring, got %q", resp.Header.Get("HX-Redirect"))
	}
	_, body = get(t, srv, "/library/scan-report")
	if strings.Contains(body, "Band / Album") {
		t.Error("want the ignored folder hidden")
	}
	_, body = get(t, srv, "/library/scan-report?ignored=1")
	if !strings.Contains(body, "Band / Album") || !strings.Contains(body, "Unignore") {
		t.Error("want Show ignored to bring it back with an Unignore button")
	}

	resp, _ = postForm(t, srv, "/library/scan-report/"+itoa(filmID)+"/dismiss", url.Values{})
	if resp.Header.Get("HX-Redirect") != "/library/scan-report?dismissed=1" {
		t.Fatalf("want a redirect after dismissing, got %q", resp.Header.Get("HX-Redirect"))
	}
	_, body = get(t, srv, "/library/scan-report?dismissed=1")
	if strings.Contains(body, "Some Film (2019)") || !strings.Contains(body, "Dismissed.") {
		t.Error("want the dismissed folder gone and the notice shown")
	}
	// A later scan that still can't match it puts it back.
	store.RecordUnmatchedFolder(ctx, db, store.UnmatchedFolder{RootFolderID: movieRoot, Kind: "movie", Path: "/media/movies/Some Film (2019)", Name: "Some Film (2019)"})
	if _, body := get(t, srv, "/library/scan-report"); !strings.Contains(body, "Some Film (2019)") {
		t.Error("want a still-unmatched folder back on the report")
	}
	if status, _ := get(t, srv, "/library/scan-report/9999/match"); status != http.StatusNotFound {
		t.Errorf("want a missing row to 404, got %d", status)
	}
}

func TestScanReport_MatchDialogAndApply(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()
	root, _ := store.CreateRootFolder(ctx, db, "/media/movies", "movie")
	store.RecordUnmatchedFolder(ctx, db, store.UnmatchedFolder{RootFolderID: root, Kind: "movie", Path: "/media/movies/Some Film (2019)", Name: "Some Film (2019)"})
	var id int64
	db.QueryRow(`SELECT id FROM unmatched_folders`).Scan(&id)

	_, body := get(t, srv, "/library/scan-report/"+itoa(id)+"/match")
	for _, want := range []string{"Match folder - Some Film (2019)", "/media/movies/Some Film (2019)", `value="Some Film"`, "TMDB isn"} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q in the match dialog, got:\n%s", want, body)
		}
	}
	// Applying without picking anything says so, and applying with no
	// quality profile explains that too.
	_, body = postForm(t, srv, "/library/scan-report/"+itoa(id)+"/match", url.Values{})
	if !strings.Contains(body, "Pick an entry first.") {
		t.Errorf("want a reminder to pick an entry, got:\n%s", body)
	}
	_, body = postForm(t, srv, "/library/scan-report/"+itoa(id)+"/match", url.Values{"id": {"27205"}})
	if !strings.Contains(body, "quality profile") {
		t.Errorf("want the missing quality profile explained, got:\n%s", body)
	}
	if left, _ := store.ListUnmatchedFolders(ctx, db, false); len(left) != 1 {
		t.Fatal("want the folder still on the report after a failed match")
	}
}
