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

// DeleteEpisodeFiles removes the given episode files (Manage Episodes) from
// disk - into the recycle bin when one is set - and from the library, so the
// episodes read as missing again. Returns how many files were removed.
func (s *ImportService) DeleteEpisodeFiles(ctx context.Context, seriesID int64, fileIDs []int64) (int, error) {
	detail, found, err := store.GetSeriesDetail(ctx, s.DB, seriesID)
	if err != nil || !found {
		return 0, err
	}
	files, err := store.ListEpisodeFileDetails(ctx, s.DB, seriesID)
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
		picked := false
		for _, id := range f.FileIDs {
			picked = picked || chosen[id]
		}
		if !picked {
			continue
		}
		if detail.Path.Valid {
			full := filepath.Join(detail.Path.String, f.RelativePath)
			if err := importer.RecycleOrRemove(full, ms.RecycleBinPath); err != nil {
				return removed, fmt.Errorf("remove %s: %w", full, err)
			}
		}
		err := store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
			for _, id := range f.FileIDs {
				if err := store.DeleteEpisodeFile(ctx, tx, id); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return removed, err
		}
		removed++
		names = append(names, filepath.Base(f.RelativePath))
	}
	if removed > 0 {
		e := seriesEvent(ctx, s.DB, seriesID, store.EventDeleted)
		e.Detail = fmt.Sprintf("%d file(s) removed: %s", removed, strings.Join(names, ", "))
		e.Source = "manage episodes"
		s.Events.Record(ctx, e)
	}
	return removed, nil
}

// MoveSeries changes a series' folder (Edit Series → Path). With moveFiles
// the existing folder is renamed to the new path first; episode files are
// stored relative to the series folder, so nothing else changes.
func (s *ImportService) MoveSeries(ctx context.Context, seriesID int64, newPath string, moveFiles bool) error {
	detail, found, err := store.GetSeriesDetail(ctx, s.DB, seriesID)
	if err != nil || !found {
		return err
	}
	newPath = filepath.Clean(newPath)
	oldPath := filepath.Clean(detail.Path.String)
	if !detail.Path.Valid || oldPath == newPath {
		return store.SetSeriesPath(ctx, s.DB, seriesID, newPath)
	}
	if moveFiles {
		if _, err := os.Stat(oldPath); err == nil {
			if _, err := os.Stat(newPath); err == nil {
				return fmt.Errorf("%s already exists; pick another path or untick Move files", newPath)
			}
			if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
				return err
			}
			if err := os.Rename(oldPath, newPath); err != nil {
				return fmt.Errorf("move %s to %s: %w", oldPath, newPath, err)
			}
		}
	}
	if err := store.SetSeriesPath(ctx, s.DB, seriesID, newPath); err != nil {
		return err
	}
	e := seriesEvent(ctx, s.DB, seriesID, store.EventRenamed)
	if moveFiles {
		e.Detail = fmt.Sprintf("Folder moved from %s to %s", oldPath, newPath)
	} else {
		e.Detail = fmt.Sprintf("Path changed from %s to %s (files not moved)", oldPath, newPath)
	}
	e.Source = "edit series"
	s.Events.Record(ctx, e)
	return nil
}
