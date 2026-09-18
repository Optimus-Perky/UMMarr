package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// A hand-managed music library keeps its covers beside the tracks, and
// MusicBrainz supplies no images at all - so without this every album in
// a 951-album library showed a blank poster while folder.jpg sat right
// there.
func TestImportArtwork(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_, albumID, artistPath := importedAlbum(t, db)
	svc := &ImportService{DB: db}
	albumPath, err := store.AlbumFolderPath(ctx, db, albumID)
	if err != nil {
		t.Fatal(err)
	}

	// Nothing to find yet.
	report, err := svc.ImportArtwork(ctx)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if report.Albums != 0 || report.Missing != 1 {
		t.Fatalf("want the album counted as having no artwork, got %+v", report)
	}

	// A Kodi-style folder: several images, and the preference order has to
	// decide rather than whatever the directory lists first.
	for _, name := range []string{"banner.jpg", "folder.jpg", "discart.png", "cover.jpg", "fanart.jpg"} {
		if err := os.WriteFile(filepath.Join(albumPath, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(artistPath, "poster.jpg"), []byte("artist"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err = svc.ImportArtwork(ctx)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if report.Albums != 1 || report.Artists != 1 || report.Missing != 0 {
		t.Fatalf("want one album cover and one artist image, got %+v", report)
	}
	file, err := store.CoverFile(ctx, db, "album", albumID)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(file) != "cover.jpg" {
		t.Errorf("want cover.jpg preferred over folder.jpg and the rest, got %q", file)
	}

	// The album's poster now points at the file, so the pages show it.
	album, found, err := store.GetAlbumDetail(ctx, db, albumID)
	if err != nil || !found {
		t.Fatalf("album: %v", err)
	}
	if album.PosterURL == "" || !filepath.IsAbs("/"+album.PosterURL) {
		t.Errorf("want the album poster served from the library, got %q", album.PosterURL)
	}

	// Running again changes nothing, so a scheduled run doesn't keep
	// reporting work it didn't do.
	report, err = svc.ImportArtwork(ctx)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if report.Albums != 0 || report.Artists != 0 {
		t.Errorf("want nothing re-imported, got %+v", report)
	}

	// Removing the best one falls back to the next.
	if err := os.Remove(filepath.Join(albumPath, "cover.jpg")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportArtwork(ctx); err != nil {
		t.Fatal(err)
	}
	if file, _ := store.CoverFile(ctx, db, "album", albumID); filepath.Base(file) != "folder.jpg" {
		t.Errorf("want folder.jpg used once cover.jpg is gone, got %q", file)
	}
}
