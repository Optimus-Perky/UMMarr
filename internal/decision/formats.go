package decision

import (
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/customformat"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
)

// formatScore is the custom formats r matches and what p scores them.
func (e *Engine) formatScore(p Profile, r customformat.Release) (names []string, score int) {
	for _, f := range customformat.Matching(e.Formats, r) {
		names = append(names, f.Name)
		score += p.FormatScores[f.ID]
	}
	return names, score
}

// customFormatRule scores a release's custom formats and rejects it below the
// profile's minimum, as Radarr's CustomFormatAllowedbyProfileSpecification.
func (e *Engine) customFormatRule(d *Decision, p Profile, r newznab.Release) {
	if len(e.Formats) == 0 {
		return
	}
	d.CustomFormats, d.CustomFormatScore = e.formatScore(p, customformat.ReleaseFrom(r))
	if d.CustomFormatScore < p.MinFormatScore {
		names := "None"
		if len(d.CustomFormats) > 0 {
			names = strings.Join(d.CustomFormats, ", ")
		}
		d.reject("Custom Formats %s have score %d below the quality profile's minimum %d", names, d.CustomFormatScore, p.MinFormatScore)
	}
}

// fileFormatScore is what the profile scores an existing file's formats, read
// from the release name it was imported from.
func (e *Engine) fileFormatScore(p Profile, currentRelease string) int {
	if currentRelease == "" || len(e.Formats) == 0 {
		return 0
	}
	_, score := e.formatScore(p, customformat.ReleaseFrom(newznab.Release{Title: currentRelease}))
	return score
}
