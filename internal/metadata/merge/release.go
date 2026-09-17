package merge

import (
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
)

// releaseSource is MusicBrainz's contribution to a release merge - the
// only source today, since Discogs (which could add its own tracklist
// data) is deferred. Still expressed the same source/merge shape as the
// other media types so adding a second source later needs no redesign.
type releaseSource struct {
	Provider       string
	Title          string
	Status         string
	Disambiguation string
	Country        []string
	Label          []string
	TrackCount     int
	ReleaseDate    *time.Time
	ExternalIDs    map[string]string
}

// MergeRelease merges any number of releaseSources into one
// ReleaseMetadata. With only one provider wired up, this mostly just
// carries MusicBrainz's data through, but keeps the same priority-table
// shape as the other Merge* functions for consistency and future
// extension.
func MergeRelease(sources []releaseSource) (metadata.ReleaseMetadata, []metadata.Provenance, []metadata.ExternalID) {
	priority := map[string][]string{
		"title":          {"musicbrainz"},
		"status":         {"musicbrainz"},
		"disambiguation": {"musicbrainz"},
		"country":        {"musicbrainz"},
		"label":          {"musicbrainz"},
		"track_count":    {"musicbrainz"},
		"release_date":   {"musicbrainz"},
	}

	titles, statuses, disambiguations := map[string]string{}, map[string]string{}, map[string]string{}
	countries, labels := map[string][]string{}, map[string][]string{}
	trackCounts := map[string]int{}
	releaseDates := map[string]*time.Time{}
	var externalIDSources []map[string]string

	for _, s := range sources {
		titles[s.Provider] = s.Title
		statuses[s.Provider] = s.Status
		disambiguations[s.Provider] = s.Disambiguation
		countries[s.Provider] = s.Country
		labels[s.Provider] = s.Label
		trackCounts[s.Provider] = s.TrackCount
		releaseDates[s.Provider] = s.ReleaseDate
		externalIDSources = append(externalIDSources, s.ExternalIDs)
	}

	result := metadata.ReleaseMetadata{
		Title:          pickString(priority["title"], titles),
		Status:         pickString(priority["status"], statuses),
		Disambiguation: pickString(priority["disambiguation"], disambiguations),
		Country:        pickStrings(priority["country"], countries),
		Label:          pickStrings(priority["label"], labels),
		TrackCount:     pickInt(priority["track_count"], trackCounts),
		ReleaseDate:    pickTime(priority["release_date"], releaseDates),
		ExternalIDs:    externalIDsToMap(mergeExternalIDs(externalIDSources...)),
	}

	const entity = "release"
	var provenance []metadata.Provenance
	for _, f := range []struct{ name, provider string }{
		{"title", result.Title.Provider},
		{"status", result.Status.Provider},
		{"disambiguation", result.Disambiguation.Provider},
		{"country", result.Country.Provider},
		{"label", result.Label.Provider},
		{"track_count", result.TrackCount.Provider},
		{"release_date", result.ReleaseDate.Provider},
	} {
		if p, ok := provenanceIfSet(entity, f.name, f.provider); ok {
			provenance = append(provenance, p)
		}
	}
	return result, provenance, mergeExternalIDs(externalIDSources...)
}

func nonEmptyString(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return []string{s}
}
