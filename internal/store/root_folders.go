package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
)

// builtInRootFolders are the library folders on UMMarr's own /data media
// mount. They're always there, so they can't be removed; any other root folder
// a user adds can be.
var builtInRootFolders = map[string]bool{
	"/data/Movies": true,
	"/data/TV":     true,
	"/data/Music":  true,
}

var (
	ErrRootFolderNotFound = errors.New("library folder not found")
	ErrRootFolderBuiltIn  = errors.New("built-in library folders can't be removed")
	ErrRootFolderInUse    = errors.New("library folder is still in use")
)

// IsBuiltInRootFolder reports whether path is one of the built-in root folders.
func IsBuiltInRootFolder(path string) bool {
	return builtInRootFolders[filepath.Clean(path)]
}

// RootFolderUsage counts the movies, series and artists stored under a root
// folder.
func RootFolderUsage(ctx context.Context, q Queryer, rootFolderID int64) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM movies WHERE root_folder_id = ?)
		     + (SELECT COUNT(*) FROM series WHERE root_folder_id = ?)
		     + (SELECT COUNT(*) FROM artists WHERE root_folder_id = ?)
	`, rootFolderID, rootFolderID, rootFolderID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count usage of root folder %d: %w", rootFolderID, err)
	}
	return n, nil
}

// DeleteRootFolder removes a root folder from UMMarr; nothing on disk is
// touched. Built-in folders are refused, and so is any folder a movie, series
// or artist still points at.
func DeleteRootFolder(ctx context.Context, q Queryer, rootFolderID int64) error {
	var path string
	err := q.QueryRowContext(ctx, `SELECT path FROM root_folders WHERE id = ?`, rootFolderID).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRootFolderNotFound
	}
	if err != nil {
		return fmt.Errorf("get root folder %d: %w", rootFolderID, err)
	}
	if IsBuiltInRootFolder(path) {
		return fmt.Errorf("%s: %w", path, ErrRootFolderBuiltIn)
	}
	n, err := RootFolderUsage(ctx, q, rootFolderID)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%s is used by %d item(s): %w", path, n, ErrRootFolderInUse)
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM root_folders WHERE id = ?`, rootFolderID); err != nil {
		return fmt.Errorf("delete root folder %d: %w", rootFolderID, err)
	}
	return nil
}
