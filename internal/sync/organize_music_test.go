package sync

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Organize & Rename for music has a level series doesn't: a track file's
// stored path is relative to its ALBUM folder, and the album folder itself
// can be renamed. These tests cover both - a file renamed inside its
// folder, and the whole album folder moving - and check the database
// agrees with the disk afterwards, since a rename that isn't recorded
// leaves the library pointing at files that no longer exist.

// importedAlbum seeds an album with files on disk, deliberately named
// wrongly, and returns the artist row id, album id and artist folder.
func importedAlbum(t *testing.T, db *sql.DB) (artistID, albumID int64, artistPath string) {
	t.Helper()
	ctx := context.Background()
	albumID = seedAlbumWithRelease(t, db, 2)
	if err := db.QueryRow(`SELECT ar.id FROM albums al JOIN artists ar ON ar.artist_metadata_id = al.artist_metadata_id WHERE al.id = ?`, albumID).Scan(&artistID); err != nil {
		t.Fatalf("find artist row: %v", err)
	}
	downloadDir := t.TempDir()
	writeDownloadFile(t, downloadDir, "aaa.flac", "a")
	writeDownloadFile(t, downloadDir, "bbb.flac", "b")
	svc := &ImportService{DB: db}
	status, message := svc.Import(ctx, store.Grab{AlbumID: sql.NullInt64{Int64: albumID, Valid: true}},
		[]importer.File{{Path: "aaa.flac", Size: 1}, {Path: "bbb.flac", Size: 1}}, downloadDir)
	if status != "imported" {
		t.Fatalf("seed import: %s (%s)", status, message)
	}
	artistPath, err := store.ArtistFolderPath(ctx, db, artistID)
	if err != nil {
		t.Fatalf("artist folder: %v", err)
	}
	return artistID, albumID, artistPath
}

func TestArtistRenamePreview_ListsOnlyMisnamedFiles(t *testing.T) {
	db := openTestDB(t)
	artistID, _, _ := importedAlbum(t, db)
	svc := &ImportService{DB: db}
	ctx := context.Background()

	// Files land already named by the templates, so nothing to organize.
	_, items, err := svc.ArtistRenamePreview(ctx, artistID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("want no rows for a correctly named library, got %+v", items)
	}

	// Rename one on disk behind UMMarr's back, as a hand-managed library
	// would be, and it should show up.
	artistPath, _ := store.ArtistFolderPath(ctx, db, artistID)
	refs, err := store.ListTrackFilesForAlbum(ctx, db, mustAlbumOf(t, db, artistID))
	if err != nil || len(refs) == 0 {
		t.Fatalf("track files: %v %+v", err, refs)
	}
	albumPath, _ := store.AlbumFolderPath(ctx, db, mustAlbumOf(t, db, artistID))
	if err := os.Rename(filepath.Join(albumPath, refs[0].RelativePath), filepath.Join(albumPath, "wrong name.flac")); err != nil {
		t.Fatalf("rename on disk: %v", err)
	}
	if err := store.UpdateTrackFilePath(ctx, db, refs[0].ID, "wrong name.flac"); err != nil {
		t.Fatalf("record wrong name: %v", err)
	}

	_, items, err = svc.ArtistRenamePreview(ctx, artistID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(items) != 1 || !strings.Contains(items[0].Current, "wrong name.flac") {
		t.Fatalf("want the misnamed file listed once, got %+v", items)
	}
	if strings.Contains(items[0].New, "wrong name") {
		t.Errorf("want the new name to follow the template, got %q", items[0].New)
	}

	renamed, problems, err := svc.RenameArtistFiles(ctx, artistID, []int64{items[0].FileID})
	if err != nil || renamed != 1 || len(problems) != 0 {
		t.Fatalf("rename: %d %v %v", renamed, problems, err)
	}
	if _, err := os.Stat(filepath.Join(artistPath, items[0].New)); err != nil {
		t.Fatalf("want the renamed file on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(albumPath, "wrong name.flac")); !os.IsNotExist(err) {
		t.Error("want the old name gone from disk")
	}
	// The recorded path must match where the file actually is, or the
	// library silently points at nothing.
	refs, _ = store.ListTrackFilesForAlbum(ctx, db, mustAlbumOf(t, db, artistID))
	for _, ref := range refs {
		if _, err := os.Stat(filepath.Join(albumPath, ref.RelativePath)); err != nil {
			t.Errorf("recorded path %q doesn't exist on disk: %v", ref.RelativePath, err)
		}
	}
}

// Renaming the album's own folder is the case series has no equivalent of:
// every track file moves, and albums.path has to follow, or the next scan
// looks in the old folder.
func TestRenameAlbumFiles_MovesTheAlbumFolderAndRecordsIt(t *testing.T) {
	db := openTestDB(t)
	artistID, albumID, artistPath := importedAlbum(t, db)
	ctx := context.Background()
	svc := &ImportService{DB: db}

	oldFolder, err := store.AlbumFolderPath(ctx, db, albumID)
	if err != nil {
		t.Fatalf("album folder: %v", err)
	}
	// Change the album title so the folder template resolves elsewhere.
	if _, err := db.ExecContext(ctx, `UPDATE albums SET title = 'Discovery' WHERE id = ?`, albumID); err != nil {
		t.Fatalf("retitle: %v", err)
	}

	_, items, err := svc.AlbumRenamePreview(ctx, albumID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want both track files listed as moving, got %+v", items)
	}
	var ids []int64
	for _, item := range items {
		if !strings.Contains(item.New, "Discovery") {
			t.Errorf("want the new path inside the retitled album folder, got %q", item.New)
		}
		ids = append(ids, item.FileID)
	}

	renamed, problems, err := svc.RenameAlbumFiles(ctx, albumID, ids)
	if err != nil || renamed != 2 || len(problems) != 0 {
		t.Fatalf("rename: %d %v %v", renamed, problems, err)
	}
	newFolder, err := store.AlbumFolderPath(ctx, db, albumID)
	if err != nil {
		t.Fatalf("album folder after: %v", err)
	}
	if newFolder == oldFolder {
		t.Fatalf("want albums.path updated to the new folder, still %q", newFolder)
	}
	if _, err := os.Stat(oldFolder); !os.IsNotExist(err) {
		t.Error("want the emptied old album folder removed")
	}
	refs, _ := store.ListTrackFilesForAlbum(ctx, db, albumID)
	if len(refs) != 2 {
		t.Fatalf("want two track files, got %d", len(refs))
	}
	for _, ref := range refs {
		if _, err := os.Stat(filepath.Join(newFolder, ref.RelativePath)); err != nil {
			t.Errorf("recorded path %q isn't in the new folder: %v", ref.RelativePath, err)
		}
	}
	// And the artist preview is clean again afterwards.
	if _, items, err := svc.ArtistRenamePreview(ctx, artistID); err != nil || len(items) != 0 {
		t.Errorf("want nothing left to organize, got %+v (%v)", items, err)
	}
	_ = artistPath
}

func mustAlbumOf(t *testing.T, db *sql.DB, artistID int64) int64 {
	t.Helper()
	var albumID int64
	if err := db.QueryRow(`SELECT al.id FROM albums al JOIN artists ar ON ar.artist_metadata_id = al.artist_metadata_id WHERE ar.id = ?`, artistID).Scan(&albumID); err != nil {
		t.Fatalf("find album: %v", err)
	}
	return albumID
}
