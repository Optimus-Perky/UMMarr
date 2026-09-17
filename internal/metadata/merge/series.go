package merge

import (
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
)

// seriesSource is one provider's contribution to a series merge. Like
// movieSource, SortTitle/CleanTitle are deliberately absent - derived at
// library-add time, not supplied by any provider. SeasonNumbers is used
// only for season-list reconciliation (see seasons.go), not merged as a
// field itself.
type seriesSource struct {
	Provider string

	Title, OriginalLanguage, Status, Network string
	Overview                                 string
	Year, Runtime                            int
	FirstAired, LastAired                    *time.Time
	Genres, Images                           []string

	Ratings       map[string]float64
	ExternalIDs   map[string]string
	SeasonNumbers []int
}

// MergeSeries merges any number of seriesSources (typically TMDB and
// TVMaze) into a single SeriesMetadata plus Provenance/ExternalID rows.
// Season/episode merging is separate - see MergeSeasons.
func MergeSeries(sources []seriesSource, opts Options) (metadata.SeriesMetadata, []metadata.Provenance, []metadata.ExternalID) {
	priority := seriesFieldPriority(opts)

	titles, languages, statuses, networks, overviews := map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}
	years, runtimes := map[string]int{}, map[string]int{}
	firstAired, lastAired := map[string]*time.Time{}, map[string]*time.Time{}
	genres, images := map[string][]string{}, map[string][]string{}
	ratings := map[string]float64{}
	var externalIDSources []map[string]string

	for _, s := range sources {
		titles[s.Provider] = s.Title
		languages[s.Provider] = s.OriginalLanguage
		statuses[s.Provider] = s.Status
		networks[s.Provider] = s.Network
		overviews[s.Provider] = s.Overview
		years[s.Provider] = s.Year
		runtimes[s.Provider] = s.Runtime
		firstAired[s.Provider] = s.FirstAired
		lastAired[s.Provider] = s.LastAired
		genres[s.Provider] = s.Genres
		images[s.Provider] = s.Images
		for k, v := range s.Ratings {
			ratings[k] = v
		}
		externalIDSources = append(externalIDSources, s.ExternalIDs)
	}

	result := metadata.SeriesMetadata{
		Title:            pickString(priority["title"], titles),
		OriginalLanguage: pickString(priority["original_language"], languages),
		Status:           pickString(priority["status"], statuses),
		Network:          pickString(priority["network"], networks),
		Overview:         pickString(priority["overview"], overviews),
		Year:             pickInt(priority["first_aired"], years),
		Runtime:          pickInt(priority["runtime"], runtimes),
		FirstAired:       pickTime(priority["first_aired"], firstAired),
		LastAired:        pickTime(priority["last_aired"], lastAired),
		Genres:           pickStrings(priority["genres"], genres),
		Images:           pickStrings(priority["images"], images),
		Ratings:          ratings,
		ExternalIDs:      externalIDsToMap(mergeExternalIDs(externalIDSources...)),
	}

	provenance := seriesProvenance(result)
	return result, provenance, mergeExternalIDs(externalIDSources...)
}

func seriesProvenance(s metadata.SeriesMetadata) []metadata.Provenance {
	const entity = "series"
	var out []metadata.Provenance
	fields := []struct {
		name     string
		provider string
	}{
		{"title", s.Title.Provider},
		{"original_language", s.OriginalLanguage.Provider},
		{"status", s.Status.Provider},
		{"network", s.Network.Provider},
		{"overview", s.Overview.Provider},
		{"year", s.Year.Provider},
		{"runtime", s.Runtime.Provider},
		{"first_aired", s.FirstAired.Provider},
		{"last_aired", s.LastAired.Provider},
		{"genres", s.Genres.Provider},
		{"images", s.Images.Provider},
	}
	for _, f := range fields {
		if p, ok := provenanceIfSet(entity, f.name, f.provider); ok {
			out = append(out, p)
		}
	}
	return out
}
