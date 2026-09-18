package api_test

import (
	"database/sql"
	"net/url"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func seedTestArtist(t *testing.T, db *sql.DB) (artistID, albumID int64) {
	t.Helper()
	ctx := t.Context()
	root, err := store.CreateRootFolder(ctx, db, t.TempDir(), "music")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := store.CreateQualityProfile(ctx, db, "Any")
	if err != nil {
		t.Fatal(err)
	}
	metadataID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name: metadata.Field[string]{Value: "Daft Punk", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "dp-mbid"}})
	if err != nil {
		t.Fatal(err)
	}
	if artistID, err = store.UpsertArtist(ctx, db, metadataID, profile, root, true); err != nil {
		t.Fatal(err)
	}
	albumID, _, err = store.UpsertAlbum(ctx, db, metadataID, metadata.AlbumMetadata{
		Title: metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "hw-mbid"}})
	if err != nil {
		t.Fatal(err)
	}
	return artistID, albumID
}

// TestArtistPage: the artist page has the toolbar, and Edit and Delete work.
func TestArtistPage(t *testing.T) {
	db := openTestDB(t)
	artistID, albumID := seedTestArtist(t, db)
	srv := newTestServerWithDB(t, db)
	id := itoa(artistID)

	_, body := get(t, srv, "/music/artists/"+id)
	for _, want := range []string{"Daft Punk", "Refresh &amp; Scan", "Fix Match", ">Edit<", ">Delete<", "Metadata", "Homework", "/music/albums/" + itoa(albumID)} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q on the artist page, got:\n%s", want, body)
		}
	}
	_, body = get(t, srv, "/music")
	if !strings.Contains(body, `href="/music/artists/`+id+`"`) || !strings.Contains(body, "music-editor") {
		t.Fatalf("want the Music page linking to the artist and offering the editor, got:\n%s", body)
	}

	// Monitoring toggles.
	resp, body := postForm(t, srv, "/music/artists/"+id+"/monitored", url.Values{"monitored": {"false"}})
	if resp.StatusCode != 200 || !strings.Contains(body, "Unmonitored") {
		t.Fatalf("want the artist unmonitored, got %d:\n%s", resp.StatusCode, body)
	}
	if a, _, _ := store.GetArtistDetail(t.Context(), db, artistID); a.Monitored {
		t.Errorf("want monitored=false saved")
	}

	// Edit: monitored again, and apply it to the albums.
	_, body = get(t, srv, "/music/artists/"+id+"/edit")
	if !strings.Contains(body, `name="quality_profile_id"`) {
		t.Fatalf("want the edit dialog, got:\n%s", body)
	}
	postForm(t, srv, "/music/artists/"+id+"/edit", url.Values{"monitored": {"on"}, "monitor_albums": {"on"}, "path": {"/music/Daft Punk"}})
	a, _, _ := store.GetArtistDetail(t.Context(), db, artistID)
	if !a.Monitored || a.Path.String != "/music/Daft Punk" {
		t.Fatalf("want the edit saved, got %+v", a)
	}

	// The mass editor unmonitors it again.
	postForm(t, srv, "/music/editor", url.Values{"id": {id}, "monitored": {"no"}})
	if a, _, _ = store.GetArtistDetail(t.Context(), db, artistID); a.Monitored {
		t.Errorf("want the mass editor to unmonitor the artist")
	}

	// Delete takes its albums with it.
	if resp, body = postForm(t, srv, "/music/editor/delete", url.Values{"id": {id}}); resp.Header.Get("HX-Redirect") == "" {
		t.Fatalf("want the artist deleted, got:\n%s", body)
	}
	if _, found, _ := store.GetArtistDetail(t.Context(), db, artistID); found {
		t.Errorf("want the artist gone")
	}
	var albums int
	db.QueryRow(`SELECT COUNT(*) FROM albums`).Scan(&albums)
	if albums != 0 {
		t.Errorf("want its albums gone too, got %d", albums)
	}
}

// seedTestTrack gives an album one release with one track, which is what
// the album page needs before it renders (and polls) any track rows.
func seedTestTrack(t *testing.T, db *sql.DB, albumID int64) int64 {
	t.Helper()
	ctx := t.Context()
	var artistMetadataID int64
	if err := db.QueryRow(`SELECT artist_metadata_id FROM albums WHERE id = ?`, albumID).Scan(&artistMetadataID); err != nil {
		t.Fatal(err)
	}
	releaseID, err := store.UpsertAlbumRelease(ctx, db, albumID, metadata.ReleaseMetadata{
		Title: metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "hw-release"}})
	if err != nil {
		t.Fatal(err)
	}
	trackID, err := store.UpsertTrack(ctx, db, releaseID, artistMetadataID, metadata.TrackSource{Number: "1", Title: "Daftendirekt", MediumNumber: 1})
	if err != nil {
		t.Fatal(err)
	}
	return trackID
}
