package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/nfo"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// RenameItem is one file whose name doesn't match the naming templates,
// with both paths relative to the series folder - Sonarr's Organize &
// Rename preview.
type RenameItem struct {
	FileID  int64   // the first episode's row: what the checkbox carries
	FileIDs []int64 // every episode row sharing the file
	Current string
	New     string
}

// RenamePreview is Sonarr's Organize & Rename preview for a series: every
// episode file that would move or be renamed to match the season folder and
// episode file templates. Files already named correctly are left out. A
// file holding several episodes is one row, named for all of them.
func (s *ImportService) RenamePreview(ctx context.Context, seriesID int64) (seriesPath string, items []RenameItem, err error) {
	seriesPath, err = store.SeriesFolderPath(ctx, s.DB, seriesID)
	if err != nil {
		return "", nil, err
	}
	refs, err := store.ListEpisodeFileRefs(ctx, s.DB, seriesID)
	if err != nil {
		return "", nil, err
	}
	type group struct {
		first      store.EpisodeFileRef
		episodeIDs []int64
		fileIDs    []int64
	}
	var order []string
	groups := map[string]*group{}
	for _, ref := range refs {
		key := filepath.Clean(ref.RelativePath)
		g, ok := groups[key]
		if !ok {
			g = &group{first: ref}
			groups[key] = g
			order = append(order, key)
		}
		g.episodeIDs = append(g.episodeIDs, ref.EpisodeID)
		g.fileIDs = append(g.fileIDs, ref.FileID)
	}
	for _, key := range order {
		g := groups[key]
		folder, err := store.ResolveEpisodeFolderPath(ctx, s.DB, seriesID, g.first.SeasonNumber)
		if err != nil {
			continue
		}
		name, err := store.ResolveEpisodesFileName(ctx, s.DB, g.episodeIDs, g.first.RelativePath)
		if err != nil {
			continue
		}
		newRel, err := filepath.Rel(seriesPath, filepath.Join(folder, name))
		if err != nil || newRel == key {
			continue
		}
		items = append(items, RenameItem{FileID: g.fileIDs[0], FileIDs: g.fileIDs, Current: g.first.RelativePath, New: newRel})
	}
	return seriesPath, items, nil
}

// RenameFiles is Sonarr's Organize: renames the chosen files (by episode
// file id) to what RenamePreview shows and records their new paths. A
// folder left empty by a move is removed. It reports how many were renamed
// and what went wrong for the rest.
func (s *ImportService) RenameFiles(ctx context.Context, seriesID int64, fileIDs []int64) (renamed int, problems []string, err error) {
	seriesPath, items, err := s.RenamePreview(ctx, seriesID)
	if err != nil {
		return 0, nil, err
	}
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return 0, nil, err
	}
	perms := permissions(ms)
	chosen := map[int64]bool{}
	for _, id := range fileIDs {
		chosen[id] = true
	}
	for _, item := range items {
		if !chosen[item.FileID] {
			continue
		}
		src, dest := filepath.Join(seriesPath, item.Current), filepath.Join(seriesPath, item.New)
		if err := importer.RenameFileIfDifferent(src, dest, perms); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", item.Current, err))
			continue
		}
		recorded := true
		for _, fileID := range item.FileIDs {
			if err := store.UpdateEpisodeFilePath(ctx, s.DB, fileID, item.New); err != nil {
				problems = append(problems, fmt.Sprintf("%s: renamed, but couldn't record it: %v", item.Current, err))
				recorded = false
			}
		}
		if !recorded {
			continue
		}
		renamed++
		e := seriesEvent(ctx, s.DB, seriesID, store.EventRenamed)
		e.Detail, e.Source = item.Current+" → "+item.New, "organize"
		s.Events.Record(ctx, e)
		nfo.RemoveEpisodeNFO(seriesPath, item.Current)
		// os.Remove only removes an empty directory, which is the point.
		if old := filepath.Dir(src); old != filepath.Clean(seriesPath) {
			_ = os.Remove(old)
		}
	}
	if renamed > 0 {
		s.writeMetadata(func() error { return s.Metadata.WriteSeries(ctx, seriesID) })
	}
	return renamed, problems, nil
}

// ErrUnsafeDelete means a series folder isn't somewhere UMMarr will remove.
var ErrUnsafeDelete = errors.New("the series folder isn't inside a library folder, so its files were left alone")

// DeleteSeries removes a series from UMMarr and, when deleteFiles is set, its
// whole folder from disk. The folder is only removed when it sits inside one
// of the library folders and isn't a library folder itself; otherwise the
// series is still removed and ErrUnsafeDelete reports the files were kept.
func (s *ImportService) DeleteSeries(ctx context.Context, seriesID int64, deleteFiles bool) error {
	detail, found, err := store.GetSeriesDetail(ctx, s.DB, seriesID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	roots, err := store.ListRootFolders(ctx, s.DB, "series")
	if err != nil {
		return err
	}
	deleted := seriesEvent(ctx, s.DB, seriesID, store.EventDeleted)
	if deleteFiles {
		deleted.Detail = "Series and files removed"
	} else {
		deleted.Detail = "Series removed, files kept"
	}
	if err := store.WithTx(ctx, s.DB, func(tx *sql.Tx) error { return store.DeleteSeries(ctx, tx, seriesID) }); err != nil {
		return err
	}
	s.Events.Record(ctx, deleted)
	if !deleteFiles || !detail.Path.Valid {
		return nil
	}
	folder := filepath.Clean(detail.Path.String)
	for _, root := range roots {
		rootPath := filepath.Clean(root.Path)
		if folder != rootPath && strings.HasPrefix(folder, rootPath+string(filepath.Separator)) {
			if err := os.RemoveAll(folder); err != nil {
				return fmt.Errorf("remove %s: %w", folder, err)
			}
			return nil
		}
	}
	return ErrUnsafeDelete
}

// removeInsideLibrary removes folder from disk when it sits inside one of
// mediaType's library folders (and isn't one itself).
func (s *ImportService) removeInsideLibrary(ctx context.Context, mediaType, folder string) error {
	roots, err := store.ListRootFolders(ctx, s.DB, mediaType)
	if err != nil {
		return err
	}
	folder = filepath.Clean(folder)
	for _, root := range roots {
		rootPath := filepath.Clean(root.Path)
		if folder != rootPath && strings.HasPrefix(folder, rootPath+string(filepath.Separator)) {
			if err := os.RemoveAll(folder); err != nil {
				return fmt.Errorf("remove %s: %w", folder, err)
			}
			return nil
		}
	}
	return ErrUnsafeDelete
}

// DeleteMovie is DeleteSeries for a movie.
func (s *ImportService) DeleteMovie(ctx context.Context, movieID int64, deleteFiles bool) error {
	detail, found, err := store.GetMovieDetail(ctx, s.DB, movieID)
	if err != nil || !found {
		return err
	}
	e := movieEvent(ctx, s.DB, movieID, store.EventDeleted)
	if err := store.WithTx(ctx, s.DB, func(tx *sql.Tx) error { return store.DeleteMovie(ctx, tx, movieID) }); err != nil {
		return err
	}
	e.Detail = "Movie removed, files kept"
	if deleteFiles && detail.Path.Valid {
		e.Detail = "Movie and files removed"
		if err := s.removeInsideLibrary(ctx, "movie", detail.Path.String); err != nil {
			e.Detail = "Movie removed; files kept: " + err.Error()
			s.Events.Record(ctx, e)
			return err
		}
	}
	s.Events.Record(ctx, e)
	return nil
}

// DeleteAlbum is DeleteSeries for an album.
func (s *ImportService) DeleteAlbum(ctx context.Context, albumID int64, deleteFiles bool) error {
	detail, found, err := store.GetAlbumDetail(ctx, s.DB, albumID)
	if err != nil || !found {
		return err
	}
	e := albumEvent(ctx, s.DB, albumID, store.EventDeleted)
	if err := store.WithTx(ctx, s.DB, func(tx *sql.Tx) error { return store.DeleteAlbum(ctx, tx, albumID) }); err != nil {
		return err
	}
	e.Detail = "Album removed, files kept"
	if deleteFiles && detail.Path.Valid {
		e.Detail = "Album and files removed"
		if err := s.removeInsideLibrary(ctx, "music", detail.Path.String); err != nil {
			e.Detail = "Album removed; files kept: " + err.Error()
			s.Events.Record(ctx, e)
			return err
		}
	}
	s.Events.Record(ctx, e)
	return nil
}
