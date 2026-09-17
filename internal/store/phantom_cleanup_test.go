package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Migration 36 clears episodes that only TVMaze listed, in seasons TMDB
// also described - the duplicate finales TVMaze's split-premiere numbering
// produced. It must leave alone anything with a file, anything TMDB named,
// and a season TVMaze is the only source for.
func TestMigration36_RemovesOnlyThePhantomEpisodes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	seriesID := seedSeriesForPhantoms(t, db)
	// Season 1: TMDB describes it, so TVMaze may not add to it.
	tmdbEp := addEpisode(t, db, seriesID, 1, 1, "Real Episode", "tmdb", false)
	phantom := addEpisode(t, db, seriesID, 1, 2, "Real Episode", "tvmaze", false)
	tvmazeWithFile := addEpisode(t, db, seriesID, 1, 3, "Has A File", "tvmaze", true)
	// Season 2: only TVMaze knows it, so its episodes are all there is.
	tvmazeOnlySeason := addEpisode(t, db, seriesID, 2, 1, "TVMaze Only Season", "tvmaze", false)

	runMigration36(t, db)

	alive := func(id int64) bool {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM episodes WHERE id = ?`, id).Scan(&n); err != nil {
			t.Fatalf("count episode %d: %v", id, err)
		}
		return n == 1
	}
	if !alive(tmdbEp) {
		t.Error("the episode TMDB named must survive")
	}
	if alive(phantom) {
		t.Error("the TVMaze-only episode in a TMDB season should be gone")
	}
	if !alive(tvmazeWithFile) {
		t.Error("an episode with a file must survive whoever named it")
	}
	if !alive(tvmazeOnlySeason) {
		t.Error("a season only TVMaze knows about must survive")
	}

	var leftover int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM metadata_field_provenance WHERE entity_type='episode' AND entity_id = ?`,
		phantom).Scan(&leftover); err != nil {
		t.Fatalf("count provenance: %v", err)
	}
	if leftover != 0 {
		t.Errorf("the removed episode's provenance rows should go with it, got %d", leftover)
	}
}

// runMigration36 replays the migration's statements against an already
// migrated test database, which has of course had them applied to an empty
// schema already.
func runMigration36(t *testing.T, db *sql.DB) {
	t.Helper()
	sqlText, err := migrationSQL("00036_remove_phantom_episodes.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := db.Exec(sqlText); err != nil {
		t.Fatalf("run migration 36: %v", err)
	}
}

// migrationSQL reads a migration file's Up section from disk - tests run
// in the package directory, and the embedded copy is unexported.
func migrationSQL(name string) (string, error) {
	raw, err := os.ReadFile(filepath.Join("migrations", name))
	if err != nil {
		return "", err
	}
	text := string(raw)
	start := strings.Index(text, "-- +goose Up")
	if start < 0 {
		return "", fmt.Errorf("%s has no Up section", name)
	}
	text = text[start+len("-- +goose Up"):]
	if end := strings.Index(text, "-- +goose Down"); end >= 0 {
		text = text[:end]
	}
	return text, nil
}

func seedSeriesForPhantoms(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	ctx := context.Background()
	metadataID, err := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{
		Title:       metadata.Field[string]{Value: "Phantom Test", Provider: "tmdb"},
		ExternalIDs: map[string]string{"tmdb": "999001"},
	})
	if err != nil {
		t.Fatalf("upsert series metadata: %v", err)
	}
	seriesID, err := store.UpsertSeries(ctx, db, metadataID, seedQualityProfile(t, db), seedRootFolder(t, db, "series"), true)
	if err != nil {
		t.Fatalf("upsert series: %v", err)
	}
	return seriesID
}

// addEpisode inserts one episode recorded as named by provider, optionally
// with a file attached.
func addEpisode(t *testing.T, db *sql.DB, seriesID int64, season, number int, title, provider string, withFile bool) int64 {
	t.Helper()
	ctx := context.Background()
	seasonID, err := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: season})
	if err != nil {
		t.Fatalf("upsert season %d: %v", season, err)
	}
	episodeID, err := store.UpsertEpisode(ctx, db, seriesID, seasonID, season, metadata.EpisodeMetadata{
		EpisodeNumber: number,
		Title:         metadata.Field[string]{Value: title, Provider: provider},
	})
	if err != nil {
		t.Fatalf("upsert episode s%02de%02d: %v", season, number, err)
	}
	if _, err := db.Exec(`INSERT OR REPLACE INTO metadata_field_provenance
		(entity_type, entity_id, field_name, provider) VALUES ('episode', ?, 'title', ?)`,
		episodeID, provider); err != nil {
		t.Fatalf("record provenance: %v", err)
	}
	if withFile {
		if _, err := store.AttachEpisodeFile(ctx, db, episodeID,
			fmt.Sprintf("Season %02d/file-s%02de%02d.mkv", season, season, number), 1); err != nil {
			t.Fatalf("attach file: %v", err)
		}
	}
	return episodeID
}
