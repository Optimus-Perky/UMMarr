// Package merge combines metadata fetched from more than one provider
// (internal/metadata/providers/*) into a single record per movie/series/
// artist, recording which provider supplied each field. See
// internal/metadata for the merged result types.
package merge

// Options overrides the default field-priority tables. A field not
// present in the override map falls back to the default for that media
// type. Priority lists name providers in preferred order - the first
// provider in the list that actually returned a non-empty value for that
// field wins.
type Options struct {
	MovieFieldPriority   map[string][]string
	SeriesFieldPriority  map[string][]string
	EpisodeFieldPriority map[string][]string
	// EpisodeListPriority overrides which provider decides a season's
	// episodes (see EpisodeListPriority).
	EpisodeListPriority []string
}

// DefaultMovieFieldPriority prefers TMDB for everything except ratings,
// where OMDb's Rotten Tomatoes/Metacritic scores are the reason it's
// included at all.
func DefaultMovieFieldPriority() map[string][]string {
	return map[string][]string{
		"title":            {"tmdb", "omdb"},
		"overview":         {"tmdb", "omdb"},
		"genres":           {"tmdb", "omdb"},
		"images":           {"tmdb"},
		"runtime":          {"tmdb", "omdb"},
		"release_date":     {"tmdb", "omdb"},
		"original_title":   {"tmdb"},
		"status":           {"tmdb"},
		"studio":           {"tmdb"},
		"collection_title": {"tmdb"},
	}
}

// DefaultSeriesFieldPriority puts TMDB first for every field and TVMaze
// last, as a fallback for what TMDB did not answer.
//
// TVMaze used to lead on scheduling fields, on the reasoning that it
// tracks current-season air dates more promptly. It also numbers episodes
// differently: where a broadcaster airs a double-length premiere, TVMaze
// often lists it as two episodes, which shifts every later episode of
// that season by one. Letting it lead put its numbering into the library.
func DefaultSeriesFieldPriority() map[string][]string {
	return map[string][]string{
		"title":       {"tmdb", "tvmaze", "tvdb"},
		"overview":    {"tmdb", "tvmaze", "tvdb"},
		"genres":      {"tmdb", "tvmaze", "tvdb"},
		"images":      {"tmdb", "tvmaze", "tvdb"},
		"status":      {"tmdb", "tvmaze", "tvdb"},
		"network":     {"tmdb", "tvmaze", "tvdb"},
		"runtime":     {"tmdb", "tvmaze", "tvdb"},
		"first_aired": {"tmdb", "tvmaze", "tvdb"},
		"last_aired":  {"tmdb", "tvmaze", "tvdb"},
	}
}

// DefaultEpisodeFieldPriority mirrors the series-level order: TMDB first,
// TVMaze only for what TMDB left empty.
func DefaultEpisodeFieldPriority() map[string][]string {
	return map[string][]string{
		"title":    {"tmdb", "tvmaze", "tvdb"},
		"overview": {"tmdb", "tvmaze", "tvdb"},
		"air_date": {"tmdb", "tvmaze", "tvdb"},
		"runtime":  {"tmdb", "tvmaze", "tvdb"},
	}
}

// EpisodeListPriority orders the providers allowed to decide which
// episodes a season HAS, as opposed to what their fields say. The first
// one that returned any episode for a season defines that season's
// episode numbers; the rest only fill in fields for those episodes.
//
// Without this the lists were unioned, so a provider that splits one
// broadcast into two entries added an episode nobody has - it showed as a
// duplicate title next to the real finale, left the season permanently
// short of complete, and was searched for forever. Shrinking season 3,
// found 2026-09-16: TMDB listed 11 episodes, TVMaze 12, and the extra was
// TMDB's episode 11 over again.
func EpisodeListPriority() []string {
	return []string{"tmdb", "tvmaze", "tvdb"}
}

func movieFieldPriority(opts Options) map[string][]string {
	return withDefaults(opts.MovieFieldPriority, DefaultMovieFieldPriority())
}

func seriesFieldPriority(opts Options) map[string][]string {
	return withDefaults(opts.SeriesFieldPriority, DefaultSeriesFieldPriority())
}

func withDefaults(overrides, defaults map[string][]string) map[string][]string {
	if len(overrides) == 0 {
		return defaults
	}
	merged := make(map[string][]string, len(defaults))
	for k, v := range defaults {
		merged[k] = v
	}
	for k, v := range overrides {
		merged[k] = v
	}
	return merged
}
