package sync

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Organize & Rename for music, the counterpart of organize.go's series
// version (see RenamePreview there). Music has one extra level: a track
// file's stored path is relative to its ALBUM folder, and the album folder
// itself can be renamed too, so a preview row's Current/New are relative to
// the artist folder and a row can move a file between album folders.

// ArtistRenamePreview lists every track file of an artist whose location
// doesn't match the naming templates. Files already named correctly are
// left out.
func (s *ImportService) ArtistRenamePreview(ctx context.Context, artistID int64) (artistPath string, items []RenameItem, skipped []RenameSkip, err error) {
	artistPath, err = store.ArtistFolderPath(ctx, s.DB, artistID)
	if err != nil {
		return "", nil, nil, err
	}
	var metadataID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT artist_metadata_id FROM artists WHERE id = ?`, artistID).Scan(&metadataID); err != nil {
		return "", nil, nil, fmt.Errorf("find artist %d: %w", artistID, err)
	}
	albums, err := store.ListAlbumsForArtist(ctx, s.DB, metadataID)
	if err != nil {
		return "", nil, nil, err
	}
	for _, album := range albums {
		albumItems, err := s.albumRenameItems(ctx, artistPath, album.ID)
		if err != nil {
			continue // one unreadable album shouldn't hide the rest
		}
		items = append(items, albumItems...)
		if albumSkips, err := s.albumRenameSkips(ctx, artistPath, album.ID, album.Title); err == nil {
			skipped = append(skipped, albumSkips...)
		}
	}
	return artistPath, items, skipped, nil
}

// AlbumRenamePreview is the same for one album.
func (s *ImportService) AlbumRenamePreview(ctx context.Context, albumID int64) (artistPath string, items []RenameItem, skipped []RenameSkip, err error) {
	var artistID int64
	var title string
	if err := s.DB.QueryRowContext(ctx, `SELECT ar.id, al.title FROM albums al JOIN artists ar ON ar.artist_metadata_id = al.artist_metadata_id WHERE al.id = ?`, albumID).Scan(&artistID, &title); err != nil {
		return "", nil, nil, fmt.Errorf("find the artist of album %d: %w", albumID, err)
	}
	artistPath, err = store.ArtistFolderPath(ctx, s.DB, artistID)
	if err != nil {
		return "", nil, nil, err
	}
	items, err = s.albumRenameItems(ctx, artistPath, albumID)
	if err != nil {
		return artistPath, nil, nil, err
	}
	skipped, _ = s.albumRenameSkips(ctx, artistPath, albumID, title)
	return artistPath, items, skipped, nil
}

// RenameSkip is a folder Organize deliberately left alone: files sitting in
// a subfolder of the album folder, which the naming templates have no way
// to express. Those are usually a second edition or a numbered disc ("CD
// 01", "12 Vinyl 01", "Download Card 02"), and flattening them into the
// album folder would put two different recordings of the same track on one
// path - so the file is left where it is and listed here to be re-matched.
type RenameSkip struct {
	AlbumID int64
	Album   string
	Folder  string // relative to the artist folder
	Files   int
}

// albumRenameItems is one album's rows, with paths relative to artistPath.
func (s *ImportService) albumRenameItems(ctx context.Context, artistPath string, albumID int64) ([]RenameItem, error) {
	current, err := store.AlbumFolderPath(ctx, s.DB, albumID)
	if err != nil {
		return nil, err
	}
	wanted, err := store.ResolveAlbumPath(ctx, s.DB, albumID)
	if err != nil {
		return nil, err
	}
	refs, err := store.ListTrackFilesForAlbum(ctx, s.DB, albumID)
	if err != nil {
		return nil, err
	}
	var items []RenameItem
	for _, ref := range refs {
		// A file in a subfolder of the album folder stays put: see
		// RenameSkip. albumRenameSkips reports those separately.
		if filepath.Dir(ref.RelativePath) != "." {
			continue
		}
		name, err := store.ResolveTrackFileName(ctx, s.DB, ref.OwnerID, ref.RelativePath)
		if err != nil {
			continue
		}
		currentPath, wantedPath := filepath.Join(current, ref.RelativePath), filepath.Join(wanted, name)
		if currentPath == wantedPath {
			continue
		}
		currentRel, err := filepath.Rel(artistPath, currentPath)
		if err != nil {
			continue
		}
		wantedRel, err := filepath.Rel(artistPath, wantedPath)
		if err != nil || currentRel == wantedRel {
			continue
		}
		items = append(items, RenameItem{FileID: ref.ID, FileIDs: []int64{ref.ID}, Current: currentRel, New: wantedRel})
	}
	return items, nil
}

// RenameArtistFiles renames the chosen track files to what
// ArtistRenamePreview shows, records their new paths, and moves each album
// folder that the templates now name differently. Empty folders left behind
// are removed. It reports how many were renamed and what went wrong.
func (s *ImportService) RenameArtistFiles(ctx context.Context, artistID int64, fileIDs []int64) (renamed int, problems []string, err error) {
	artistPath, items, _, err := s.ArtistRenamePreview(ctx, artistID)
	if err != nil {
		return 0, nil, err
	}
	return s.renameTrackFiles(ctx, artistPath, items, fileIDs)
}

// RenameAlbumFiles is RenameArtistFiles for one album.
func (s *ImportService) RenameAlbumFiles(ctx context.Context, albumID int64, fileIDs []int64) (renamed int, problems []string, err error) {
	artistPath, items, _, err := s.AlbumRenamePreview(ctx, albumID)
	if err != nil {
		return 0, nil, err
	}
	return s.renameTrackFiles(ctx, artistPath, items, fileIDs)
}

func (s *ImportService) renameTrackFiles(ctx context.Context, artistPath string, items []RenameItem, fileIDs []int64) (renamed int, problems []string, err error) {
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return 0, nil, err
	}
	perms := permissions(ms)
	chosen := map[int64]bool{}
	for _, id := range fileIDs {
		chosen[id] = true
	}
	touchedAlbums := map[int64]bool{}
	claimed := map[string]string{} // destination -> the file already heading there
	for _, item := range items {
		if !chosen[item.FileID] {
			continue
		}
		src, dest := filepath.Join(artistPath, item.Current), filepath.Join(artistPath, item.New)
		// Nothing here may destroy a file. RenameFileIfDifferent ends in
		// os.Rename, which overwrites an existing destination silently, so
		// a name two files both resolve to has to be refused rather than
		// applied - whether the other file is already on disk or is another
		// row of this same run.
		if other, taken := claimed[dest]; taken {
			problems = append(problems, fmt.Sprintf("%s: %s wants that name too, so both were left alone", item.Current, other))
			continue
		}
		if _, err := os.Lstat(dest); err == nil {
			problems = append(problems, fmt.Sprintf("%s: %s already exists, so it was left alone", item.Current, item.New))
			continue
		}
		claimed[dest] = item.Current
		if err := importer.RenameFileIfDifferent(src, dest, perms); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", item.Current, err))
			continue
		}
		albumID, err := s.albumOfTrackFile(ctx, item.FileID)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: renamed, but couldn't find its album: %v", item.Current, err))
			continue
		}
		// The stored path is relative to the album folder, which this
		// rename may itself have moved, so record it against the folder the
		// file actually landed in.
		if err := store.UpdateTrackFilePath(ctx, s.DB, item.FileID, filepath.Base(item.New)); err != nil {
			problems = append(problems, fmt.Sprintf("%s: renamed, but couldn't record it: %v", item.Current, err))
			continue
		}
		renamed++
		touchedAlbums[albumID] = true
		e := albumEvent(ctx, s.DB, albumID, store.EventRenamed)
		e.Detail, e.Source = item.Current+" → "+item.New, "organize"
		s.Events.Record(ctx, e)
		if old := filepath.Dir(src); old != filepath.Clean(artistPath) {
			// os.Remove only removes an empty directory, which is the point.
			_ = os.Remove(old)
		}
	}
	for albumID := range touchedAlbums {
		if err := store.SetAlbumPath(ctx, s.DB, albumID); err != nil {
			problems = append(problems, fmt.Sprintf("couldn't record album %d's folder: %v", albumID, err))
		}
		s.writeMetadata(func() error { return s.Metadata.WriteAlbum(ctx, albumID) })
	}
	return renamed, problems, nil
}

func (s *ImportService) albumOfTrackFile(ctx context.Context, fileID int64) (int64, error) {
	var albumID int64
	err := s.DB.QueryRowContext(ctx, `
		SELECT ar.album_id FROM tracks t
		JOIN album_releases ar ON ar.id = t.album_release_id
		WHERE t.track_file_id = ?`, fileID).Scan(&albumID)
	return albumID, err
}

// DeleteTrackFiles removes the chosen track files from disk (via the
// recycle bin when one is configured) and drops their rows, so the tracks
// read as missing again. The music counterpart of DeleteEpisodeFiles.
func (s *ImportService) DeleteTrackFiles(ctx context.Context, albumID int64, fileIDs []int64) (int, error) {
	album, found, err := store.GetAlbumDetail(ctx, s.DB, albumID)
	if err != nil || !found {
		return 0, err
	}
	files, err := store.ListTrackFileDetails(ctx, s.DB, albumID)
	if err != nil {
		return 0, err
	}
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return 0, err
	}
	chosen := map[int64]bool{}
	for _, id := range fileIDs {
		chosen[id] = true
	}
	removed := 0
	var names []string
	for _, f := range files {
		if !chosen[f.ID] {
			continue
		}
		if album.Path.Valid {
			full := filepath.Join(album.Path.String, f.RelativePath)
			if err := importer.RecycleOrRemove(full, ms.RecycleBinPath); err != nil {
				return removed, fmt.Errorf("remove %s: %w", full, err)
			}
		}
		if err := store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
			return store.DeleteTrackFile(ctx, tx, f.ID)
		}); err != nil {
			return removed, err
		}
		removed++
		names = append(names, filepath.Base(f.RelativePath))
	}
	if removed > 0 {
		e := albumEvent(ctx, s.DB, albumID, store.EventDeleted)
		e.Detail = fmt.Sprintf("%d file(s) removed: %s", removed, strings.Join(names, ", "))
		e.Source = "manage track files"
		s.Events.Record(ctx, e)
	}
	return removed, nil
}

// albumRenameSkips groups an album's untouched subfolders, so the dialog
// can show what Organize left behind and offer to re-match it.
func (s *ImportService) albumRenameSkips(ctx context.Context, artistPath string, albumID int64, album string) ([]RenameSkip, error) {
	albumPath, err := store.AlbumFolderPath(ctx, s.DB, albumID)
	if err != nil {
		return nil, err
	}
	refs, err := store.ListTrackFilesForAlbum(ctx, s.DB, albumID)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	var order []string
	for _, ref := range refs {
		dir := filepath.Dir(ref.RelativePath)
		if dir == "." {
			continue
		}
		rel, err := filepath.Rel(artistPath, filepath.Join(albumPath, dir))
		if err != nil {
			continue
		}
		if _, seen := counts[rel]; !seen {
			order = append(order, rel)
		}
		counts[rel]++
	}
	skips := make([]RenameSkip, 0, len(order))
	for _, rel := range order {
		skips = append(skips, RenameSkip{AlbumID: albumID, Album: album, Folder: rel, Files: counts[rel]})
	}
	return skips, nil
}
