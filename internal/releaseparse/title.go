package releaseparse

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// This file reads what a release title is for - a movie and year, or a
// series with its season and episodes - plus Radarr's proper/repack
// revision and hardcoded subtitle tags. It's a simplified take on Radarr's
// and Sonarr's parsers: enough to match releases to library items, not a
// port of every naming scheme they handle.

// MovieInfo is what a release title says about a movie.
type MovieInfo struct {
	Title string
	Year  int // 0 when the title has none
}

// EpisodeInfo is what a release title says about a series.
type EpisodeInfo struct {
	SeriesTitle string
	Year        int // a year at the end of the series title, e.g. "Doctor Who 2005"
	Season      int
	Episodes    []int     // empty for a full season pack or a daily episode
	FullSeason  bool      // a whole-season pack
	MultiSeason bool      // spans several seasons, e.g. S01-S03
	AirDate     time.Time // a daily episode, e.g. "Show 2024.05.01"
}

const sep = `[\s._-]`

var (
	leadingGroup = regexp.MustCompile(`^\[[^\]]*\]` + sep + `*`)
	// Year candidates; yearBoundary checks what's either side, since Go's
	// regexp has no lookaround and a consumed separator would hide the
	// second of two adjacent years ("2049.2017").
	movieYear = regexp.MustCompile(`(?:18|19|20)\d{2}`)
	// Tokens that start the technical part of a title when there's no year.
	titleJunk = regexp.MustCompile(`(?i)[\s._(\[-](?:2160p|1080p|1080i|720p|576p|480p|4k|uhd|blu-?ray|bdrip|brrip|web-?dl|webrip|web|hdtv|dvdrip|dvd|remux|x264|x265|h\.?264|h\.?265|hevc|proper|repack|multi|complete|internal)(?:[\s._)\]-]|$)`)

	seasonEpisodes = regexp.MustCompile(`(?i)^(.+?)` + sep + `+s(\d{1,2})` + sep + `?e(\d{1,3})((?:` + sep + `?-?` + sep + `?e\d{1,3})*)(?:-(\d{1,3}))?(?:[\s._\[\](),-]|$)`)
	extraEpisode   = regexp.MustCompile(`(?i)e(\d{1,3})`)
	crossEpisode   = regexp.MustCompile(`(?i)^(.+?)` + sep + `+(\d{1,2})x(\d{2,3})(?:` + sep + `|$)`)
	multiSeason    = regexp.MustCompile(`(?i)^(.+?)` + sep + `+(?:s(\d{1,2})` + sep + `?-` + sep + `?s?(\d{1,2})|seasons?` + sep + `?(\d{1,2})` + sep + `?-` + sep + `?(\d{1,2}))(?:[\s._\[\](),-]|$)`)
	seasonPack     = regexp.MustCompile(`(?i)^(.+?)` + sep + `+(?:s(\d{1,2})|season` + sep + `?(\d{1,2}))(?:[\s._\[\](),-]|$)`)
	dailyEpisode   = regexp.MustCompile(`^(.+?)` + sep + `+((?:19|20)\d{2})` + sep + `(\d{2})` + sep + `(\d{2})(?:` + sep + `|$)`)
	trailingYear   = regexp.MustCompile(`[\s(\[]+((?:19|20)\d{2})[)\]]*$`)
)

// yearBoundary reports whether position i (just before or after a year) is
// the edge of the title or a separator, so "2160p" and "12010" aren't years.
func yearBoundary(t string, i int) bool {
	if i < 0 || i >= len(t) {
		return true
	}
	return strings.IndexByte(" ._()[]-", t[i]) >= 0
}

// cleanName turns separators into spaces and trims leftover brackets and dashes.
func cleanName(s string) string {
	s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.Trim(s, " -([")
}

// ParseMovie reads a movie title and year from a release title. Like Radarr
// it takes the last year with a title before it, so "Blade Runner 2049 2017"
// is Blade Runner 2049 from 2017.
func ParseMovie(release string) (MovieInfo, bool) {
	t := leadingGroup.ReplaceAllString(strings.TrimSpace(release), "")
	matches := movieYear.FindAllStringIndex(t, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		start, end := matches[i][0], matches[i][1]
		if !yearBoundary(t, start-1) || !yearBoundary(t, end) {
			continue
		}
		name := cleanName(t[:start])
		if name == "" {
			continue
		}
		year, _ := strconv.Atoi(t[start:end])
		return MovieInfo{Title: name, Year: year}, true
	}
	if loc := titleJunk.FindStringIndex(t); loc != nil {
		if name := cleanName(t[:loc[0]]); name != "" {
			return MovieInfo{Title: name}, true
		}
	}
	return MovieInfo{}, false
}

func seriesName(raw string) (string, int) {
	name := cleanName(raw)
	if m := trailingYear.FindStringSubmatchIndex(name); m != nil && m[0] > 0 {
		year, _ := strconv.Atoi(name[m[2]:m[3]])
		return cleanName(name[:m[0]]), year
	}
	return name, 0
}

// ParseEpisode reads the series, season and episodes from a release title:
// S01E02, S01E02E03, S01E02-E04, 1x02, season packs (S01, Season 1),
// multi-season packs and daily episodes (2024.05.01).
func ParseEpisode(release string) (EpisodeInfo, bool) {
	t := leadingGroup.ReplaceAllString(strings.TrimSpace(release), "")

	if m := seasonEpisodes.FindStringSubmatch(t); m != nil {
		info := EpisodeInfo{}
		info.SeriesTitle, info.Year = seriesName(m[1])
		info.Season, _ = strconv.Atoi(m[2])
		first, _ := strconv.Atoi(m[3])
		last := first
		extras := extraEpisode.FindAllStringSubmatch(m[4], -1)
		ranged := strings.Contains(m[4], "-")
		for _, e := range extras {
			n, _ := strconv.Atoi(e[1])
			last = n
			if !ranged {
				info.Episodes = append(info.Episodes, n)
			}
		}
		if m[5] != "" {
			last, _ = strconv.Atoi(m[5])
			ranged = true
		}
		if ranged && last > first {
			info.Episodes = nil
			for n := first; n <= last; n++ {
				info.Episodes = append(info.Episodes, n)
			}
		} else {
			info.Episodes = append([]int{first}, info.Episodes...)
		}
		return info, info.SeriesTitle != ""
	}
	if m := crossEpisode.FindStringSubmatch(t); m != nil {
		info := EpisodeInfo{}
		info.SeriesTitle, info.Year = seriesName(m[1])
		info.Season, _ = strconv.Atoi(m[2])
		ep, _ := strconv.Atoi(m[3])
		info.Episodes = []int{ep}
		return info, info.SeriesTitle != ""
	}
	if m := multiSeason.FindStringSubmatch(t); m != nil {
		info := EpisodeInfo{MultiSeason: true, FullSeason: true}
		info.SeriesTitle, info.Year = seriesName(m[1])
		info.Season, _ = strconv.Atoi(m[2] + m[4])
		return info, info.SeriesTitle != ""
	}
	if m := seasonPack.FindStringSubmatch(t); m != nil {
		info := EpisodeInfo{FullSeason: true}
		info.SeriesTitle, info.Year = seriesName(m[1])
		info.Season, _ = strconv.Atoi(m[2] + m[3])
		return info, info.SeriesTitle != ""
	}
	if m := dailyEpisode.FindStringSubmatch(t); m != nil {
		date, err := time.Parse("2006-01-02", m[2]+"-"+m[3]+"-"+m[4])
		if err != nil {
			return EpisodeInfo{}, false
		}
		info := EpisodeInfo{AirDate: date}
		info.SeriesTitle, info.Year = seriesName(m[1])
		return info, info.SeriesTitle != ""
	}
	return EpisodeInfo{}, false
}

// Revision is Radarr's release revision: version 2 for a proper or repack
// (or the version a "v2" tag gives), plus how many times REAL appears.
type Revision struct {
	Version  int
	Real     int
	IsRepack bool
}

// Compare orders revisions: -1 when r is older than o, 1 when newer.
func (r Revision) Compare(o Revision) int {
	switch {
	case r.Version != o.Version:
		if r.Version < o.Version {
			return -1
		}
		return 1
	case r.Real != o.Real:
		if r.Real < o.Real {
			return -1
		}
		return 1
	}
	return 0
}

var (
	properPattern  = regexp.MustCompile(`(?i)\bproper\b`)
	repackPattern  = regexp.MustCompile(`(?i)\b(?:repack\d?|rerip\d?)\b`)
	versionPattern = regexp.MustCompile(`(?i)\d[-._ ]?v(\d)[-._ ]|\[v(\d)\]|repack(\d)|rerip(\d)`)
	realPattern    = regexp.MustCompile(`\bREAL\b`)
)

// ParseRevision follows Radarr's QualityParser.ParseQualityModifiers.
func ParseRevision(release string) Revision {
	r := Revision{Version: 1}
	name := strings.ReplaceAll(strings.TrimSpace(release), "_", " ")
	version := 0
	if m := versionPattern.FindStringSubmatch(name); m != nil {
		for _, g := range m[1:] {
			if g != "" {
				version, _ = strconv.Atoi(g)
				r.Version = version
				break
			}
		}
	}
	bumped := 2
	if version > 0 {
		bumped = version + 1
	}
	if properPattern.MatchString(name) {
		r.Version = bumped
	}
	if repackPattern.MatchString(name) {
		r.Version = bumped
		r.IsRepack = true
	}
	r.Real = len(realPattern.FindAllString(release, -1))
	return r
}

var (
	hardcodedSubsWord = regexp.MustCompile(`(?i)\b(\w+)SUBS?\b`)
	hardcodedGeneric  = regexp.MustCompile(`(?i)\b(?:HC|SUBBED)\b`)
)

// HardcodedSubs returns the hardcoded subtitle tag in a release title ("" for
// none), following Radarr: a word ending in SUB/SUBS other than SOFTSUBS,
// MULTISUBS or HORRIBLESUBS gives that word, and HC or SUBBED gives
// "Generic Hardcoded Subs". The last one in the title wins.
func HardcodedSubs(release string) string {
	pos, value := -1, ""
	for _, m := range hardcodedSubsWord.FindAllStringSubmatchIndex(release, -1) {
		prefix := strings.ToUpper(release[m[2]:m[3]])
		if strings.HasSuffix(prefix, "SOFT") || strings.HasSuffix(prefix, "MULTI") || strings.HasSuffix(prefix, "HORRIBLE") {
			continue
		}
		pos, value = m[0], release[m[0]:m[1]]
	}
	for _, m := range hardcodedGeneric.FindAllStringIndex(release, -1) {
		if m[0] > pos {
			pos, value = m[0], "Generic Hardcoded Subs"
		}
	}
	return value
}
