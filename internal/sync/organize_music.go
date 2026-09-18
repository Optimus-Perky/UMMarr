package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
func (s *ImportService) ArtistRenamePreview(ctx context.Context, artistID int64) (artistPath string, items []RenameItem, err error) {
	artistPath, err = store.ArtistFolderPath(ctx, s.DB, artistID)
	if err != nil {
		return "", nil, err
	}
	var metadataID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT artist_metadata_id FROM artists WHERE id = ?`, artistID).Scan(&metadataID); err != nil {
		return "", nil, fmt.Errorf("find artist %d: %w", artistID, err)
	}
	albums, err := store.ListAlbumsForArtist(ctx, s.DB, metadataID)
	if err != nil {
		return "", nil, err
	}
	for _, album := range albums {
		albumItems, err := s.albumRenameItems(ctx, artistPath, album.ID)
		if err != nil {
			continue // one unreadable album shouldn't hide the rest
		}
		items = append(items, albumItems...)
	}
	return artistPath, items, nil
}

// AlbumRenamePreview is the same for one album.
func (s *ImportService) AlbumRenamePreview(ctx context.Context, albumID int64) (artistPath string, items []RenameItem, err error) {
	var artistID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT ar.id FROM albums al JOIN artists ar ON ar.artist_metadata_id = al.artist_metadata_id WHERE al.id = ?`, albumID).Scan(&artistID); err != nil {
		return "", nil, fmt.Errorf("find the artist of album %d: %w", albumID, err)
	}
	artistPath, err = store.ArtistFolderPath(ctx, s.DB, artistID)
	if err != nil {
		return "", nil, err
	}
	items, err = s.albumRenameItems(ctx, artistPath, albumID)
	return artistPath, items, err
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
	artistPath, items, err := s.ArtistRenamePreview(ctx, artistID)
	if err != nil {
		return 0, nil, err
	}
	return s.renameTrackFiles(ctx, artistPath, items, fileIDs)
}

// RenameAlbumFiles is RenameArtistFiles for one album.
func (s *ImportService) RenameAlbumFiles(ctx context.Context, albumID int64, fileIDs []int64) (renamed int, problems []string, err error) {
	artistPath, items, err := s.AlbumRenamePreview(ctx, albumID)
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
	for _, item := range items {
		if !chosen[item.FileID] {
			continue
		}
		src, dest := filepath.Join(artistPath, item.Current), filepath.Join(artistPath, item.New)
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
