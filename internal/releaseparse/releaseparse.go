// Package releaseparse extracts a rough quality (source + resolution),
// video codec and release group out of a release title or a downloaded
// file's own name - pure, dependency-free string parsing, no I/O, mirroring
// internal/pathbuilder/internal/titleutil's leaf-package shape.
//
// This is deliberately NOT a Custom-Formats-style engine (no regex-rule
// scoring, no per-indexer preferences) - just enough to populate the
// Quality/Release Group columns Radarr/Sonarr show, and to feed a
// user-weighted quality-profile score (see Score, AllQualities) that
// decides which releases get grabbed.
package releaseparse

import (
	"regexp"
	"strings"
)

// FileQuality is what Parse extracts, and what gets persisted as JSON
// into movie_files.quality / episode_files.quality / track_files.quality
// (columns that have existed since the original schema but were never
// written to before this).
type FileQuality struct {
	Source       string `json:"source,omitempty"`     // "Bluray"|"WEBDL"|"WEBRip"|"HDTV"|"SDTV"|"DVD"|"Remux"|""
	Resolution   string `json:"resolution,omitempty"` // "2160p"|"1080p"|"720p"|"480p"|""
	Codec        string `json:"codec,omitempty"`      // "x265"|"x264"|"HEVC"|"AVC"|"AV1"|"XviD"|"DivX"|"VC-1"|"MPEG2"|""
	ReleaseGroup string `json:"releaseGroup,omitempty"`
}

// String renders "Source-Resolution" (Radarr/Sonarr's own naming
// convention, e.g. "Bluray-1080p"), just whichever half is known, or ""
// if neither parsed.
func (q FileQuality) String() string {
	switch {
	case q.Source != "" && q.Resolution != "":
		return q.Source + "-" + q.Resolution
	case q.Source != "":
		return q.Source
	case q.Resolution != "":
		return q.Resolution
	default:
		return ""
	}
}

type namedPattern struct {
	pattern *regexp.Regexp
	name    string
}

var (
	// resolutionPattern follows Sonarr's ResolutionRegex, folded onto the
	// resolutions UMMarr's quality catalog has. The first one in the title
	// wins, as in Sonarr.
	resolutionPattern = regexp.MustCompile(`(?i)\b(?:` +
		`(?P<r2160>2160p|3840x2160|4k[-_. ](?:uhd|hevc|bd|h\.?265)|(?:uhd|hevc|bd|h\.?265)[-_. ]4k)|` +
		`(?P<r1080>1080p|1920x1080|1440p|fhd|1080i)|` +
		`(?P<r720>720p|1280x720|960p)|` +
		`(?P<r480>480p|480i|640x480|848x480|540p|576p|360p))\b`)
	// alternativeResolution is Sonarr's fallback: a bare UHD or [4K] means 2160p.
	alternativeResolution = regexp.MustCompile(`(?i)\buhd\b|\[4k\]`)

	// releaseGroupPattern anchors at the true end of the string, but Parse
	// is called on both a raw release title (no extension, e.g. from a
	// search result) AND a downloaded file's own name (always has an
	// extension, e.g. "...x265-hallowed.mkv") - the optional trailing
	// ".ext" group absorbs the latter without requiring two patterns.
	releaseGroupPattern = regexp.MustCompile(`-([A-Za-z0-9]+)(?:\.[A-Za-z0-9]{2,4})?$`)

	// sourcePatterns is checked in order - most-specific first, since a
	// title can contain multiple loosely-related tokens (e.g. a BDRemux
	// release's title contains both "remux" and "bluray"-ish tokens; remux
	// must win since it implies a materially different, higher-fidelity
	// source than a plain encode). The tokens follow Sonarr's SourceRegex.
	sourcePatterns = []namedPattern{
		{regexp.MustCompile(`(?i)remux`), "Remux"},
		{regexp.MustCompile(`(?i)\b(?:blu-?ray|hd-?dvd|bdmux|bdrip|bdlight|brrip)\b|\bbd\b[^$]`), "Bluray"},
		{regexp.MustCompile(`(?i)\bweb[-_. ]?dl(?:mux)?\b|\b(?:amazonhd|amazonsd|ituneshd|maxdomehd|netflixu?hd|webhd|hbomaxhd|disneyhd)\b`), "WEBDL"},
		{regexp.MustCompile(`(?i)\bweb-?rip\b|\bwebmux\b`), "WEBRip"},
		{regexp.MustCompile(`(?i)\b(?:720|1080|2160)p[-_. ]web\b|\bweb[-_. ](?:720|1080|2160)p\b|\bweb[-_. ](?:[xh][ .]?26[45]|avc|hevc|ddp?5[. ]1)\b|\b(?:amzn|nf|dp|atvp|hmax|dsnp)[-_. ]web\b`), "WEBDL"},
		{regexp.MustCompile(`[ .]WEB$`), "WEBDL"},
		{regexp.MustCompile(`(?i)\bhdtv\b`), "HDTV"},
		{regexp.MustCompile(`(?i)\b(?:dvd|dvdrip|ntsc|pal|xvidvd)\b`), "DVD"},
		{regexp.MustCompile(`(?i)\b(?:ws[-_. ]dsr|dsr|pdtv|sdtv|tvrip)\b`), "SDTV"},
	}
	// otherSource is Sonarr's OtherSourceRegex, used only when nothing else
	// gives the source.
	otherSourceHD = regexp.MustCompile(`(?i)hd[-_. ]tv`)
	otherSourceSD = regexp.MustCompile(`(?i)sd[-_. ]tv`)

	// codecPatterns is checked in order. Encoder names (x265/x264) win over
	// the format names (HEVC/AVC) when a title carries both, since they're
	// the more specific of the two. "H 264" with a space is how some
	// indexers render "H.264" in titles.
	codecPatterns = []namedPattern{
		{regexp.MustCompile(`(?i)\bx\.?265\b`), "x265"},
		{regexp.MustCompile(`(?i)\bx\.?264\b`), "x264"},
		{regexp.MustCompile(`(?i)\b(hevc|h[ .]?265)\b`), "HEVC"},
		{regexp.MustCompile(`(?i)\b(avc|h[ .]?264)\b`), "AVC"},
		{regexp.MustCompile(`(?i)\bav1\b`), "AV1"},
		{regexp.MustCompile(`(?i)\bxvid\b`), "XviD"},
		{regexp.MustCompile(`(?i)\bdivx\b`), "DivX"},
		{regexp.MustCompile(`(?i)\bvc-?1\b`), "VC-1"},
		{regexp.MustCompile(`(?i)\bmpeg-?2\b`), "MPEG2"},
	}
)

func firstMatch(patterns []namedPattern, title string) string {
	for _, p := range patterns {
		if p.pattern.MatchString(title) {
			return p.name
		}
	}
	return ""
}

func parseResolution(title string) string {
	if m := resolutionPattern.FindStringSubmatch(title); m != nil {
		for i, name := range resolutionPattern.SubexpNames() {
			if i > 0 && m[i] != "" {
				return strings.TrimPrefix(name, "r") + "p"
			}
		}
	}
	if alternativeResolution.MatchString(title) {
		return "2160p"
	}
	return ""
}

// Parse extracts quality/source/codec/release-group from a release title or
// a file's own name - same input shape either way (a plain string), so
// every import path (grab-based, using the downloaded file's own
// pre-rename name, or library-scan-based, using whatever's found on
// disk) calls this identically.
//
// When the title names a resolution but no source, Sonarr and Radarr treat
// it as HDTV (SDTV below 720p); a bare x264 with neither is SDTV. Parse does
// the same, so such releases get a quality instead of Unknown.
func Parse(title string) FileQuality {
	q := FileQuality{
		Resolution: parseResolution(title),
		Source:     firstMatch(sourcePatterns, title),
		Codec:      firstMatch(codecPatterns, title),
	}
	switch {
	case q.Source == "SDTV" && (q.Resolution == "720p" || q.Resolution == "1080p" || q.Resolution == "2160p"):
		q.Source = "HDTV"
	case q.Source != "":
	case q.Resolution == "480p":
		q.Source = "SDTV"
	case q.Resolution != "":
		q.Source = "HDTV"
	case q.Codec == "x264":
		q.Source = "SDTV"
	case otherSourceHD.MatchString(title):
		q.Source, q.Resolution = "HDTV", "720p"
	case otherSourceSD.MatchString(title):
		q.Source = "SDTV"
	}
	if m := releaseGroupPattern.FindStringSubmatch(strings.TrimSpace(title)); m != nil {
		q.ReleaseGroup = m[1]
	}
	return q
}
