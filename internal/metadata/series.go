package metadata

import "time"

// SeriesMetadata mirrors series_metadata's columns (migration 00005).
type SeriesMetadata struct {
	Title            Field[string]
	SortTitle        Field[string]
	CleanTitle       Field[string]
	Status           Field[string]
	Overview         Field[string]
	Network          Field[string]
	AirTime          Field[string]
	SeriesType       Field[string]
	Certification    Field[string]
	Year             Field[int]
	FirstAired       Field[*time.Time]
	LastAired        Field[*time.Time]
	Runtime          Field[int]
	OriginalLanguage Field[string]
	Genres           Field[[]string]
	Images           Field[[]string]
	Ratings          map[string]float64

	ExternalIDs map[string]string

	Seasons []SeasonMetadata
}

// SeasonMetadata mirrors one seasons row (migration 00005), plus its
// episodes. SeasonNumber is not a Field[T] - it's the join key providers
// are unioned on, not a mergeable value.
type SeasonMetadata struct {
	SeasonNumber int
	Monitored    bool
	Poster       string // the season's poster URL, "" when none
	Episodes     []EpisodeMetadata
}

// EpisodeMetadata mirrors one episodes row (migration 00005).
type EpisodeMetadata struct {
	EpisodeNumber         int
	Title                 Field[string]
	AirDate               Field[*time.Time]
	Overview              Field[string]
	AbsoluteEpisodeNumber Field[int]
	Runtime               Field[int]
}
