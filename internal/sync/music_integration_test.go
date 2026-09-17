//go:build integration

package sync

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/musicbrainz"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// variousArtistsMBID is MusicBrainz's real, well-known "Various Artists"
// artist entity - the same constant referenced throughout this project's
// Lidarr research and schema design.
const variousArtistsMBID = "89ad4ac3-39f7-470e-963a-56509c546377"

// TestLive_AddVariousArtistsCompilationEndToEnd is the capstone proof for
// this pass: real MusicBrainz calls (no API key needed) through the full
// AddArtistByMBID -> AddAlbumByMBID pipeline for a real "Now That's What
// I Call Music" compilation, asserting compilation_series is populated
// and at least one synced track is attributed to its own real performing
// artist rather than the album's Various Artists. Run with:
// go test -tags=integration ./...
func TestLive_AddVariousArtistsCompilationEndToEnd(t *testing.T) {
	mb, err := musicbrainz.New(musicbrainz.Options{UserAgent: "UMMarr-integration-test/0.1 (test@example.invalid)"})
	if err != nil {
		t.Fatalf("new musicbrainz client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer db.Close()

	rootFolderID := seedRootFolder(t, db, "music")
	qualityProfileID := seedQualityProfile(t, db)

	musicSvc := &MusicService{DB: db, MusicBrainz: mb}

	artistID, err := musicSvc.AddArtistByMBID(ctx, variousArtistsMBID, rootFolderID, qualityProfileID)
	if err != nil {
		t.Fatalf("add Various Artists: %v", err)
	}

	// Find a real "Now That's What I Call Music" release-group that
	// MusicBrainz has linked to its series, same discovery approach as
	// providers/musicbrainz/integration_test.go's series-rels test.
	candidates, err := mb.SearchReleaseGroup(ctx, `"Now That's What I Call Music"`)
	if err != nil {
		t.Fatalf("search release-group: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("want at least one release-group match")
	}
	var releaseGroupMBID string
	for _, c := range candidates {
		rg, err := mb.GetReleaseGroup(ctx, c.ID)
		if err != nil {
			t.Fatalf("get release group %s: %v", c.ID, err)
		}
		for _, rel := range rg.Relations {
			if rel.TargetType == "series" && rel.Series != nil {
				releaseGroupMBID = c.ID
				break
			}
		}
		if releaseGroupMBID != "" {
			break
		}
	}
	if releaseGroupMBID == "" {
		t.Fatal("want at least one candidate with a series relation")
	}

	albumID, err := musicSvc.AddAlbumByMBID(ctx, releaseGroupMBID, artistID)
	if err != nil {
		t.Fatalf("add album: %v", err)
	}

	var seriesCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM compilation_series_albums WHERE album_id = ?
	`, albumID).Scan(&seriesCount); err != nil {
		t.Fatalf("count compilation_series_albums: %v", err)
	}
	if seriesCount != 1 {
		t.Fatalf("want album linked to exactly 1 compilation series, got %d", seriesCount)
	}

	assertAtLeastOneNonVATrackArtist(t, ctx, db, albumID)
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

func seedQualityProfile(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(), `INSERT INTO quality_profiles (name) VALUES ('Test Profile')`)
	if err != nil {
		t.Fatalf("seed quality_profiles: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func assertAtLeastOneNonVATrackArtist(t *testing.T, ctx context.Context, db *sql.DB, albumID int64) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT am.name
		FROM tracks t
		JOIN album_releases r ON r.id = t.album_release_id
		JOIN artist_metadata am ON am.id = t.artist_metadata_id
		WHERE r.album_id = ?
	`, albumID)
	if err != nil {
		t.Fatalf("query track artists: %v", err)
	}
	defer rows.Close()

	sawNonVA := false
	trackArtistCount := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan track artist name: %v", err)
		}
		trackArtistCount++
		if name != "Various Artists" {
			sawNonVA = true
		}
	}
	if trackArtistCount == 0 {
		t.Fatal("want at least one synced track with a resolved artist")
	}
	if !sawNonVA {
		t.Fatal("want at least one track attributed to a real performing artist, not just Various Artists")
	}
}
