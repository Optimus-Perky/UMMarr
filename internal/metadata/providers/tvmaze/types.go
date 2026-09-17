package tvmaze

// Externals matches TVMaze's "externals" object, exposing cross-references
// to IMDb/TheTVDB/TVRage even though TVMaze itself needs no API key - a
// free way to get an imdb_id without going through TMDB or TVDB.
type Externals struct {
	TVRage  *int    `json:"tvrage"`
	TheTVDB *int    `json:"thetvdb"`
	IMDb    *string `json:"imdb"`
}

// Show is the body of GET /shows/{id} (and one entry's "show" field in
// /search/shows results).
type Show struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Summary   string    `json:"summary"` // HTML-formatted
	Premiered string    `json:"premiered"`
	Ended     string    `json:"ended"`
	Status    string    `json:"status"`
	Genres    []string  `json:"genres"`
	Externals Externals `json:"externals"`
	Image     *Image    `json:"image"`
}

// Image matches TVMaze's {medium, original} image URL pair.
type Image struct {
	Medium   string `json:"medium"`
	Original string `json:"original"`
}

// SearchResult is one entry in a /search/shows response - TVMaze wraps
// each match with a relevance score alongside the show itself.
type SearchResult struct {
	Score float64 `json:"score"`
	Show  Show    `json:"show"`
}

// Episode is one entry in a /shows/{id}/episodes response.
type Episode struct {
	Season  int    `json:"season"`
	Number  int    `json:"number"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Airdate string `json:"airdate"`
	Runtime int    `json:"runtime"`
}
