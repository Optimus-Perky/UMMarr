package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestUpsertMovieMetadata_IdempotentByExternalID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	m := metadata.MovieMetadata{
		Title:       metadata.Field[string]{Value: "Inception", Provider: "tmdb"},
		Overview:    metadata.Field[string]{Value: "First overview", Provider: "tmdb"},
		Year:        metadata.Field[int]{Value: 2010, Provider: "tmdb"},
		ExternalIDs: map[string]string{"tmdb": "27205", "imdb": "tt1375666"},
	}

	id1, err := store.UpsertMovieMetadata(ctx, db, m)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	m.Overview = metadata.Field[string]{Value: "Updated overview", Provider: "tmdb"}
	id2, err := store.UpsertMovieMetadata(ctx, db, m)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("want same metadata id on re-sync, got %d then %d", id1, id2)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM movie_metadata`).Scan(&count); err != nil {
		t.Fatalf("count movie_metadata: %v", err)
	}
	if count != 1 {
		t.Fatalf("want 1 movie_metadata row after two upserts, got %d", count)
	}

	var overview, cleanTitle, sortTitle string
	if err := db.QueryRowContext(ctx, `SELECT overview, clean_title, sort_title FROM movie_metadata WHERE id = ?`, id1).Scan(&overview, &cleanTitle, &sortTitle); err != nil {
		t.Fatalf("query movie_metadata: %v", err)
	}
	if overview != "Updated overview" {
		t.Fatalf("want updated overview persisted, got %q", overview)
	}
	if cleanTitle != "inception" || sortTitle != "Inception" {
		t.Fatalf("want computed clean_title/sort_title, got %q/%q", cleanTitle, sortTitle)
	}

	var extIDCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM external_ids WHERE entity_type = 'movie' AND entity_id = ?`, id1).Scan(&extIDCount); err != nil {
		t.Fatalf("count external_ids: %v", err)
	}
	if extIDCount != 2 {
		t.Fatalf("want 2 external_ids rows (tmdb+imdb), got %d", extIDCount)
	}
}

func TestUpsertMovie_DoesNotClobberMonitoredOnRefresh(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	res, err := db.ExecContext(ctx, `INSERT INTO movie_metadata (title, sort_title, clean_title) VALUES ('X', 'X', 'x')`)
	if err != nil {
		t.Fatalf("seed movie_metadata: %v", err)
	}
	metadataID, _ := res.LastInsertId()

	qualityProfileID := seedQualityProfile(t, db)
	rootFolderID := seedRootFolder(t, db, "movie")

	movieID1, err := store.UpsertMovie(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("first upsert movie: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE movies SET monitored = 0 WHERE id = ?`, movieID1); err != nil {
		t.Fatalf("simulate user unmonitoring: %v", err)
	}

	// A refresh calling UpsertMovie again with monitored=true must NOT
	// re-enable it - that's a user setting, not metadata.
	movieID2, err := store.UpsertMovie(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("second upsert movie: %v", err)
	}
	if movieID1 != movieID2 {
		t.Fatalf("want same movie id, got %d then %d", movieID1, movieID2)
	}

	var monitored bool
	if err := db.QueryRowContext(ctx, `SELECT monitored FROM movies WHERE id = ?`, movieID1).Scan(&monitored); err != nil {
		t.Fatalf("query monitored: %v", err)
	}
	if monitored {
		t.Fatal("want monitored to stay false after a refresh, user setting was clobbered")
	}
}

func seedQualityProfile(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(), `INSERT INTO quality_profiles (name) VALUES ('Test Profile')`)
	if err != nil {
		t.Fatalf("seed quality_profiles: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func seedRootFolder(t *testing.T, db *sql.DB, mediaType string) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(), `INSERT INTO root_folders (path, media_type) VALUES (?, ?)`, "/media/"+mediaType, mediaType)
	if err != nil {
		t.Fatalf("seed root_folders: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestListMovies(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rootFolderID := seedRootFolder(t, db, "movie")
	qualityProfileID := seedQualityProfile(t, db)

	for _, title := range []string{"Inception", "Interstellar"} {
		metadataID, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
			Title:       metadata.Field[string]{Value: title, Provider: "tmdb"},
			Year:        metadata.Field[int]{Value: 2010, Provider: "tmdb"},
			ExternalIDs: map[string]string{"tmdb": title},
		})
		if err != nil {
			t.Fatalf("upsert movie_metadata %s: %v", title, err)
		}
		if _, err := store.UpsertMovie(ctx, db, metadataID, qualityProfileID, rootFolderID, true); err != nil {
			t.Fatalf("upsert movie %s: %v", title, err)
		}
	}

	all, err := store.ListMovies(ctx, db)
	if err != nil {
		t.Fatalf("list movies: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 movies, got %d", len(all))
	}

	recent, err := store.ListRecentMovies(ctx, db, 1)
	if err != nil {
		t.Fatalf("list recent movies: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("want 1 recent movie (limit), got %d", len(recent))
	}
}

func TestListMoviesMissingFile(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rootFolderID := seedRootFolder(t, db, "movie")
	qualityProfileID := seedQualityProfile(t, db)

	metadataID, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
		Title: metadata.Field[string]{Value: "Inception", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "27205"},
	})
	if err != nil {
		t.Fatalf("upsert movie_metadata: %v", err)
	}
	withFileID, err := store.UpsertMovie(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert movie: %v", err)
	}
	if _, err := store.InsertMovieFile(ctx, db, withFileID, "Inception.mkv", 100); err != nil {
		t.Fatalf("insert movie file: %v", err)
	}

	metadataID2, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
		Title: metadata.Field[string]{Value: "Interstellar", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "157336"},
	})
	if err != nil {
		t.Fatalf("upsert movie_metadata 2: %v", err)
	}
	missingFileID, err := store.UpsertMovie(ctx, db, metadataID2, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert movie 2: %v", err)
	}

	missing, err := store.ListMoviesMissingFile(ctx, db)
	if err != nil {
		t.Fatalf("list movies missing file: %v", err)
	}
	if len(missing) != 1 || missing[0].ID != missingFileID {
		t.Fatalf("want only the file-less movie %d, got %+v", missingFileID, missing)
	}
}
