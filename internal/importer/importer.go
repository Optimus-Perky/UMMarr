// Package importer holds the pure, dependency-free pieces of importing a
// finished download: extension classification, filename parsing, and the
// generic file-copy primitive. No database or download-client knowledge -
// internal/sync/import.go owns the movie/series/album algorithms and maps
// []deluge.TorrentFile into this package's own File type, keeping this a
// leaf package like internal/pathbuilder/internal/titleutil (though
// unlike those two, CopyFile does touch the filesystem).
package importer

import (
	"path/filepath"
	"sort"
	"strings"
)

// File is a downloaded file's relative path (relative to the download's
// save_path, i.e. deluge.TorrentFile.Path, or to a ScanDirectory root) and
// size.
type File struct {
	Path string
	Size int64
}

var videoExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".m4v": true, ".mov": true,
	".wmv": true, ".ts": true, ".webm": true, ".flv": true, ".mpg": true, ".mpeg": true,
}

var audioExtensions = map[string]bool{
	".mp3": true, ".flac": true, ".m4a": true, ".aac": true, ".ogg": true,
	".opus": true, ".wav": true, ".wma": true, ".alac": true,
}

var archiveExtensions = map[string]bool{
	".zip": true, ".rar": true, ".7z": true, ".tar": true, ".gz": true,
}

// IsVideoFile reports whether relativePath's extension is a recognized
// video container.
func IsVideoFile(relativePath string) bool {
	return videoExtensions[strings.ToLower(filepath.Ext(relativePath))]
}

// IsAudioFile reports whether relativePath's extension is a recognized
// audio format.
func IsAudioFile(relativePath string) bool {
	return audioExtensions[strings.ToLower(filepath.Ext(relativePath))]
}

// IsArchiveFile reports whether relativePath's extension is a recognized
// archive format.
func IsArchiveFile(relativePath string) bool {
	return archiveExtensions[strings.ToLower(filepath.Ext(relativePath))]
}

// LargestVideoFile picks the biggest video-extension file in files, for
// unambiguous single-file movie imports - this naturally excludes
// samples/extras (always much smaller than the main feature) without
// parsing anything out of their names.
func LargestVideoFile(files []File) (File, bool) {
	var best File
	found := false
	for _, f := range files {
		if !IsVideoFile(f.Path) {
			continue
		}
		if !found || f.Size > best.Size {
			best = f
			found = true
		}
	}
	return best, found
}

// LargestAudioFile picks the biggest audio-extension file in files, for
// an unambiguous single-track import (see ImportService.importTrack) -
// mirrors LargestVideoFile's reasoning for movies.
func LargestAudioFile(files []File) (File, bool) {
	var best File
	found := false
	for _, f := range files {
		if !IsAudioFile(f.Path) {
			continue
		}
		if !found || f.Size > best.Size {
			best = f
			found = true
		}
	}
	return best, found
}

// AudioFilesSorted filters files to audio extensions, sorted naturally by
// path - the ordering importAlbum zips positionally against a release's
// track list (see internal/store.FindImportRelease).
func AudioFilesSorted(files []File) []File {
	var audio []File
	for _, f := range files {
		if IsAudioFile(f.Path) {
			audio = append(audio, f)
		}
	}
	sort.Slice(audio, func(i, j int) bool { return audio[i].Path < audio[j].Path })
	return audio
}

// HasArchiveFile reports whether any file in files is a recognized
// archive - the "this download needs manual extraction" signal used when
// nothing else in the download is importable.
func HasArchiveFile(files []File) bool {
	for _, f := range files {
		if IsArchiveFile(f.Path) {
			return true
		}
	}
	return false
}
