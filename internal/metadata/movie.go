package metadata

import "time"

// MovieMetadata is the merged result for one movie, shaped 1:1 with the
// movie_metadata table's columns (see internal/store/migrations/00004).
// Ratings is a map since more than one provider can contribute a rating
// under its own key (e.g. "tmdb" vote average alongside OMDb's Rotten
// Tomatoes/Metacritic scores) rather than one field overwriting another.
type MovieMetadata struct {
	Title           Field[string]
	SortTitle       Field[string]
	CleanTitle      Field[string]
	OriginalTitle   Field[string]
	Status          Field[string]
	Year            Field[int]
	Runtime         Field[int]
	InCinemas       Field[*time.Time]
	PhysicalRelease Field[*time.Time]
	DigitalRelease  Field[*time.Time]
	Certification   Field[string]
	Overview        Field[string]
	Studio          Field[string]
	CollectionTitle Field[string]
	Genres          Field[[]string]
	Images          Field[[]string]

	// Ratings holds one entry per contributing provider, e.g.
	// {"tmdb": 8.1, "rotten_tomatoes": 87, "metacritic": 74}. Not a single
	// Field[T] because multiple providers' ratings coexist rather than one
	// replacing another - see merge/priority.go.
	Ratings map[string]float64

	ExternalIDs map[string]string // provider -> external id
}
