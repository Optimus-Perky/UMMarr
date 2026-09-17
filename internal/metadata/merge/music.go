package merge

import (
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
)

// Only MusicBrainz is wired up in this pass (see the project plan's scope
// decision - Discogs/TheAudioDB are deferred), so these priority lists
// have one entry today. They're still expressed the same way as the
// movie/series tables so adding a second music provider later is a
// one-line change, not a redesign.
func defaultArtistFieldPriority() map[string][]string {
	return map[string][]string{
		"name":           {"musicbrainz"},
		"disambiguation": {"musicbrainz"},
		"artist_type":    {"musicbrainz"},
	}
}

func defaultAlbumFieldPriority() map[string][]string {
	return map[string][]string{
		"title":           {"musicbrainz"},
		"disambiguation":  {"musicbrainz"},
		"album_type":      {"musicbrainz"},
		"release_date":    {"musicbrainz"},
		"secondary_types": {"musicbrainz"},
	}
}

// artistSource is one provider's contribution to an artist merge.
type artistSource struct {
	Provider       string
	Name           string
	Disambiguation string
	ArtistType     string
	ExternalIDs    map[string]string
}

// MergeArtist merges any number of artistSources into one ArtistMetadata.
func MergeArtist(sources []artistSource) (metadata.ArtistMetadata, []metadata.Provenance, []metadata.ExternalID) {
	priority := defaultArtistFieldPriority()

	names, disambiguations, types := map[string]string{}, map[string]string{}, map[string]string{}
	var externalIDSources []map[string]string
	for _, s := range sources {
		names[s.Provider] = s.Name
		disambiguations[s.Provider] = s.Disambiguation
		types[s.Provider] = s.ArtistType
		externalIDSources = append(externalIDSources, s.ExternalIDs)
	}

	result := metadata.ArtistMetadata{
		Name:           pickString(priority["name"], names),
		Disambiguation: pickString(priority["disambiguation"], disambiguations),
		ArtistType:     pickString(priority["artist_type"], types),
		ExternalIDs:    externalIDsToMap(mergeExternalIDs(externalIDSources...)),
	}

	const entity = "artist"
	var provenance []metadata.Provenance
	for _, f := range []struct{ name, provider string }{
		{"name", result.Name.Provider},
		{"disambiguation", result.Disambiguation.Provider},
		{"artist_type", result.ArtistType.Provider},
	} {
		if p, ok := provenanceIfSet(entity, f.name, f.provider); ok {
			provenance = append(provenance, p)
		}
	}
	return result, provenance, mergeExternalIDs(externalIDSources...)
}

// albumSource is one provider's contribution to an album merge. Series is
// only ever set by the MusicBrainz adapter (see adapt_musicbrainz.go) -
// it's not a mergeable field, just carried through to the caller so it can
// populate compilation_series/compilation_series_albums directly.
type albumSource struct {
	Provider       string
	Title          string
	Disambiguation string
	AlbumType      string
	SecondaryTypes []string
	ReleaseDate    *time.Time
	ExternalIDs    map[string]string
	Series         *metadata.CompilationSeriesInfo
}

// MergeAlbum merges any number of albumSources into one AlbumMetadata.
func MergeAlbum(sources []albumSource) (metadata.AlbumMetadata, []metadata.Provenance, []metadata.ExternalID) {
	priority := defaultAlbumFieldPriority()

	titles, disambiguations, albumTypes := map[string]string{}, map[string]string{}, map[string]string{}
	secondaryTypes := map[string][]string{}
	releaseDates := map[string]*time.Time{}
	var externalIDSources []map[string]string
	var series *metadata.CompilationSeriesInfo
	for _, s := range sources {
		titles[s.Provider] = s.Title
		disambiguations[s.Provider] = s.Disambiguation
		albumTypes[s.Provider] = s.AlbumType
		secondaryTypes[s.Provider] = s.SecondaryTypes
		releaseDates[s.Provider] = s.ReleaseDate
		externalIDSources = append(externalIDSources, s.ExternalIDs)
		if s.Series != nil {
			series = s.Series
		}
	}

	result := metadata.AlbumMetadata{
		Title:          pickString(priority["title"], titles),
		Disambiguation: pickString(priority["disambiguation"], disambiguations),
		AlbumType:      pickString(priority["album_type"], albumTypes),
		SecondaryTypes: pickStrings(priority["secondary_types"], secondaryTypes),
		ReleaseDate:    pickTime(priority["release_date"], releaseDates),
		ExternalIDs:    externalIDsToMap(mergeExternalIDs(externalIDSources...)),
		Series:         series,
	}

	const entity = "album"
	var provenance []metadata.Provenance
	for _, f := range []struct{ name, provider string }{
		{"title", result.Title.Provider},
		{"disambiguation", result.Disambiguation.Provider},
		{"album_type", result.AlbumType.Provider},
		{"secondary_types", result.SecondaryTypes.Provider},
		{"release_date", result.ReleaseDate.Provider},
	} {
		if p, ok := provenanceIfSet(entity, f.name, f.provider); ok {
			provenance = append(provenance, p)
		}
	}
	return result, provenance, mergeExternalIDs(externalIDSources...)
}
