package merge

import (
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
)

// movieSource is one provider's contribution to a movie merge, produced by
// adapt_tmdb.go/adapt_omdb.go. Ratings and ExternalIDs are unioned rather
// than picked-by-priority, since e.g. OMDb's Rotten Tomatoes/Metacritic
// scores and TMDB's vote average are different metrics, not competing
// values for the same field.
//
// SortTitle/CleanTitle are deliberately absent: no provider supplies them,
// they're derived from Title at library-add time by a later "sync service"
// pass (see the project plan's persistence-integration scope boundary),
// not something this merge layer should compute.
type movieSource struct {
	Provider string

	Title, OriginalTitle          string
	Status, Certification, Studio string
	CollectionTitle               string
	Overview                      string
	Year, Runtime                 int
	ReleaseDate                   *time.Time
	Genres, Images                []string

	Ratings     map[string]float64
	ExternalIDs map[string]string
}

// MergeMovie merges any number of movieSources (typically one from TMDB
// and, if available, one from OMDb) into a single MovieMetadata plus the
// Provenance/ExternalID rows a caller should persist via
// internal/store.UpsertExternalID / UpsertFieldProvenance.
func MergeMovie(sources []movieSource, opts Options) (metadata.MovieMetadata, []metadata.Provenance, []metadata.ExternalID) {
	priority := movieFieldPriority(opts)

	titles, originalTitles := map[string]string{}, map[string]string{}
	statuses, certifications, studios, collectionTitles, overviews := map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}
	years, runtimes := map[string]int{}, map[string]int{}
	releaseDates := map[string]*time.Time{}
	genres, images := map[string][]string{}, map[string][]string{}
	ratings := map[string]float64{}
	var externalIDSources []map[string]string

	for _, s := range sources {
		titles[s.Provider] = s.Title
		originalTitles[s.Provider] = s.OriginalTitle
		statuses[s.Provider] = s.Status
		certifications[s.Provider] = s.Certification
		studios[s.Provider] = s.Studio
		collectionTitles[s.Provider] = s.CollectionTitle
		overviews[s.Provider] = s.Overview
		years[s.Provider] = s.Year
		runtimes[s.Provider] = s.Runtime
		releaseDates[s.Provider] = s.ReleaseDate
		genres[s.Provider] = s.Genres
		images[s.Provider] = s.Images
		for k, v := range s.Ratings {
			ratings[k] = v
		}
		externalIDSources = append(externalIDSources, s.ExternalIDs)
	}

	result := metadata.MovieMetadata{
		Title:           pickString(priority["title"], titles),
		OriginalTitle:   pickString(priority["original_title"], originalTitles),
		Status:          pickString(priority["status"], statuses),
		Certification:   pickString(priority["certification"], certifications),
		Studio:          pickString(priority["studio"], studios),
		CollectionTitle: pickString(priority["collection_title"], collectionTitles),
		Overview:        pickString(priority["overview"], overviews),
		Year:            pickInt(priority["release_date"], years),
		Runtime:         pickInt(priority["runtime"], runtimes),
		InCinemas:       pickTime(priority["release_date"], releaseDates),
		Genres:          pickStrings(priority["genres"], genres),
		Images:          pickStrings(priority["images"], images),
		Ratings:         ratings,
		ExternalIDs:     externalIDsToMap(mergeExternalIDs(externalIDSources...)),
	}

	externalIDs := mergeExternalIDs(externalIDSources...)
	provenance := movieProvenance(result)
	return result, provenance, externalIDs
}

func externalIDsToMap(ids []metadata.ExternalID) map[string]string {
	m := make(map[string]string, len(ids))
	for _, id := range ids {
		m[id.Provider] = id.ExternalID
	}
	return m
}

func movieProvenance(m metadata.MovieMetadata) []metadata.Provenance {
	const entity = "movie"
	var out []metadata.Provenance
	fields := []struct {
		name     string
		provider string
	}{
		{"title", m.Title.Provider},
		{"original_title", m.OriginalTitle.Provider},
		{"status", m.Status.Provider},
		{"certification", m.Certification.Provider},
		{"studio", m.Studio.Provider},
		{"collection_title", m.CollectionTitle.Provider},
		{"overview", m.Overview.Provider},
		{"year", m.Year.Provider},
		{"runtime", m.Runtime.Provider},
		{"in_cinemas", m.InCinemas.Provider},
		{"genres", m.Genres.Provider},
		{"images", m.Images.Provider},
	}
	for _, f := range fields {
		if p, ok := provenanceIfSet(entity, f.name, f.provider); ok {
			out = append(out, p)
		}
	}
	return out
}
