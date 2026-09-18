package sync

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
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
	_, items, _, err := svc.ArtistRenamePreview(ctx, artistID)
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

	_, items, _, err = svc.ArtistRenamePreview(ctx, artistID)
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

	_, items, _, err := svc.AlbumRenamePreview(ctx, albumID)
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
	if _, items, _, err := svc.ArtistRenamePreview(ctx, artistID); err != nil || len(items) != 0 {
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

// The rule Mark set after seeing the first preview: two editions of one
// album (a vinyl rip and a download card, say) each keep their own file.
// A subfolder the templates can't describe is left alone and reported for
// re-matching, and even if something did aim two files at one name, the
// rename refuses rather than overwriting - os.Rename would destroy one
// silently.
func TestArtistRenamePreview_LeavesSubfoldersAloneAndNeverOverwrites(t *testing.T) {
	db := openTestDB(t)
	artistID, albumID, artistPath := importedAlbum(t, db)
	ctx := context.Background()
	svc := &ImportService{DB: db}
	albumPath, _ := store.AlbumFolderPath(ctx, db, albumID)

	// Put one file in an edition subfolder, as a hand-managed library has.
	refs, _ := store.ListTrackFilesForAlbum(ctx, db, albumID)
	if len(refs) != 2 {
		t.Fatalf("want two seeded files, got %d", len(refs))
	}
	edition := filepath.Join(albumPath, "12 Vinyl 01")
	if err := os.MkdirAll(edition, 0o755); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join("12 Vinyl 01", filepath.Base(refs[0].RelativePath))
	if err := os.Rename(filepath.Join(albumPath, refs[0].RelativePath), filepath.Join(albumPath, moved)); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTrackFilePath(ctx, db, refs[0].ID, moved); err != nil {
		t.Fatal(err)
	}

	_, items, skipped, err := svc.ArtistRenamePreview(ctx, artistID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	for _, item := range items {
		if strings.Contains(item.Current, "12 Vinyl 01") {
			t.Errorf("want the subfolder file left out of the rename rows, got %+v", item)
		}
	}
	if len(skipped) != 1 || skipped[0].Files != 1 || !strings.Contains(skipped[0].Folder, "12 Vinyl 01") {
		t.Fatalf("want the subfolder reported once for re-matching, got %+v", skipped)
	}

	// Renaming everything on offer must leave both files on disk.
	var ids []int64
	for _, item := range items {
		ids = append(ids, item.FileID)
	}
	if _, problems, err := svc.RenameArtistFiles(ctx, artistID, ids); err != nil || len(problems) != 0 {
		t.Fatalf("rename: %v %v", problems, err)
	}
	if _, err := os.Stat(filepath.Join(albumPath, moved)); err != nil {
		t.Errorf("want the edition's file untouched: %v", err)
	}
	refs, _ = store.ListTrackFilesForAlbum(ctx, db, albumID)
	if len(refs) != 2 {
		t.Fatalf("want both files still tracked, got %d", len(refs))
	}

	// A destination that already exists is refused, not overwritten.
	artistPathClean := filepath.Clean(artistPath)
	occupied := RenameItem{FileID: refs[1].ID, FileIDs: []int64{refs[1].ID},
		Current: mustRel(t, artistPathClean, filepath.Join(albumPath, refs[1].RelativePath)),
		New:     mustRel(t, artistPathClean, filepath.Join(albumPath, moved))}
	renamed, problems, err := svc.renameTrackFiles(ctx, artistPathClean, []RenameItem{occupied}, []int64{refs[1].ID})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if renamed != 0 || len(problems) != 1 || !strings.Contains(problems[0], "already exists") {
		t.Fatalf("want the overwrite refused and reported, got renamed=%d problems=%v", renamed, problems)
	}
	for _, name := range []string{moved, refs[1].RelativePath} {
		if _, err := os.Stat(filepath.Join(albumPath, name)); err != nil {
			t.Errorf("want %s still on disk: %v", name, err)
		}
	}
}

func mustRel(t *testing.T, base, path string) string {
	t.Helper()
	rel, err := filepath.Rel(base, path)
	if err != nil {
		t.Fatalf("rel %s %s: %v", base, path, err)
	}
	return rel
}

// The other half of Mark's rule: a file a scan couldn't identify is offered
// for manual matching, and matching it by hand attaches it where it lies.
func TestUnmatchedAlbumFiles_ListsAndAttachesByHand(t *testing.T) {
	db := openTestDB(t)
	_, albumID, _ := importedAlbum(t, db)
	ctx := context.Background()
	svc := &ImportService{DB: db, Events: &Events{DB: db}}
	albumPath, _ := store.AlbumFolderPath(ctx, db, albumID)

	// Nothing loose to begin with.
	loose, err := svc.UnmatchedAlbumFiles(ctx, albumID)
	if err != nil || len(loose) != 0 {
		t.Fatalf("want no unmatched files in a fully imported album, got %+v (%v)", loose, err)
	}

	// Detach one file's row, as a scan that refused to guess would leave it.
	refs, _ := store.ListTrackFilesForAlbum(ctx, db, albumID)
	orphan := refs[0]
	if _, err := db.ExecContext(ctx, `UPDATE tracks SET track_file_id = NULL WHERE track_file_id = ?`, orphan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM track_files WHERE id = ?`, orphan.ID); err != nil {
		t.Fatal(err)
	}

	loose, err = svc.UnmatchedAlbumFiles(ctx, albumID)
	if err != nil || len(loose) != 1 || loose[0].Path != orphan.RelativePath {
		t.Fatalf("want the detached file offered for matching, got %+v (%v)", loose, err)
	}

	// Match it to a track that has no file.
	_, tracks, _ := store.FindImportRelease(ctx, db, albumID)
	var free int64
	for _, tr := range tracks {
		if !tr.HasFile {
			free = tr.ID
		}
	}
	if free == 0 {
		t.Fatal("want a track with no file to match against")
	}
	if err := svc.AttachAlbumFile(ctx, albumID, free, orphan.RelativePath); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if loose, _ := svc.UnmatchedAlbumFiles(ctx, albumID); len(loose) != 0 {
		t.Errorf("want nothing left unmatched, got %+v", loose)
	}
	if _, err := os.Stat(filepath.Join(albumPath, orphan.RelativePath)); err != nil {
		t.Errorf("want the file left where it was: %v", err)
	}
	// A file that isn't actually loose can't be attached.
	if err := svc.AttachAlbumFile(ctx, albumID, free, "nothing.flac"); err == nil {
		t.Error("want an unknown file refused")
	}
}

// Re-match from tags exists to undo what positional matching got wrong, and
// the worst of that is two files on each other's tracks. Fixing a swap means
// detaching both before attaching either, or the second move lands on a
// track that still holds the first file.
func TestRematch_FixesASwapAndRefusesAHalfOne(t *testing.T) {
	db := openTestDB(t)
	_, albumID, _ := importedAlbum(t, db)
	ctx := context.Background()
	albumPath, _ := store.AlbumFolderPath(ctx, db, albumID)

	files, err := store.ListTrackFileDetails(ctx, db, albumID)
	if err != nil || len(files) != 2 {
		t.Fatalf("want two attached files, got %+v (%v)", files, err)
	}
	first, second := files[0], files[1]

	// Tag each file with the OTHER file's track number: they are swapped.
	tags := map[string]mediainfo.AudioTags{
		first.RelativePath:  {TrackNumber: trackNumberOf(t, db, second.TrackID), DiscNumber: 1},
		second.RelativePath: {TrackNumber: trackNumberOf(t, db, first.TrackID), DiscNumber: 1},
	}
	svc := &ImportService{DB: db, Events: &Events{DB: db}, Probe: func(_ context.Context, path string) (mediainfo.Info, error) {
		for name, tag := range tags {
			if path == filepath.Join(albumPath, name) {
				t := tag
				return mediainfo.Info{Schema: mediainfo.Schema, Tags: &t}, nil
			}
		}
		return mediainfo.Info{Schema: mediainfo.Schema}, nil
	}}

	report, err := svc.RematchPreview(ctx, albumID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(report.Changes) != 2 || report.Confirmed != 0 {
		t.Fatalf("want both files reported as misplaced, got %+v", report)
	}
	for _, c := range report.Changes {
		if !c.Occupied {
			t.Errorf("want %s to say its track is occupied by the other file", c.Path)
		}
	}

	// Moving only one of a swapped pair has nowhere to go, and must refuse
	// rather than detach a file and leave it hanging.
	if _, err := svc.ApplyRematch(ctx, albumID, []int64{first.ID}); err == nil {
		t.Error("want half a swap refused")
	}
	after, _ := store.ListTrackFileDetails(ctx, db, albumID)
	if len(after) != 2 {
		t.Fatalf("want both files still attached after the refusal, got %d", len(after))
	}

	// Both together works.
	moved, err := svc.ApplyRematch(ctx, albumID, []int64{first.ID, second.ID})
	if err != nil || moved != 2 {
		t.Fatalf("apply: moved %d: %v", moved, err)
	}
	after, _ = store.ListTrackFileDetails(ctx, db, albumID)
	if len(after) != 2 {
		t.Fatalf("want both files still attached, got %d", len(after))
	}
	for _, f := range after {
		switch f.RelativePath {
		case first.RelativePath:
			if f.TrackID != second.TrackID {
				t.Errorf("%s is on track %d, want %d", f.RelativePath, f.TrackID, second.TrackID)
			}
		case second.RelativePath:
			if f.TrackID != first.TrackID {
				t.Errorf("%s is on track %d, want %d", f.RelativePath, f.TrackID, first.TrackID)
			}
		}
	}
	// A second preview has nothing left to say.
	if report, err := svc.RematchPreview(ctx, albumID); err != nil || len(report.Changes) != 0 || report.Confirmed != 2 {
		t.Errorf("want both confirmed in place afterwards, got %+v (%v)", report, err)
	}
}

// A file whose tags say nothing keeps the track it has: re-matching is for
// replacing guesses with facts, not for making new guesses.
func TestRematch_LeavesUnreadableFilesWhereTheyAre(t *testing.T) {
	db := openTestDB(t)
	_, albumID, _ := importedAlbum(t, db)
	ctx := context.Background()
	svc := &ImportService{DB: db, Events: &Events{DB: db}, Probe: func(context.Context, string) (mediainfo.Info, error) {
		return mediainfo.Info{Schema: mediainfo.Schema}, nil // no tags at all
	}}

	before, _ := store.ListTrackFileDetails(ctx, db, albumID)
	report, err := svc.RematchPreview(ctx, albumID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(report.Changes) != 0 || len(report.Unreadable) != len(before) {
		t.Fatalf("want every file reported as unreadable and nothing changed, got %+v", report)
	}
	after, _ := store.ListTrackFileDetails(ctx, db, albumID)
	for i := range before {
		if before[i].TrackID != after[i].TrackID {
			t.Errorf("%s moved", before[i].RelativePath)
		}
	}
}

func trackNumberOf(t *testing.T, db *sql.DB, trackID int64) int {
	t.Helper()
	var number int
	if err := db.QueryRow(`SELECT CAST(track_number AS INTEGER) FROM tracks WHERE id = ?`, trackID).Scan(&number); err != nil {
		t.Fatalf("track number of %d: %v", trackID, err)
	}
	return number
}
