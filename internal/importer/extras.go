package importer

import (
	"path/filepath"
	"strings"
)

// ParseExtensions reads a comma-separated extension list such as "srt, .nfo"
// into a set of lower-case extensions with their leading dot.
func ParseExtensions(list string) map[string]bool {
	exts := map[string]bool{}
	for _, part := range strings.Split(list, ",") {
		ext := strings.TrimLeft(strings.ToLower(strings.TrimSpace(part)), ".")
		if ext != "" {
			exts["."+ext] = true
		}
	}
	return exts
}

// IsExtraFile reports whether path has one of exts.
func IsExtraFile(path string, exts map[string]bool) bool {
	return exts[strings.ToLower(filepath.Ext(path))]
}

func stem(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// BelongsTo reports whether extra's name starts with main's, ignoring
// extensions and case: "Show.S01E01.en.srt" belongs to "Show.S01E01.mkv".
func BelongsTo(extra, main string) bool {
	return strings.HasPrefix(strings.ToLower(stem(extra)), strings.ToLower(stem(main)))
}

// ExtraName names an extra file imported beside a main file that was renamed
// to newMainName: the new name plus whatever followed the old main name in
// the extra's name. "Movie.2010.en.srt" beside "Movie.2010.mkv", renamed to
// "The Matrix (1999).mkv", becomes "The Matrix (1999).en.srt". An extra whose
// name doesn't start with the main file's keeps its own name after the new
// one: "English.srt" becomes "The Matrix (1999).English.srt".
func ExtraName(extra, main, newMainName string) string {
	suffix := "." + stem(extra)
	if BelongsTo(extra, main) && len(stem(extra)) >= len(stem(main)) {
		suffix = stem(extra)[len(stem(main)):]
	}
	return stem(newMainName) + suffix + extraExtension(extra)
}

// KeptExtraName is the name for an extra that isn't renamed to match a main
// file, such as an album's cover.jpg.
func KeptExtraName(extra string) string {
	return stem(extra) + extraExtension(extra)
}

// extraExtension keeps an extra's extension, except that .nfo becomes
// .nfo-orig, as Radarr does: media servers write their own .nfo files, and
// the downloaded one shouldn't be mistaken for or overwrite theirs.
func extraExtension(path string) string {
	ext := filepath.Ext(path)
	if strings.EqualFold(ext, ".nfo") {
		return ".nfo-orig"
	}
	return ext
}
