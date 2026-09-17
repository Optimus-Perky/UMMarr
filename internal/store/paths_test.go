package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/pathbuilder"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestResolveMoviePath(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rootFolderID := seedRootFolder(t, db, "movie")
	qualityProfileID := seedQualityProfile(t, db)

	metadataID, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
		Title:       metadata.Field[string]{Value: "Inception", Provider: "tmdb"},
		Year:        metadata.Field[int]{Value: 2010, Provider: "tmdb"},
		ExternalIDs: map[string]string{"tmdb": "27205"},
	})
	if err != nil {
		t.Fatalf("upsert movie_metadata: %v", err)
	}
	movieID, err := store.UpsertMovie(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert movie: %v", err)
	}

	var path string
	if err := db.QueryRowContext(ctx, `SELECT path FROM movies WHERE id = ?`, movieID).Scan(&path); err != nil {
		t.Fatalf("query movie path: %v", err)
	}
	want := "/media/movie/Inception (2010)"
	if path != want {
		t.Fatalf("want path %q, got %q", want, path)
	}
}

func TestResolveSeriesAndSeasonPath(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rootFolderID := seedRootFolder(t, db, "series")
	qualityProfileID := seedQualityProfile(t, db)

	metadataID, err := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{
		Title:       metadata.Field[string]{Value: "Breaking Bad", Provider: "tmdb"},
		ExternalIDs: map[string]string{"tmdb": "1396"},
	})
	if err != nil {
		t.Fatalf("upsert series_metadata: %v", err)
	}
	seriesID, err := store.UpsertSeries(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert series: %v", err)
	}

	var seriesPath string
	if err := db.QueryRowContext(ctx, `SELECT path FROM series WHERE id = ?`, seriesID).Scan(&seriesPath); err != nil {
		t.Fatalf("query series path: %v", err)
	}
	wantSeriesPath := "/media/series/Breaking Bad"
	if seriesPath != wantSeriesPath {
		t.Fatalf("want series path %q, got %q", wantSeriesPath, seriesPath)
	}

	// season_folder=true: season path nests under the series path.
	nested := store.ResolveSeasonPath(seriesPath, "Season {season}", 3, true, nil, pathbuilder.Options{})
	if want := "/media/series/Breaking Bad/Season 3"; nested != want {
		t.Fatalf("want nested season path %q, got %q", want, nested)
	}

	// season_folder=false: no subfolder, episodes live directly under the series.
	flat := store.ResolveSeasonPath(seriesPath, "Season {season}", 3, false, nil, pathbuilder.Options{})
	if flat != seriesPath {
		t.Fatalf("want flat season path to equal series path %q, got %q", seriesPath, flat)
	}

	// A user override wins outright, regardless of season_folder.
	override := "/custom/season-3-elsewhere"
	overridden := store.ResolveSeasonPath(seriesPath, "Season {season}", 3, true, &override, pathbuilder.Options{})
	if overridden != override {
		t.Fatalf("want override path %q to win, got %q", override, overridden)
	}
}

func TestResolveAlbumPath_NormalArtistAndSingle(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rootFolderID := seedRootFolder(t, db, "music")
	qualityProfileID := seedQualityProfile(t, db)

	artistMetaID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name:        metadata.Field[string]{Value: "Adele", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "adele-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert artist_metadata: %v", err)
	}
	artistID, err := store.UpsertArtist(ctx, db, artistMetaID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert artist: %v", err)
	}
	_ = artistID

	albumDate := time.Date(2015, 11, 20, 0, 0, 0, 0, time.UTC)
	albumID, created, err := store.UpsertAlbum(ctx, db, artistMetaID, metadata.AlbumMetadata{
		Title:       metadata.Field[string]{Value: "25", Provider: "musicbrainz"},
		AlbumType:   metadata.Field[string]{Value: "Album", Provider: "musicbrainz"},
		ReleaseDate: metadata.Field[*time.Time]{Value: &albumDate, Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "25-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert album: %v", err)
	}
	if !created {
		t.Fatal("want album reported as newly created")
	}
	if err := store.SetAlbumPath(ctx, db, albumID); err != nil {
		t.Fatalf("set album path: %v", err)
	}

	var albumPath string
	if err := db.QueryRowContext(ctx, `SELECT path FROM albums WHERE id = ?`, albumID).Scan(&albumPath); err != nil {
		t.Fatalf("query album path: %v", err)
	}
	want := "/media/music/Adele/25 (2015)"
	if albumPath != want {
		t.Fatalf("want album path %q, got %q", want, albumPath)
	}

	// A single must resolve through the exact same shape - no special-casing.
	singleDate := time.Date(2021, 10, 15, 0, 0, 0, 0, time.UTC)
	singleID, _, err := store.UpsertAlbum(ctx, db, artistMetaID, metadata.AlbumMetadata{
		Title:       metadata.Field[string]{Value: "Easy on Me", Provider: "musicbrainz"},
		AlbumType:   metadata.Field[string]{Value: "Single", Provider: "musicbrainz"},
		ReleaseDate: metadata.Field[*time.Time]{Value: &singleDate, Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "easy-on-me-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert single: %v", err)
	}
	if err := store.SetAlbumPath(ctx, db, singleID); err != nil {
		t.Fatalf("set single path: %v", err)
	}
	var singlePath string
	if err := db.QueryRowContext(ctx, `SELECT path FROM albums WHERE id = ?`, singleID).Scan(&singlePath); err != nil {
		t.Fatalf("query single path: %v", err)
	}
	if want := "/media/music/Adele/Easy on Me (2021)"; singlePath != want {
		t.Fatalf("want single path %q (same shape as a normal album), got %q", want, singlePath)
	}
}

func TestResolveAlbumPath_VariousArtists(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	seedRootFolder(t, db, "music")

	vaMetaID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name:        metadata.Field[string]{Value: "Various Artists", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "89ad4ac3-39f7-470e-963a-56509c546377"},
	})
	if err != nil {
		t.Fatalf("upsert VA artist_metadata: %v", err)
	}

	now50Date := time.Date(2001, 11, 19, 0, 0, 0, 0, time.UTC)

	// With a series link.
	withSeriesID, _, err := store.UpsertAlbum(ctx, db, vaMetaID, metadata.AlbumMetadata{
		Title:       metadata.Field[string]{Value: "Now That's What I Call Music! 50", Provider: "musicbrainz"},
		ReleaseDate: metadata.Field[*time.Time]{Value: &now50Date, Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "now-50-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert VA album with series: %v", err)
	}
	seriesID, err := store.UpsertCompilationSeries(ctx, db, "Now That's What I Call Music", "Now That's What I Call Music", "series-mbid")
	if err != nil {
		t.Fatalf("upsert compilation series: %v", err)
	}
	if err := store.LinkAlbumToCompilationSeries(ctx, db, seriesID, withSeriesID, 50); err != nil {
		t.Fatalf("link album to series: %v", err)
	}
	if err := store.SetAlbumPath(ctx, db, withSeriesID); err != nil {
		t.Fatalf("set path (with series): %v", err)
	}
	var withSeriesPath string
	if err := db.QueryRowContext(ctx, `SELECT path FROM albums WHERE id = ?`, withSeriesID).Scan(&withSeriesPath); err != nil {
		t.Fatalf("query path: %v", err)
	}
	if want := "/media/music/Various Artists/Now That's What I Call Music/Now That's What I Call Music! 50 (2001)"; withSeriesPath != want {
		t.Fatalf("want %q, got %q", want, withSeriesPath)
	}

	// No series link - literal "Various Artists" fallback.
	randomDate := time.Date(2005, 6, 1, 0, 0, 0, 0, time.UTC)
	noSeriesID, _, err := store.UpsertAlbum(ctx, db, vaMetaID, metadata.AlbumMetadata{
		Title:       metadata.Field[string]{Value: "Some Random Compilation", Provider: "musicbrainz"},
		ReleaseDate: metadata.Field[*time.Time]{Value: &randomDate, Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "random-comp-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert VA album without series: %v", err)
	}
	if err := store.SetAlbumPath(ctx, db, noSeriesID); err != nil {
		t.Fatalf("set path (no series): %v", err)
	}
	var noSeriesPath string
	if err := db.QueryRowContext(ctx, `SELECT path FROM albums WHERE id = ?`, noSeriesID).Scan(&noSeriesPath); err != nil {
		t.Fatalf("query path: %v", err)
	}
	if want := "/media/music/Various Artists/Some Random Compilation (2005)"; noSeriesPath != want {
		t.Fatalf("want %q, got %q", want, noSeriesPath)
	}
}

func TestUpsertMovie_PathNotClobberedOnRefresh(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	rootFolderID := seedRootFolder(t, db, "movie")
	qualityProfileID := seedQualityProfile(t, db)

	metadataID, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
		Title:       metadata.Field[string]{Value: "Inception", Provider: "tmdb"},
		Year:        metadata.Field[int]{Value: 2010, Provider: "tmdb"},
		ExternalIDs: map[string]string{"tmdb": "27205"},
	})
	if err != nil {
		t.Fatalf("upsert movie_metadata: %v", err)
	}
	movieID, err := store.UpsertMovie(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("first upsert movie: %v", err)
	}

	// Simulate the user manually moving/renaming the folder on disk.
	if _, err := db.ExecContext(ctx, `UPDATE movies SET path = '/media/movie/moved-by-hand' WHERE id = ?`, movieID); err != nil {
		t.Fatalf("simulate manual move: %v", err)
	}

	// A refresh (second UpsertMovie call for the same metadata id) must
	// not touch path at all - it's only ever set on first creation.
	movieID2, err := store.UpsertMovie(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("second upsert movie: %v", err)
	}
	if movieID != movieID2 {
		t.Fatalf("want same movie id, got %d then %d", movieID, movieID2)
	}

	var path string
	if err := db.QueryRowContext(ctx, `SELECT path FROM movies WHERE id = ?`, movieID).Scan(&path); err != nil {
		t.Fatalf("query path: %v", err)
	}
	if path != "/media/movie/moved-by-hand" {
		t.Fatalf("want manually-set path preserved across refresh, got %q", path)
	}
}

func TestUpdateMovieNamingConfig(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := store.UpdateMovieNamingConfig(ctx, db, "{Movie Title}", "{Movie Title}.{Release Year}"); err != nil {
		t.Fatalf("update movie naming config: %v", err)
	}
	config, err := store.GetNamingConfig(ctx, db, "movie")
	if err != nil {
		t.Fatalf("get naming config: %v", err)
	}
	if config.MovieFolderFormat.String != "{Movie Title}" || config.MovieFileFormat.String != "{Movie Title}.{Release Year}" {
		t.Fatalf("movie naming config not persisted: %+v", config)
	}
}

func TestUpdateSeriesNamingConfig(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := store.UpdateSeriesNamingConfig(ctx, db, "{Series Title} (New)", "S{season}", "{Episode Title} only"); err != nil {
		t.Fatalf("update series naming config: %v", err)
	}
	config, err := store.GetNamingConfig(ctx, db, "series")
	if err != nil {
		t.Fatalf("get naming config: %v", err)
	}
	if config.SeriesFolderFormat.String != "{Series Title} (New)" || config.SeasonFolderFormat.String != "S{season}" ||
		config.EpisodeFileFormat.String != "{Episode Title} only" {
		t.Fatalf("series naming config not persisted: %+v", config)
	}
}

func TestUpdateMusicNamingConfig(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if err := store.UpdateMusicNamingConfig(ctx, db, "{Artist Name} (New)", "{Album Title}", "VA/{Series Name}", "{Track Title}"); err != nil {
		t.Fatalf("update music naming config: %v", err)
	}
	config, err := store.GetNamingConfig(ctx, db, "music")
	if err != nil {
		t.Fatalf("get naming config: %v", err)
	}
	if config.ArtistFolderFormat.String != "{Artist Name} (New)" || config.AlbumFolderFormat.String != "{Album Title}" ||
		config.VASeriesFolderFormat.String != "VA/{Series Name}" || config.TrackFileFormat.String != "{Track Title}" {
		t.Fatalf("music naming config not persisted: %+v", config)
	}
}

// TestUpdateMovieNamingConfig_TakesEffectImmediately proves the "no
// restart needed" claim for naming specifically: ResolveMovieFileName
// re-reads GetNamingConfig fresh on every call, so a template saved via
// Settings changes the very next resolved filename with no caching to
// invalidate.
func TestUpdateMovieNamingConfig_TakesEffectImmediately(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	movieID := seedMovie(t, db)

	before, err := store.ResolveMovieFileName(ctx, db, movieID, "download.mkv")
	if err != nil {
		t.Fatalf("resolve movie file name: %v", err)
	}

	if err := store.UpdateMovieNamingConfig(ctx, db, "{Movie Title} ({Release Year})", "Renamed - {Movie Title}"); err != nil {
		t.Fatalf("update movie naming config: %v", err)
	}

	after, err := store.ResolveMovieFileName(ctx, db, movieID, "download.mkv")
	if err != nil {
		t.Fatalf("resolve movie file name after update: %v", err)
	}
	if before == after {
		t.Fatalf("want the resolved filename to change after updating the template, got %q both times", before)
	}
	if after != "Renamed - Inception.mkv" {
		t.Fatalf("want 'Renamed - Inception.mkv', got %q", after)
	}
}
