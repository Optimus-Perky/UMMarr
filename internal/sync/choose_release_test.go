package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/musicbrainz"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Choosing a release did nothing at all for any album that had files:
// the chosen release was synced, then dropped again by the tidy-up that
// removes releases nothing is attached to, leaving the old one - which
// still held every file - in place. Blur's The Ballad of Darren stayed on
// the Canadian release however many times it was told not to.
func TestChooseRelease_MovesTheFilesAcross(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release/uk-release":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "uk-release", "title": "Homework", "country": "GB",
				"media": []map[string]any{{"position": 1, "format": "CD", "tracks": []map[string]any{
					{"id": "uk-t1", "number": "1", "title": "One"},
					{"id": "uk-t2", "number": "2", "title": "Two"},
				}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	db := openTestDB(t)
	ctx := context.Background()
	_, albumID, _ := importedAlbum(t, db)

	before, err := store.AlbumAttachedFiles(ctx, db, albumID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 2 {
		t.Fatalf("want the seeded album to have two files, got %d", len(before))
	}
	oldReleaseID := before[0].ReleaseID

	client, err := musicbrainz.New(musicbrainz.Options{UserAgent: "test/1.0", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	svc := &MusicService{DB: db, MusicBrainz: client}
	if err := svc.ChooseRelease(ctx, albumID, "uk-release"); err != nil {
		t.Fatalf("choose release: %v", err)
	}

	after, err := store.AlbumAttachedFiles(ctx, db, albumID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 {
		t.Fatalf("want both files still attached, got %d", len(after))
	}
	for _, f := range after {
		if f.ReleaseID == oldReleaseID {
			t.Errorf("%s is still on the release that was replaced", f.RelativePath)
		}
	}
	// The album is left on the chosen release alone, not on both.
	var releases int
	if err := db.QueryRow(`SELECT COUNT(*) FROM album_releases WHERE album_id = ?`, albumID).Scan(&releases); err != nil {
		t.Fatal(err)
	}
	if releases != 1 {
		t.Errorf("want one release left, got %d", releases)
	}
	// And it is the one asked for.
	var mbid string
	if err := db.QueryRow(`SELECT external_id FROM external_ids e JOIN album_releases r ON r.id = e.entity_id
		WHERE e.entity_type = 'release' AND e.provider = 'musicbrainz' AND r.album_id = ?`, albumID).Scan(&mbid); err != nil {
		t.Fatal(err)
	}
	if mbid != "uk-release" {
		t.Errorf("album is on release %q", mbid)
	}
}

// A shorter release has no home for every file. Those files keep their
// rows and turn up in Manage Track Files rather than being attached to a
// guess or quietly deleted.
func TestChooseRelease_ShorterReleaseStrandsTheExtras(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/release/single-track" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "single-track", "title": "Homework",
			"media": []map[string]any{{"position": 1, "tracks": []map[string]any{
				{"id": "only-t1", "number": "1", "title": "One"},
			}}}})
	}))
	t.Cleanup(srv.Close)

	db := openTestDB(t)
	ctx := context.Background()
	_, albumID, _ := importedAlbum(t, db)
	client, _ := musicbrainz.New(musicbrainz.Options{UserAgent: "test/1.0", BaseURL: srv.URL})
	svc := &MusicService{DB: db, MusicBrainz: client}
	if err := svc.ChooseRelease(ctx, albumID, "single-track"); err != nil {
		t.Fatalf("choose release: %v", err)
	}

	attached, err := store.AlbumAttachedFiles(ctx, db, albumID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attached) != 1 {
		t.Fatalf("want one file on the one track, got %d", len(attached))
	}
	// The second file is untouched on disk and offered for matching by
	// hand, rather than deleted or attached to a guess.
	imports := &ImportService{DB: db}
	loose, err := imports.UnmatchedAlbumFiles(ctx, albumID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loose) != 1 {
		t.Fatalf("want one file waiting to be matched, got %d", len(loose))
	}
	// And only one release is left, so the picker is not left showing the
	// one that was replaced.
	var releases int
	if err := db.QueryRow(`SELECT COUNT(*) FROM album_releases WHERE album_id = ?`, albumID).Scan(&releases); err != nil {
		t.Fatal(err)
	}
	if releases != 1 {
		t.Errorf("want the replaced release dropped, %d left", releases)
	}
}
