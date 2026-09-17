package tmdb

// Genre matches TMDB's {id, name} genre objects.
type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// ExternalIDs matches the "external_ids" sub-object returned when a
// movie/TV request includes append_to_response=external_ids.
type ExternalIDs struct {
	IMDbID string `json:"imdb_id"`
}

// MovieSearchResult is one entry in a /search/movie response.
type MovieSearchResult struct {
	ID          int     `json:"id"`
	Title       string  `json:"title"`
	Overview    string  `json:"overview"`
	ReleaseDate string  `json:"release_date"`
	PosterPath  string  `json:"poster_path"`
	VoteAverage float64 `json:"vote_average"`
}

// SearchMovieResponse is the body of GET /search/movie.
type SearchMovieResponse struct {
	Page         int                 `json:"page"`
	Results      []MovieSearchResult `json:"results"`
	TotalPages   int                 `json:"total_pages"`
	TotalResults int                 `json:"total_results"`
}

// ProductionCompany is one entry in Movie.ProductionCompanies.
type ProductionCompany struct {
	Name string `json:"name"`
}

// Collection matches Movie.BelongsToCollection when the movie is part of
// one (e.g. a franchise).
type Collection struct {
	Name string `json:"name"`
}

// Movie is the body of GET /movie/{id}?append_to_response=external_ids.
type Movie struct {
	ID                  int                 `json:"id"`
	Title               string              `json:"title"`
	OriginalTitle       string              `json:"original_title"`
	Overview            string              `json:"overview"`
	ReleaseDate         string              `json:"release_date"`
	Runtime             int                 `json:"runtime"`
	Status              string              `json:"status"`
	Genres              []Genre             `json:"genres"`
	PosterPath          string              `json:"poster_path"`
	BackdropPath        string              `json:"backdrop_path"`
	VoteAverage         float64             `json:"vote_average"`
	ProductionCompanies []ProductionCompany `json:"production_companies"`
	BelongsToCollection *Collection         `json:"belongs_to_collection"`
	ExternalIDs         *ExternalIDs        `json:"external_ids"`
}

// SeriesSearchResult is one entry in a /search/tv response.
type SeriesSearchResult struct {
	ID           int     `json:"id"`
	Name         string  `json:"name"`
	Overview     string  `json:"overview"`
	FirstAirDate string  `json:"first_air_date"`
	PosterPath   string  `json:"poster_path"`
	VoteAverage  float64 `json:"vote_average"`
}

// SearchSeriesResponse is the body of GET /search/tv.
type SearchSeriesResponse struct {
	Page         int                  `json:"page"`
	Results      []SeriesSearchResult `json:"results"`
	TotalPages   int                  `json:"total_pages"`
	TotalResults int                  `json:"total_results"`
}

// SeasonSummary is one entry in Series.Seasons.
type SeasonSummary struct {
	SeasonNumber int    `json:"season_number"`
	EpisodeCount int    `json:"episode_count"`
	Name         string `json:"name"`
}

// Network is one entry in Series.Networks.
type Network struct {
	Name string `json:"name"`
}

// Series is the body of GET /tv/{id}?append_to_response=external_ids.
type Series struct {
	ID               int             `json:"id"`
	Name             string          `json:"name"`
	Overview         string          `json:"overview"`
	FirstAirDate     string          `json:"first_air_date"`
	LastAirDate      string          `json:"last_air_date"`
	Status           string          `json:"status"`
	OriginalLanguage string          `json:"original_language"`
	Genres           []Genre         `json:"genres"`
	PosterPath       string          `json:"poster_path"`
	VoteAverage      float64         `json:"vote_average"`
	Networks         []Network       `json:"networks"`
	EpisodeRunTime   []int           `json:"episode_run_time"`
	Seasons          []SeasonSummary `json:"seasons"`
	ExternalIDs      *ExternalIDs    `json:"external_ids"`
}

// Episode is one entry in a Season's Episodes.
type Episode struct {
	EpisodeNumber int    `json:"episode_number"`
	Name          string `json:"name"`
	Overview      string `json:"overview"`
	AirDate       string `json:"air_date"`
	Runtime       int    `json:"runtime"`
}

// Season is the body of GET /tv/{id}/season/{season_number}.
type Season struct {
	SeasonNumber int       `json:"season_number"`
	PosterPath   string    `json:"poster_path"`
	Name         string    `json:"name"`
	Episodes     []Episode `json:"episodes"`
}
