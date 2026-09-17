package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// moveFolder renames oldPath to newPath when moveFiles is set; a folder
// that isn't on disk yet has nothing to move.
func moveFolder(oldPath, newPath string, moveFiles bool) error {
	if !moveFiles || oldPath == newPath {
		return nil
	}
	if _, err := os.Stat(oldPath); err != nil {
		return nil
	}
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("%s already exists; pick another folder or untick Move files", newPath)
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return err
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("move %s to %s: %w", oldPath, newPath, err)
	}
	return nil
}

// ChangeRootFolder moves a movie or series into another library folder,
// keeping its own folder name - the mass editor's Root Folder. With
// moveFiles its folder moves too; otherwise only where UMMarr looks changes.
func (s *ImportService) ChangeRootFolder(ctx context.Context, kind string, id, rootFolderID int64, moveFiles bool) error {
	mediaType, table := "movie", "movies"
	switch kind {
	case "movie":
	case "series":
		mediaType, table = "series", "series"
	case "artist":
		mediaType, table = "music", "artists"
	default:
		return fmt.Errorf("unknown library kind %q", kind)
	}
	roots, err := store.ListRootFolders(ctx, s.DB, mediaType)
	if err != nil {
		return err
	}
	var root *store.RootFolder
	for i := range roots {
		if roots[i].ID == rootFolderID {
			root = &roots[i]
		}
	}
	if root == nil {
		return fmt.Errorf("library folder %d isn't a %s folder", rootFolderID, mediaType)
	}
	var title, current string
	var currentRoot int64
	if kind == "artist" {
		d, found, err := store.GetArtistDetail(ctx, s.DB, id)
		if err != nil || !found {
			return fmt.Errorf("artist %d not found: %v", id, err)
		}
		title, current, currentRoot = d.Name, d.Path.String, d.RootFolderID
	} else if kind == "movie" {
		d, found, err := store.GetMovieDetail(ctx, s.DB, id)
		if err != nil || !found {
			return fmt.Errorf("movie %d not found: %v", id, err)
		}
		title, current, currentRoot = d.Title, d.Path.String, d.RootFolderID
	} else {
		d, found, err := store.GetSeriesDetail(ctx, s.DB, id)
		if err != nil || !found {
			return fmt.Errorf("series %d not found: %v", id, err)
		}
		title, current, currentRoot = d.Title, d.Path.String, d.RootFolderID
	}
	if currentRoot == rootFolderID {
		return nil
	}
	if current == "" {
		return fmt.Errorf("%s has no folder yet", title)
	}
	oldPath := filepath.Clean(current)
	newPath := filepath.Join(root.Path, filepath.Base(oldPath))
	if err := moveFolder(oldPath, newPath, moveFiles); err != nil {
		return fmt.Errorf("%s: %w", title, err)
	}
	if err := store.SetRootFolder(ctx, s.DB, table, id, rootFolderID, newPath); err != nil {
		return err
	}
	var e store.Event
	switch kind {
	case "movie":
		e = movieEvent(ctx, s.DB, id, store.EventRenamed)
	case "artist":
		e = store.Event{Event: store.EventRenamed, Title: title}
	default:
		e = seriesEvent(ctx, s.DB, id, store.EventRenamed)
	}
	e.Source = "mass editor"
	if moveFiles {
		e.Detail = fmt.Sprintf("Moved from %s to %s", oldPath, newPath)
	} else {
		e.Detail = fmt.Sprintf("Library folder changed to %s (files not moved)", root.Path)
	}
	s.Events.Record(ctx, e)
	return nil
}
