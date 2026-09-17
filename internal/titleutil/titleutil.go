// Package titleutil computes the clean/sort title variants that
// title/sort_title/clean_title (and the artist/album equivalents) require
// - NOT NULL columns with no default across the schema, so a name has to
// go through here before it can be inserted anywhere. Dependency-free
// besides Unicode normalization, so the future path builder and importers
// can reuse it too, not just the sync service.
package titleutil

import (
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

var leadingArticles = []string{"the ", "a ", "an "}

// CleanTitle lowercases, Unicode-normalizes (NFKD fold, so e.g. "é"
// becomes "e" rather than being dropped), and strips everything but
// letters/digits - matching the Sonarr/Radarr/Lidarr convention used for
// search-matching and duplicate detection. Leading articles are NOT
// stripped here (that's SortTitle's job) since clean_title is for
// matching, not display ordering.
func CleanTitle(s string) string {
	folded := norm.NFKD.String(strings.ToLower(s))
	var b strings.Builder
	b.Grow(len(folded))
	for _, r := range folded {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SortTitle strips one leading "The "/"A "/"An " (case-insensitive) so
// titles sort alphabetically by their meaningful word, otherwise
// preserving the original case and spacing. "The Matrix" -> "Matrix",
// "A Beautiful Mind" -> "Beautiful Mind", "Fargo" -> "Fargo" (unchanged),
// "A" -> "A" (stripping the entire string would leave nothing useful to
// sort by, so a title that's just an article is left as-is).
func SortTitle(s string) string {
	lower := strings.ToLower(s)
	for _, article := range leadingArticles {
		if strings.HasPrefix(lower, article) && len(s) > len(article) {
			return strings.TrimSpace(s[len(article):])
		}
	}
	return s
}

// Slug turns a title into an address segment the way Sonarr does:
// lower-case, accents folded, anything but letters and digits becomes a
// single hyphen. "The Fast and the Furious" -> "the-fast-and-the-furious".
func Slug(s string) string {
	folded := norm.NFKD.String(strings.ToLower(s))
	var b strings.Builder
	b.Grow(len(folded))
	dash := false
	for _, r := range folded {
		switch {
		case unicode.Is(unicode.Mn, r):
			// A folded accent: nothing to write, and no hyphen either.
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// SlugWithYear is Slug plus the year, when known: a movie's address, since
// remakes share a title.
func SlugWithYear(title string, year int64) string {
	slug := Slug(title)
	if year > 0 {
		slug += "-" + strconv.FormatInt(year, 10)
	}
	return slug
}
