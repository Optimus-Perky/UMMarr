package importer

import (
	"regexp"
	"strconv"
)

// Go's regexp package (RE2) has no lookahead/lookbehind, so boundary
// checks below are ordinary consumed character-class groups, not
// assertions.
var (
	// reSeasonEpisode matches "S01E02"/"s01e02" and 3-digit episode
	// variants like "S01E102", plus a trailing run of additional
	// episodes for multi-episode files - "S01E23E24", "S01E23-E24", and
	// "S01E23-24" all match, with the extra numbers pulled out of the
	// captured tail by reExtraEpisode. A separator is required between
	// episode numbers deliberately: a bare concatenation like "S01E2324"
	// is genuinely ambiguous (is that E23+E24, or a single malformed
	// episode 2324?) and isn't guessed at - rename the file with an
	// explicit separator instead.
	reSeasonEpisode = regexp.MustCompile(`(?i)S(\d{1,2})E(\d{2,3})((?:-?E\d{2,3}|-\d{2,3})*)`)

	// reExtraEpisode pulls each additional episode number out of
	// reSeasonEpisode's captured tail group.
	reExtraEpisode = regexp.MustCompile(`\d{2,3}`)

	// reXFormat matches the alternate "1x02"/"12x103" numbering style.
	// Requires a non-digit (or string start/end) on both sides so it
	// doesn't fire on an unrelated digit run like a resolution
	// ("1920x1080") - within a pure digit run, the only non-digit
	// character available to serve as a boundary is the literal "x"
	// itself, which the pattern already consumes as its own separator, so
	// a bare resolution string can never satisfy both boundaries.
	reXFormat = regexp.MustCompile(`(?i)(?:^|[^0-9])(\d{1,2})x(\d{2,3})(?:[^0-9]|$)`)

	// reBareEpisode matches a standalone "E05"/"e005" with no season
	// prefix - used only as a fallback, paired with reSeasonFromPath to
	// recover the season from a parent directory name.
	reBareEpisode = regexp.MustCompile(`(?i)E(\d{2,3})`)

	// reSeasonFromPath recovers a season number from a "Season NN"/
	// "SeasonNN" path component - typically the parent directory Deluge
	// preserved from the source torrent/season-pack layout.
	reSeasonFromPath = regexp.MustCompile(`(?i)Season\s?(\d{1,2})`)
)

// ParseEpisodes extracts a season and one-or-more episode numbers from a
// downloaded file's relative path (which may include a parent-directory
// component, e.g. "Season 02/Show.S02E05.mkv"). Tries, in order: "S01E02"
// style (including multi-episode files - "S01E23E24", "S01E23-E24",
// "S01E23-24" all return episodes []int{23, 24}), "1x02" style, then a bare
// "E05" with the season recovered from a "Season NN" path component
// elsewhere in relativePath. Returns ok=false - skip, do not guess - when
// no recognizable pattern is found.
//
// Explicitly out of scope: absolute/bare episode numbering with no S/E
// marker at all (e.g. anime releases using only "013") - there is no
// reliable way to distinguish a bare episode number from an unrelated
// 2-3 digit number elsewhere in a filename (a resolution, a year
// fragment, a bitrate), so this pass does not attempt it. Also out of
// scope: multi-episode runs with no separator at all ("S01E2324") - that's
// genuinely ambiguous (E23+E24, or one malformed episode 2324?), so it's
// left as a single (wrong) episode number rather than guessed at; rename
// the file with an explicit separator instead.
func ParseEpisodes(relativePath string) (season int, episodes []int, ok bool) {
	if m := reSeasonEpisode.FindStringSubmatch(relativePath); m != nil {
		eps := []int{atoi(m[2])}
		for _, extra := range reExtraEpisode.FindAllString(m[3], -1) {
			eps = append(eps, atoi(extra))
		}
		return atoi(m[1]), eps, true
	}
	if m := reXFormat.FindStringSubmatch(relativePath); m != nil {
		return atoi(m[1]), []int{atoi(m[2])}, true
	}
	if m := reBareEpisode.FindStringSubmatch(relativePath); m != nil {
		if sm := reSeasonFromPath.FindStringSubmatch(relativePath); sm != nil {
			return atoi(sm[1]), []int{atoi(m[1])}, true
		}
	}
	return 0, nil, false
}

// ParseEpisode is ParseEpisodes for callers that only care about a single
// episode - the common case. For a multi-episode file it returns the
// first episode of the match.
func ParseEpisode(relativePath string) (season, episode int, ok bool) {
	season, episodes, ok := ParseEpisodes(relativePath)
	if !ok {
		return 0, 0, false
	}
	return season, episodes[0], true
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s) // regex guarantees s is all digits
	return n
}
