// Package fixtures contains golden-path round-trip tests for the schema
// itself - seeding real rows through plain SQL (the sqlc-generated query
// layer lands in a later pass) to prove out the relationships and
// constraints migrations 00001-00010 establish, per the verification
// checklist in the project plan.
package fixtures

import (
	"database/sql"
	"path/filepath"
	"testing"

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

// Scenario 1: normal artist/album/tracks.
func TestNormalArtistAlbumTracks(t *testing.T) {
	db := openTestDB(t)

	artistMetaID := insertArtistMetadata(t, db, "Daft Punk", "daftpunk")
	insertArtist(t, db, artistMetaID)

	albumID := insertAlbum(t, db, artistMetaID, "Discovery", "Album", "[]")
	releaseID := insertRelease(t, db, albumID, "Discovery")
	insertTrack(t, db, releaseID, artistMetaID, "One More Time", "1")

	var got string
	err := db.QueryRow(`
		SELECT ar.name FROM tracks t
		JOIN artist_metadata ar ON ar.id = t.artist_metadata_id
		WHERE t.title = ?`, "One More Time").Scan(&got)
	if err != nil {
		t.Fatalf("query track artist: %v", err)
	}
	if got != "Daft Punk" {
		t.Fatalf("want Daft Punk, got %s", got)
	}
}

// Scenario 2: VA compilation with compilation_series + sequence_number.
func TestVariousArtistsCompilationSeries(t *testing.T) {
	db := openTestDB(t)

	vaMetaID := insertArtistMetadata(t, db, "Various Artists", "variousartists")
	insertArtist(t, db, vaMetaID)

	seriesID := insertCompilationSeries(t, db, "Now That's What I Call Music", "musicbrainz")
	albumID := insertAlbum(t, db, vaMetaID, "Now That's What I Call Music! 50", "Album", `["Compilation"]`)
	linkAlbumToSeries(t, db, seriesID, albumID, 50)

	// Each track on a VA compilation carries its OWN artist, independent of
	// the album-level (Various Artists) artist - the mechanism carried
	// forward unchanged from Lidarr.
	realArtistMetaID := insertArtistMetadata(t, db, "Robbie Williams", "robbiewilliams")
	releaseID := insertRelease(t, db, albumID, "Now That's What I Call Music! 50")
	insertTrack(t, db, releaseID, realArtistMetaID, "Angels", "1")

	var seriesName string
	var seq int
	err := db.QueryRow(`
		SELECT cs.name, csa.sequence_number
		FROM compilation_series_albums csa
		JOIN compilation_series cs ON cs.id = csa.compilation_series_id
		WHERE csa.album_id = ?`, albumID).Scan(&seriesName, &seq)
	if err != nil {
		t.Fatalf("query series link: %v", err)
	}
	if seriesName != "Now That's What I Call Music" || seq != 50 {
		t.Fatalf("want series 'Now That's What I Call Music' seq 50, got %q seq %d", seriesName, seq)
	}

	var trackArtist string
	err = db.QueryRow(`
		SELECT ar.name FROM tracks t
		JOIN artist_metadata ar ON ar.id = t.artist_metadata_id
		WHERE t.title = ?`, "Angels").Scan(&trackArtist)
	if err != nil {
		t.Fatalf("query track artist: %v", err)
	}
	if trackArtist != "Robbie Williams" {
		t.Fatalf("want Robbie Williams, got %s", trackArtist)
	}
}

// Scenario 3: a single (AlbumType=Single) - no special-case code should be
// needed, it resolves through the exact same album shape as a full album.
func TestSingleResolvesLikeAlbum(t *testing.T) {
	db := openTestDB(t)

	artistMetaID := insertArtistMetadata(t, db, "Adele", "adele")
	insertArtist(t, db, artistMetaID)
	albumID := insertAlbum(t, db, artistMetaID, "Easy on Me", "Single", "[]")

	var albumType string
	err := db.QueryRow(`SELECT album_type FROM albums WHERE id = ?`, albumID).Scan(&albumType)
	if err != nil {
		t.Fatalf("query album type: %v", err)
	}
	if albumType != "Single" {
		t.Fatalf("want Single, got %s", albumType)
	}
}

// Scenario 4: a movie with multi-provider external_ids.
func TestMovieMultiProviderExternalIDs(t *testing.T) {
	db := openTestDB(t)

	res, err := db.Exec(`INSERT INTO movie_metadata (title, sort_title, clean_title, year)
		VALUES (?, ?, ?, ?)`, "Inception", "Inception", "inception", 2010)
	if err != nil {
		t.Fatalf("insert movie_metadata: %v", err)
	}
	metaID, _ := res.LastInsertId()

	for _, p := range []struct{ provider, id string }{
		{"tmdb", "27205"}, {"imdb", "tt1375666"}, {"omdb", "tt1375666"},
	} {
		if _, err := db.Exec(`INSERT INTO external_ids (entity_type, entity_id, provider, external_id)
			VALUES ('movie', ?, ?, ?)`, metaID, p.provider, p.id); err != nil {
			t.Fatalf("insert external_id %s: %v", p.provider, err)
		}
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM external_ids WHERE entity_type = 'movie' AND entity_id = ?`, metaID).Scan(&count); err != nil {
		t.Fatalf("count external_ids: %v", err)
	}
	if count != 3 {
		t.Fatalf("want 3 external ids, got %d", count)
	}
}

// Scenario 5: a TV series with real seasons rows.
func TestSeriesWithSeasons(t *testing.T) {
	db := openTestDB(t)

	res, err := db.Exec(`INSERT INTO series_metadata (title, sort_title, clean_title)
		VALUES (?, ?, ?)`, "Breaking Bad", "Breaking Bad", "breakingbad")
	if err != nil {
		t.Fatalf("insert series_metadata: %v", err)
	}
	metaID, _ := res.LastInsertId()

	res, err = db.Exec(`INSERT INTO series (series_metadata_id, season_folder) VALUES (?, 1)`, metaID)
	if err != nil {
		t.Fatalf("insert series: %v", err)
	}
	seriesID, _ := res.LastInsertId()

	if _, err := db.Exec(`INSERT INTO seasons (series_id, season_number) VALUES (?, 1), (?, 2)`, seriesID, seriesID); err != nil {
		t.Fatalf("insert seasons: %v", err)
	}

	var seasonCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM seasons WHERE series_id = ?`, seriesID).Scan(&seasonCount); err != nil {
		t.Fatalf("count seasons: %v", err)
	}
	if seasonCount != 2 {
		t.Fatalf("want 2 seasons, got %d", seasonCount)
	}
}

// --- helpers ---

func insertArtistMetadata(t *testing.T, db *sql.DB, name, cleanName string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO artist_metadata (name, clean_name, sort_name) VALUES (?, ?, ?)`,
		name, cleanName, name)
	if err != nil {
		t.Fatalf("insert artist_metadata %s: %v", name, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func insertArtist(t *testing.T, db *sql.DB, artistMetaID int64) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO artists (artist_metadata_id) VALUES (?)`, artistMetaID)
	if err != nil {
		t.Fatalf("insert artist: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func insertAlbum(t *testing.T, db *sql.DB, artistMetaID int64, title, albumType, secondaryTypes string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO albums (artist_metadata_id, title, clean_title, album_type, secondary_types)
		VALUES (?, ?, ?, ?, ?)`, artistMetaID, title, title, albumType, secondaryTypes)
	if err != nil {
		t.Fatalf("insert album %s: %v", title, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func insertRelease(t *testing.T, db *sql.DB, albumID int64, title string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO album_releases (album_id, title) VALUES (?, ?)`, albumID, title)
	if err != nil {
		t.Fatalf("insert release %s: %v", title, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func insertTrack(t *testing.T, db *sql.DB, releaseID, artistMetaID int64, title, trackNumber string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO tracks (album_release_id, artist_metadata_id, track_number, title)
		VALUES (?, ?, ?, ?)`, releaseID, artistMetaID, trackNumber, title)
	if err != nil {
		t.Fatalf("insert track %s: %v", title, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func insertCompilationSeries(t *testing.T, db *sql.DB, name, source string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO compilation_series (name, sort_name, source) VALUES (?, ?, ?)`,
		name, name, source)
	if err != nil {
		t.Fatalf("insert compilation_series %s: %v", name, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func linkAlbumToSeries(t *testing.T, db *sql.DB, seriesID, albumID int64, sequenceNumber int) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO compilation_series_albums (compilation_series_id, album_id, sequence_number)
		VALUES (?, ?, ?)`, seriesID, albumID, sequenceNumber)
	if err != nil {
		t.Fatalf("link album to series: %v", err)
	}
}
