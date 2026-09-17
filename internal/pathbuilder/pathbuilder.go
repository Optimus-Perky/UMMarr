// Package pathbuilder resolves Radarr/Sonarr/Lidarr-style naming
// templates (e.g. "{Movie Title} ({Release Year})") into actual path
// segments. It has no database or domain knowledge of its own - callers
// (internal/store's Resolve*Path functions) build the token map and
// decide which template applies; this package only does the string
// substitution and filesystem-safety sanitization, the same
// small-reusable-dependency-free shape as internal/titleutil.
package pathbuilder

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// tokenPattern matches "{Token Name}" and the zero-padded numeric form
// "{token:00}" (the padding width is however many zeros appear after the
// colon) - the latter is what {track:00}/{season:00}-style templates use.
var tokenPattern = regexp.MustCompile(`\{([^{}:]+)(?::(0+))?\}`)

// ResolveTemplate substitutes every {Token Name} or {token:00} in
// template with its value from tokens (keyed by the token name exactly as
// it appears between the braces, ignoring any :00 padding suffix). A
// token with no entry in tokens resolves to "" rather than leaving the
// literal placeholder in place - callers control what appears by what
// they put in the map, not by what's in the template.
func ResolveTemplate(template string, tokens map[string]string) string {
	return tokenPattern.ReplaceAllStringFunc(template, func(match string) string {
		groups := tokenPattern.FindStringSubmatch(match)
		name, padding := groups[1], groups[2]
		value := tokens[name]
		if padding == "" {
			return value
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			return value // not numeric - padding doesn't apply, pass through
		}
		return fmt.Sprintf("%0*d", len(padding), n)
	})
}

// ColonReplacement is how a colon is handled when illegal characters are
// replaced rather than removed.
type ColonReplacement string

const (
	ColonDelete         ColonReplacement = "delete"           // "Title: Sub" -> "Title Sub"
	ColonDash           ColonReplacement = "dash"             // "Title: Sub" -> "Title- Sub"
	ColonSpaceDash      ColonReplacement = "space_dash"       // "Title: Sub" -> "Title - Sub"
	ColonSpaceDashSpace ColonReplacement = "space_dash_space" // "Title: Sub" -> "Title - Sub", "10:30" -> "10 - 30"
	ColonSmart          ColonReplacement = "smart"            // "Title: Sub" -> "Title - Sub", "10:30" -> "10-30"
)

// Options controls how SanitizeSegment treats characters Windows can't use
// in a name. The zero value removes them, which is what UMMarr always did
// before this was configurable.
type Options struct {
	ReplaceIllegal bool
	Colon          ColonReplacement
}

// windowsReserved matches characters Windows forbids in a path segment
// (< > : " / \ | ? * and control characters) - sanitized even on a
// non-Windows host, since the library may be shared/backed up cross-
// platform later.
var windowsReserved = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

var controlCharacters = regexp.MustCompile(`[\x00-\x1f]`)

// illegalReplacements is used when Options.ReplaceIllegal is set; colons are
// handled separately by replaceColons.
var illegalReplacements = strings.NewReplacer(
	`\`, "+", "/", "+",
	"<", "", ">", "", `"`, "", "|", "",
	"?", "!", "*", "-",
)

// SanitizeSegment cleans a single path segment (never a full multi-level
// path - split on "/" first for templates like "Various Artists/{Series
// Name}" and sanitize each piece separately, matching how Radarr's own
// FileNameBuilder handles multi-component templates) by removing or
// replacing filesystem-reserved characters, then trimming the trailing
// dots/spaces Windows also disallows.
func SanitizeSegment(s string, opts Options) string {
	var cleaned string
	if opts.ReplaceIllegal {
		cleaned = illegalReplacements.Replace(replaceColons(s, opts.Colon))
		cleaned = controlCharacters.ReplaceAllString(cleaned, "")
	} else {
		cleaned = windowsReserved.ReplaceAllString(s, "")
	}
	return strings.TrimRight(strings.TrimSpace(cleaned), ". ")
}

func replaceColons(s string, mode ColonReplacement) string {
	switch mode {
	case ColonDash:
		return strings.ReplaceAll(s, ":", "-")
	case ColonSpaceDash:
		return strings.ReplaceAll(s, ":", " -")
	case ColonSpaceDashSpace:
		// A colon already followed by a space shouldn't end up with two.
		return strings.ReplaceAll(strings.ReplaceAll(s, ": ", " - "), ":", " - ")
	case ColonSmart:
		return strings.ReplaceAll(strings.ReplaceAll(s, ": ", " - "), ":", "-")
	default:
		return strings.ReplaceAll(s, ":", "")
	}
}

// JoinSegments joins already-sanitized path segments with "/", skipping
// any that are empty after sanitization (e.g. a token that resolved to ""
// and left nothing else in its segment) rather than producing a path with
// an empty component.
func JoinSegments(parts ...string) string {
	nonEmpty := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return strings.Join(nonEmpty, "/")
}

// ResolveTemplatePath resolves a template that may itself contain literal
// "/" separators (e.g. "Various Artists/{Series Name}" is two directory
// levels by design) into a slice of sanitized segments, one per level.
// The template is split on "/" BEFORE token substitution, not after -
// otherwise a resolved token value that happens to contain "/" (e.g. a
// movie title with a slash in it) would incorrectly create extra
// directory levels instead of being sanitized away within its own
// segment.
func ResolveTemplatePath(template string, tokens map[string]string, opts Options) []string {
	rawSegments := strings.Split(template, "/")
	segments := make([]string, 0, len(rawSegments))
	for _, raw := range rawSegments {
		resolved := ResolveTemplate(raw, tokens)
		segments = append(segments, SanitizeSegment(resolved, opts))
	}
	return segments
}
