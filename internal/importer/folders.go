package importer

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RemoveEmptyDirs removes every directory inside root that holds no files,
// deepest first, and finally root itself if that leaves it empty. It relies on
// os.Remove refusing a directory that isn't empty, so a directory containing
// any file at all - of any kind, including a symlink - survives, and so does
// every directory above it. The caller must never pass a library root folder.
func RemoveEmptyDirs(root string) (removed int, err error) {
	var dirs []string
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	if walkErr != nil {
		if os.IsNotExist(walkErr) {
			return 0, nil
		}
		return 0, walkErr
	}
	sep := string(os.PathSeparator)
	sort.SliceStable(dirs, func(i, j int) bool {
		return strings.Count(dirs[i], sep) > strings.Count(dirs[j], sep)
	})
	for _, dir := range dirs {
		if os.Remove(dir) == nil {
			removed++
		}
	}
	return removed, nil
}

// UnmappedFolders counts the folders directly inside root that aren't one of
// mapped (compared as cleaned paths) - folders in a library that UMMarr doesn't
// track, like Radarr's Unmapped Folders column. Names in ignore are never
// counted.
func UnmappedFolders(root string, mapped []string, ignore ...string) (int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	known := make(map[string]bool, len(mapped))
	for _, p := range mapped {
		known[filepath.Clean(p)] = true
	}
	skip := make(map[string]bool, len(ignore))
	for _, name := range ignore {
		skip[name] = true
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() || skip[e.Name()] || known[filepath.Join(filepath.Clean(root), e.Name())] {
			continue
		}
		count++
	}
	return count, nil
}
