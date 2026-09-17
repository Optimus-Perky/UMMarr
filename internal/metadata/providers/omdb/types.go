package omdb

// Rating is one entry in OMDb's "Ratings" array - its main value over
// TMDB, since it includes Rotten Tomatoes and Metacritic scores TMDB
// doesn't have natively.
type Rating struct {
	Source string `json:"Source"`
	Value  string `json:"Value"`
}

// Response is the body of a successful lookup (by IMDb id or by title).
// OMDb encodes "not found" as {"Response":"False","Error":"..."} rather
// than an HTTP error status, hence the Response/Error fields here.
type Response struct {
	Title    string   `json:"Title"`
	Year     string   `json:"Year"`
	Runtime  string   `json:"Runtime"`
	Genre    string   `json:"Genre"` // comma-separated
	Plot     string   `json:"Plot"`
	Poster   string   `json:"Poster"`
	ImdbID   string   `json:"imdbID"`
	Ratings  []Rating `json:"Ratings"`
	Response string   `json:"Response"`
	Error    string   `json:"Error"`
}
