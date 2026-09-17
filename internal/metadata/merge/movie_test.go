package merge

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/omdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// TestMergeMovie_TMDBAndOMDb covers verification item 1: fixture TMDB+OMDb
// responses merge into one MovieMetadata, both external_ids rows (plus
// imdb, extracted from TMDB's external_ids sub-object) get written to a
// real SQLite DB, and metadata_field_provenance correctly attributes
// overview to tmdb and ratings to omdb per the default priority table.
func TestMergeMovie_TMDBAndOMDb(t *testing.T) {
	tmdbMovie := &tmdb.Movie{
		ID:          27205,
		Title:       "Inception",
		Overview:    "A thief who steals corporate secrets through dream-sharing technology...",
		ReleaseDate: "2010-07-15",
		Runtime:     148,
		Status:      "Released",
		Genres:      []tmdb.Genre{{ID: 28, Name: "Action"}, {ID: 878, Name: "Science Fiction"}},
		PosterPath:  "/poster.jpg",
		VoteAverage: 8.4,
		ExternalIDs: &tmdb.ExternalIDs{IMDbID: "tt1375666"},
	}
	omdbResp := &omdb.Response{
		Title:  "Inception",
		Year:   "2010",
		ImdbID: "tt1375666",
		Ratings: []omdb.Rating{
			{Source: "Internet Movie Database", Value: "8.8/10"},
			{Source: "Rotten Tomatoes", Value: "87%"},
			{Source: "Metacritic", Value: "74/100"},
		},
		Response: "True",
	}

	sources := []movieSource{adaptTMDBMovie(tmdbMovie), adaptOMDbMovie(omdbResp)}
	merged, provenance, externalIDs := MergeMovie(sources, Options{})

	if merged.Overview.Value == "" || merged.Overview.Provider != "tmdb" {
		t.Fatalf("want overview from tmdb, got %+v", merged.Overview)
	}
	if merged.Ratings["rotten_tomatoes"] != 87 {
		t.Fatalf("want rotten_tomatoes rating 87, got %v", merged.Ratings["rotten_tomatoes"])
	}
	if merged.Ratings["tmdb"] != 8.4 {
		t.Fatalf("want tmdb rating 8.4, got %v", merged.Ratings["tmdb"])
	}

	idsByProvider := map[string]string{}
	for _, id := range externalIDs {
		idsByProvider[id.Provider] = id.ExternalID
	}
	for _, want := range []string{"tmdb", "omdb", "imdb"} {
		if _, ok := idsByProvider[want]; !ok {
			t.Fatalf("want external id for provider %s, got %v", want, idsByProvider)
		}
	}

	provenanceByField := map[string]string{}
	for _, p := range provenance {
		provenanceByField[p.FieldName] = p.Provider
	}
	if provenanceByField["overview"] != "tmdb" {
		t.Fatalf("want overview provenance tmdb, got %s", provenanceByField["overview"])
	}

	// Persist and verify against the real schema.
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	res, err := db.ExecContext(ctx, `INSERT INTO movie_metadata (title, sort_title, clean_title, year) VALUES (?, ?, ?, ?)`,
		merged.Title.Value, merged.Title.Value, merged.Title.Value, merged.Year.Value)
	if err != nil {
		t.Fatalf("insert movie_metadata: %v", err)
	}
	movieID, _ := res.LastInsertId()

	for _, id := range externalIDs {
		if err := store.UpsertExternalID(ctx, db, "movie", movieID, id.Provider, id.ExternalID); err != nil {
			t.Fatalf("upsert external_id %s: %v", id.Provider, err)
		}
	}
	for _, p := range provenance {
		if err := store.UpsertFieldProvenance(ctx, db, p.EntityType, movieID, p.FieldName, p.Provider); err != nil {
			t.Fatalf("upsert provenance %s: %v", p.FieldName, err)
		}
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM external_ids WHERE entity_type = 'movie' AND entity_id = ?`, movieID).Scan(&count); err != nil {
		t.Fatalf("count external_ids: %v", err)
	}
	if count != 3 {
		t.Fatalf("want 3 external_ids rows, got %d", count)
	}

	var overviewProvider string
	if err := db.QueryRowContext(ctx, `SELECT provider FROM metadata_field_provenance WHERE entity_type = 'movie' AND entity_id = ? AND field_name = 'overview'`, movieID).Scan(&overviewProvider); err != nil {
		t.Fatalf("query overview provenance: %v", err)
	}
	if overviewProvider != "tmdb" {
		t.Fatalf("want overview provenance row 'tmdb', got %s", overviewProvider)
	}

	// Re-upserting the same data must not error or duplicate rows -
	// importers/refreshes rely on this being idempotent.
	for _, id := range externalIDs {
		if err := store.UpsertExternalID(ctx, db, "movie", movieID, id.Provider, id.ExternalID); err != nil {
			t.Fatalf("re-upsert external_id %s: %v", id.Provider, err)
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM external_ids WHERE entity_type = 'movie' AND entity_id = ?`, movieID).Scan(&count); err != nil {
		t.Fatalf("re-count external_ids: %v", err)
	}
	if count != 3 {
		t.Fatalf("want still 3 external_ids rows after re-upsert, got %d", count)
	}
}

// TestMergeMovie_TMDBOnly confirms a single-provider merge (OMDb key not
// configured, or lookup failed) still produces a sensible result rather
// than requiring both sources.
func TestMergeMovie_TMDBOnly(t *testing.T) {
	tmdbMovie := &tmdb.Movie{
		ID:          27205,
		Title:       "Inception",
		Overview:    "A thief...",
		ReleaseDate: "2010-07-15",
		VoteAverage: 8.4,
	}
	merged, _, externalIDs := MergeMovie([]movieSource{adaptTMDBMovie(tmdbMovie)}, Options{})
	if merged.Title.Value != "Inception" || merged.Title.Provider != "tmdb" {
		t.Fatalf("want title from tmdb, got %+v", merged.Title)
	}
	if len(externalIDs) != 1 || externalIDs[0].Provider != "tmdb" {
		t.Fatalf("want only tmdb external id, got %v", externalIDs)
	}
}
