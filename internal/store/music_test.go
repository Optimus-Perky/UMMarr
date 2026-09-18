package store_test

import (
	"context"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestUpsertArtistMetadata_IdempotentByExternalID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	a := metadata.ArtistMetadata{
		Name:        metadata.Field[string]{Value: "Daft Punk", Provider: "musicbrainz"},
		ArtistType:  metadata.Field[string]{Value: "Group", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "056e4f3e-d505-4dad-8ec1-d04f521cbb56"},
	}

	id1, err := store.UpsertArtistMetadata(ctx, db, a)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	id2, err := store.UpsertArtistMetadata(ctx, db, a)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("want same artist_metadata id on re-sync, got %d then %d", id1, id2)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM artist_metadata`).Scan(&count); err != nil {
		t.Fatalf("count artist_metadata: %v", err)
	}
	if count != 1 {
		t.Fatalf("want 1 artist_metadata row after two upserts, got %d", count)
	}

	var cleanName, sortName string
	if err := db.QueryRowContext(ctx, `SELECT clean_name, sort_name FROM artist_metadata WHERE id = ?`, id1).Scan(&cleanName, &sortName); err != nil {
		t.Fatalf("query artist_metadata: %v", err)
	}
	if cleanName != "daftpunk" || sortName != "Daft Punk" {
		t.Fatalf("want computed clean_name/sort_name, got %q/%q", cleanName, sortName)
	}
}

func TestCompilationSeriesLink_UpsertByAlbumID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	artistID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name:        metadata.Field[string]{Value: "Various Artists", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "89ad4ac3-39f7-470e-963a-56509c546377"},
	})
	if err != nil {
		t.Fatalf("upsert VA artist: %v", err)
	}

	albumID, _, err := store.UpsertAlbum(ctx, db, artistID, metadata.AlbumMetadata{
		Title:       metadata.Field[string]{Value: "Now That's What I Call Music! 50", Provider: "musicbrainz"},
		AlbumType:   metadata.Field[string]{Value: "Album", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "rg-mbid-50"},
	})
	if err != nil {
		t.Fatalf("upsert album: %v", err)
	}

	seriesID, err := store.UpsertCompilationSeries(ctx, db, "Now That's What I Call Music", "Now That's What I Call Music", "series-mbid")
	if err != nil {
		t.Fatalf("upsert compilation series: %v", err)
	}
	if err := store.LinkAlbumToCompilationSeries(ctx, db, seriesID, albumID, 50); err != nil {
		t.Fatalf("link album to series: %v", err)
	}
	// Re-linking (e.g. a refresh) must update, not duplicate or error.
	if err := store.LinkAlbumToCompilationSeries(ctx, db, seriesID, albumID, 50); err != nil {
		t.Fatalf("re-link album to series: %v", err)
	}

	var linkedSeriesID int64
	var sequenceNumber int
	if err := db.QueryRowContext(ctx, `SELECT compilation_series_id, sequence_number FROM compilation_series_albums WHERE album_id = ?`, albumID).Scan(&linkedSeriesID, &sequenceNumber); err != nil {
		t.Fatalf("query link: %v", err)
	}
	if linkedSeriesID != seriesID || sequenceNumber != 50 {
		t.Fatalf("want link to series %d seq 50, got series %d seq %d", seriesID, linkedSeriesID, sequenceNumber)
	}
}

func TestUpsertTrack_VariousArtistsPerTrackAttribution(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	vaID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name:        metadata.Field[string]{Value: "Various Artists", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "89ad4ac3-39f7-470e-963a-56509c546377"},
	})
	if err != nil {
		t.Fatalf("upsert VA artist: %v", err)
	}
	albumID, _, err := store.UpsertAlbum(ctx, db, vaID, metadata.AlbumMetadata{
		Title:       metadata.Field[string]{Value: "Compilation", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "rg-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert album: %v", err)
	}
	releaseID, err := store.UpsertAlbumRelease(ctx, db, albumID, metadata.ReleaseMetadata{
		Title:       metadata.Field[string]{Value: "Compilation", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "release-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert release: %v", err)
	}

	robbieID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name:        metadata.Field[string]{Value: "Robbie Williams", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "robbie-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert robbie: %v", err)
	}

	trackID, err := store.UpsertTrack(ctx, db, releaseID, robbieID, metadata.TrackSource{
		Number: "1", Title: "Angels", DurationMs: 240000, MediumNumber: 1,
		ArtistCredits: []metadata.ArtistCreditRef{{Name: "Robbie Williams", MusicBrainzArtistID: "robbie-mbid"}},
	})
	if err != nil {
		t.Fatalf("upsert track: %v", err)
	}

	var trackArtistName string
	if err := db.QueryRowContext(ctx, `
		SELECT am.name FROM tracks t JOIN artist_metadata am ON am.id = t.artist_metadata_id WHERE t.id = ?
	`, trackID).Scan(&trackArtistName); err != nil {
		t.Fatalf("query track artist: %v", err)
	}
	if trackArtistName != "Robbie Williams" {
		t.Fatalf("want track attributed to Robbie Williams (not the album's Various Artists), got %s", trackArtistName)
	}

	// Re-syncing the same track (by album_release_id+medium+number) must
	// update in place, not duplicate.
	if _, err := store.UpsertTrack(ctx, db, releaseID, robbieID, metadata.TrackSource{
		Number: "1", Title: "Angels (Remastered)", DurationMs: 241000, MediumNumber: 1,
	}); err != nil {
		t.Fatalf("re-upsert track: %v", err)
	}
	var trackCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tracks WHERE album_release_id = ?`, releaseID).Scan(&trackCount); err != nil {
		t.Fatalf("count tracks: %v", err)
	}
	if trackCount != 1 {
		t.Fatalf("want 1 track after re-sync, got %d", trackCount)
	}
}

func TestListAlbums(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	artistID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name:        metadata.Field[string]{Value: "Daft Punk", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "daft-punk-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert artist: %v", err)
	}

	for _, title := range []string{"Discovery", "Homework"} {
		if _, _, err := store.UpsertAlbum(ctx, db, artistID, metadata.AlbumMetadata{
			Title:       metadata.Field[string]{Value: title, Provider: "musicbrainz"},
			ExternalIDs: map[string]string{"musicbrainz": title},
		}); err != nil {
			t.Fatalf("upsert album %s: %v", title, err)
		}
	}

	all, err := store.ListAlbums(ctx, db)
	if err != nil {
		t.Fatalf("list albums: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 albums, got %d", len(all))
	}
	for _, a := range all {
		if a.ArtistName != "Daft Punk" {
			t.Fatalf("want artist name 'Daft Punk' joined in, got %q", a.ArtistName)
		}
		if a.CompilationSeriesName != "" {
			t.Fatalf("want no compilation series for a normal album, got %q", a.CompilationSeriesName)
		}
	}

	recent, err := store.ListRecentAlbums(ctx, db, 1)
	if err != nil {
		t.Fatalf("list recent albums: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("want 1 recent album (limit), got %d", len(recent))
	}
}

func TestListAlbums_CompilationSeriesName(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	vaID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name:        metadata.Field[string]{Value: "Various Artists", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "89ad4ac3-39f7-470e-963a-56509c546377"},
	})
	if err != nil {
		t.Fatalf("upsert VA artist: %v", err)
	}
	albumID, _, err := store.UpsertAlbum(ctx, db, vaID, metadata.AlbumMetadata{
		Title:       metadata.Field[string]{Value: "Now That's What I Call Music! 50", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "now-50-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert VA album: %v", err)
	}
	seriesID, err := store.UpsertCompilationSeries(ctx, db, "Now That's What I Call Music", "Now That's What I Call Music", "series-mbid")
	if err != nil {
		t.Fatalf("upsert compilation series: %v", err)
	}
	if err := store.LinkAlbumToCompilationSeries(ctx, db, seriesID, albumID, 50); err != nil {
		t.Fatalf("link album to series: %v", err)
	}

	albums, err := store.ListAlbums(ctx, db)
	if err != nil {
		t.Fatalf("list albums: %v", err)
	}
	if len(albums) != 1 || albums[0].CompilationSeriesName != "Now That's What I Call Music" {
		t.Fatalf("want compilation series name joined in, got %+v", albums)
	}
	if !albums[0].CompilationSeqNumber.Valid || albums[0].CompilationSeqNumber.Int64 != 50 {
		t.Fatalf("want sequence number 50, got %+v", albums[0].CompilationSeqNumber)
	}
}

// track_number is TEXT, because MusicBrainz numbers can be "A1" on a vinyl.
// Ordering it as text puts track 10 between 1 and 2, and positional matching
// then attached every file after the first to the wrong track - which is
// what a real library turned out to be full of.
func TestFindImportRelease_OrdersTracksNumerically(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	albumID, artistID := seedAlbumForRelease(t, db)
	releaseID, err := store.UpsertAlbumRelease(ctx, db, albumID, metadata.ReleaseMetadata{
		Title: metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "hw-order"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"1", "2", "3", "10", "11", "12"} {
		if _, err := store.UpsertTrack(ctx, db, releaseID, artistID, metadata.TrackSource{
			Number: n, Title: "Track " + n, MediumNumber: 1}); err != nil {
			t.Fatal(err)
		}
	}

	_, tracks, err := store.FindImportRelease(ctx, db, albumID)
	if err != nil {
		t.Fatalf("find import release: %v", err)
	}
	var got []string
	for _, tr := range tracks {
		got = append(got, tr.Number)
	}
	want := []string{"1", "2", "3", "10", "11", "12"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("track order = %v, want %v", got, want)
		}
	}
}
