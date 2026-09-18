package metadata

import "time"

// ArtistMetadata mirrors artist_metadata's columns (migration 00006).
type ArtistMetadata struct {
	Name           Field[string]
	CleanName      Field[string]
	SortName       Field[string]
	Overview       Field[string]
	Disambiguation Field[string]
	ArtistType     Field[string]
	Status         Field[string]
	Genres         Field[[]string]
	Images         Field[[]string]
	Ratings        map[string]float64

	ExternalIDs map[string]string
}

// AlbumMetadata mirrors albums' columns (migration 00006) - the
// MusicBrainz "release-group" equivalent.
type AlbumMetadata struct {
	Title          Field[string]
	CleanTitle     Field[string]
	Disambiguation Field[string]
	Overview       Field[string]
	ReleaseDate    Field[*time.Time]
	AlbumType      Field[string] // Album/EP/Single/Broadcast/Other
	SecondaryTypes Field[[]string]
	Genres         Field[[]string]
	Images         Field[[]string]
	Ratings        map[string]float64

	ExternalIDs map[string]string

	// Series is set only when the release-group is linked to a
	// MusicBrainz Series (e.g. "Now That's What I Call Music") - see
	// providers/musicbrainz/series.go. Nil for anything else, including
	// Various Artists albums MusicBrainz hasn't modeled as part of a
	// series (those still go under compilation_series with
	// source='manual', just not populated from here).
	Series *CompilationSeriesInfo
}

// CompilationSeriesInfo is what a MusicBrainz series-relationship lookup
// yields, shaped to populate compilation_series/compilation_series_albums
// (migration 00008) directly.
type CompilationSeriesInfo struct {
	Name                string
	MusicBrainzSeriesID string
	SequenceNumber      int
}

// ReleaseMetadata mirrors album_releases' columns (migration 00006/00007).
type ReleaseMetadata struct {
	Title          Field[string]
	Status         Field[string]
	Disambiguation Field[string]
	Country        Field[[]string]
	Label          Field[[]string]
	TrackCount     Field[int]
	ReleaseDate    Field[*time.Time]

	ExternalIDs map[string]string
}

// ArtistCreditRef names one contributing artist on a track - e.g. the
// real performing artist on one track of a Various Artists compilation,
// independent of the album's own (Various Artists) artist.
type ArtistCreditRef struct {
	Name                string
	MusicBrainzArtistID string
}

// TrackSource is one track's data carried through from a provider, ready
// to persist. Unlike the *Metadata types above, this isn't a Field[T]-
// merged shape: there's only ever one data source per track today (no
// competing providers to reconcile), just a 1:1 passthrough list.
type TrackSource struct {
	// MBID is the track id MusicBrainz gives this track ON THIS RELEASE -
	// what Picard writes into a file as MUSICBRAINZ_RELEASETRACKID, so a
	// tagged file can be matched to its exact track instead of by position.
	MBID          string
	Number        string
	Title         string
	DurationMs    int
	MediumNumber  int
	ArtistCredits []ArtistCreditRef
}
