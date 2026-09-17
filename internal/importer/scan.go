package importer

import (
	"fmt"
	"io/fs"
	"path/filepath"
)

// ScanDirectory walks root and returns every regular file found, with
// paths relative to root. Used only by the "Check again" manual retry
// path (internal/sync's DownloadService.RetryImport) - unlike the
// automatic import path, which trusts Deluge's own torrent file listing
// (fast, no filesystem access), a retry needs to see whatever is
// actually on disk right now, since Deluge never reports files a user
// extracted from an archive after the torrent itself finished.
func ScanDirectory(root string) ([]File, error) {
	var files []File
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, File{Path: rel, Size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan directory %s: %w", root, err)
	}
	return files, nil
}
