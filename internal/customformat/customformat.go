// Package customformat is Radarr's and Sonarr's custom formats: named sets
// of conditions a release either meets or doesn't, scored per quality
// profile.
package customformat

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
)

// Condition kinds, named as in Radarr and Sonarr so exported JSON reads the same.
const (
	ReleaseTitle    = "ReleaseTitleSpecification"
	ReleaseGroup    = "ReleaseGroupSpecification"
	Edition         = "EditionSpecification"
	Source          = "SourceSpecification"
	Resolution      = "ResolutionSpecification"
	QualityModifier = "QualityModifierSpecification"
	Size            = "SizeSpecification"
	IndexerFlag     = "IndexerFlagSpecification"
	Language        = "LanguageSpecification"
	ReleaseType     = "ReleaseTypeSpecification"
)

// Kinds lists the conditions a format can have, for the editor.
var Kinds = []struct{ Key, Label, Help string }{
	{ReleaseTitle, "Release Title", "Regular expression matched against the release name"},
	{ReleaseGroup, "Release Group", "Regular expression matched against the release group"},
	{Edition, "Edition", "Regular expression matched against the release name, for editions such as Director's Cut"},
	{Source, "Source", "Where the release came from"},
	{Resolution, "Resolution", "The video resolution"},
	{QualityModifier, "Quality Modifier", "REMUX"},
	{Size, "Size", "Release size between a minimum and maximum, in GB"},
	{IndexerFlag, "Indexer Flag", "A flag the indexer set, such as Freeleech"},
	{Language, "Language", "An audio language in the release name"},
	{ReleaseType, "Release Type", "TV only: a single episode, several episodes or a season pack"},
}

// Choices are the values a list condition can take.
var Choices = map[string][]string{
	Source:          {"Bluray", "Remux", "WEBDL", "WEBRip", "HDTV", "SDTV", "DVD"},
	Resolution:      {"2160p", "1080p", "720p", "576p", "480p"},
	QualityModifier: {"REMUX"},
	IndexerFlag:     flagNames(),
	Language:        releaseparse.KnownLanguages(),
	ReleaseType:     {"SingleEpisode", "MultiEpisode", "SeasonPack"},
}

func flagNames() []string {
	out := make([]string, 0, len(newznab.FlagNames))
	for _, f := range newznab.FlagNames {
		out = append(out, f.Name)
	}
	return out
}

// Condition is one test a release is put to.
type Condition struct {
	Name           string  `json:"name"`
	Implementation string  `json:"implementation"`
	Negate         bool    `json:"negate"`
	Required       bool    `json:"required"`
	Value          string  `json:"value,omitempty"`
	Min            float64 `json:"min,omitempty"` // Size, GB
	Max            float64 `json:"max,omitempty"`
}

// Format is a custom format.
type Format struct {
	ID                  int64
	Name                string
	IncludeWhenRenaming bool
	Conditions          []Condition
}

// Release is what a format is judged against.
type Release struct {
	Title     string
	Quality   releaseparse.FileQuality
	Size      int64 // bytes; 0 when unknown
	Flags     newznab.Flags
	Languages []string
	// ReleaseType is "SingleEpisode", "MultiEpisode" or "SeasonPack" for
	// TV, "" otherwise.
	ReleaseType string
}

// ReleaseFrom describes an indexer release.
func ReleaseFrom(r newznab.Release) Release {
	in := Release{Title: r.Title, Quality: releaseparse.Parse(r.Title), Size: r.Size, Flags: r.Flags, Languages: releaseparse.Languages(r.Title)}
	if info, ok := releaseparse.ParseEpisode(r.Title); ok {
		switch {
		case info.FullSeason:
			in.ReleaseType = "SeasonPack"
		case len(info.Episodes) > 1:
			in.ReleaseType = "MultiEpisode"
		default:
			in.ReleaseType = "SingleEpisode"
		}
	}
	return in
}

var regexCache sync.Map

func compile(pattern string) (*regexp.Regexp, error) {
	if re, ok := regexCache.Load(pattern); ok {
		return re.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, err
	}
	regexCache.Store(pattern, re)
	return re, nil
}

// Validate checks a condition can be used.
func (c Condition) Validate() error {
	switch c.Implementation {
	case ReleaseTitle, ReleaseGroup, Edition:
		if strings.TrimSpace(c.Value) == "" {
			return fmt.Errorf("%s: enter a regular expression", c.label())
		}
		if _, err := compile(convertPattern(c.Value)); err != nil {
			return fmt.Errorf("%s: %v", c.label(), err)
		}
	case Size:
		if c.Min < 0 || c.Max < 0 || (c.Max > 0 && c.Min > c.Max) {
			return fmt.Errorf("%s: the minimum must be below the maximum", c.label())
		}
	case Source, Resolution, QualityModifier, IndexerFlag, Language, ReleaseType:
		for _, v := range Choices[c.Implementation] {
			if v == c.Value {
				return nil
			}
		}
		return fmt.Errorf("%s: choose a value", c.label())
	default:
		return fmt.Errorf("unknown condition %q", c.Implementation)
	}
	return nil
}

func (c Condition) label() string {
	if c.Name != "" {
		return c.Name
	}
	return c.Implementation
}

// convertPattern is where a Radarr (.NET) pattern would be adapted for Go.
// Most TRaSH patterns are plain groups and \b and work unchanged; Go has no
// lookahead or lookbehind, so patterns using them fail to compile and are
// refused when saved or imported.
func convertPattern(p string) string { return p }

// satisfied is whether the release meets the condition, before Negate.
func (c Condition) satisfied(r Release) bool {
	switch c.Implementation {
	case ReleaseTitle, Edition:
		re, err := compile(convertPattern(c.Value))
		return err == nil && re.MatchString(r.Title)
	case ReleaseGroup:
		re, err := compile(convertPattern(c.Value))
		return err == nil && r.Quality.ReleaseGroup != "" && re.MatchString(r.Quality.ReleaseGroup)
	case Source:
		switch c.Value {
		case "Bluray":
			return r.Quality.Source == "Bluray" || r.Quality.Source == "Remux"
		case "SDTV":
			return r.Quality.Source == "SDTV" || (r.Quality.Source == "HDTV" && r.Quality.Resolution == "480p")
		}
		return strings.EqualFold(r.Quality.Source, c.Value)
	case Resolution:
		return strings.EqualFold(r.Quality.Resolution, c.Value)
	case QualityModifier:
		return c.Value == "REMUX" && r.Quality.Source == "Remux"
	case Size:
		if r.Size <= 0 {
			return false
		}
		gb := float64(r.Size) / (1 << 30)
		return gb > c.Min && (c.Max <= 0 || gb <= c.Max)
	case IndexerFlag:
		for _, f := range newznab.FlagNames {
			if f.Name == c.Value {
				return r.Flags&f.Flag != 0
			}
		}
	case Language:
		for _, l := range r.Languages {
			if strings.EqualFold(l, c.Value) {
				return true
			}
		}
	case ReleaseType:
		return r.ReleaseType == c.Value
	}
	return false
}

// Matches follows Radarr's rule: conditions are grouped by kind; within a
// kind every required condition must pass and, unless they are all
// required, at least one must pass. A format with no conditions never
// matches.
func (f Format) Matches(r Release) bool {
	if len(f.Conditions) == 0 {
		return false
	}
	groups := map[string][]Condition{}
	for _, c := range f.Conditions {
		groups[c.Implementation] = append(groups[c.Implementation], c)
	}
	for _, conds := range groups {
		allRequired, anyPassed := true, false
		for _, c := range conds {
			passed := c.satisfied(r) != c.Negate
			if c.Required && !passed {
				return false
			}
			if !c.Required {
				allRequired = false
			}
			anyPassed = anyPassed || passed
		}
		if !allRequired && !anyPassed {
			return false
		}
	}
	return true
}

// Matching returns the formats r matches.
func Matching(formats []Format, r Release) []Format {
	var out []Format
	for _, f := range formats {
		if f.Matches(r) {
			out = append(out, f)
		}
	}
	return out
}
