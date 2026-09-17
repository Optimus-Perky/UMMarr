package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestChangeRootFolder(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	oldMovies, newMovies := t.TempDir(), t.TempDir()
	oldID, _ := store.CreateRootFolder(ctx, db, oldMovies, "movie")
	newID, _ := store.CreateRootFolder(ctx, db, newMovies, "movie")
	profile := seedQualityProfile(t, db)
	meta, _ := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{Title: metadata.Field[string]{Value: "Inception", Provider: "tmdb"}, Year: metadata.Field[int]{Value: 2010, Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "27205"}})
	movieID, err := store.UpsertMovie(ctx, db, meta, profile, oldID, true)
	if err != nil {
		t.Fatal(err)
	}
	d, _, _ := store.GetMovieDetail(ctx, db, movieID)
	os.MkdirAll(d.Path.String, 0o755)
	os.WriteFile(filepath.Join(d.Path.String, "Inception (2010).mkv"), []byte("x"), 0o644)
	svc := &ImportService{DB: db}

	if err := svc.ChangeRootFolder(ctx, "movie", movieID, newID, true); err != nil {
		t.Fatal(err)
	}
	moved, _, _ := store.GetMovieDetail(ctx, db, movieID)
	wantPath := filepath.Join(newMovies, filepath.Base(d.Path.String))
	if moved.RootFolderID != newID || moved.Path.String != wantPath {
		t.Fatalf("want the movie under the new folder at %s, got root %d path %s", wantPath, moved.RootFolderID, moved.Path.String)
	}
	if _, err := os.Stat(filepath.Join(wantPath, "Inception (2010).mkv")); err != nil {
		t.Fatalf("want the file moved: %v", err)
	}
	if _, err := os.Stat(d.Path.String); !os.IsNotExist(err) {
		t.Fatal("want the old folder gone")
	}
	if err := svc.ChangeRootFolder(ctx, "movie", movieID, newID, true); err != nil {
		t.Fatalf("the same folder again is a no-op: %v", err)
	}

	// Moving back onto a folder that already exists is refused and nothing changes.
	os.MkdirAll(d.Path.String, 0o755)
	if err := svc.ChangeRootFolder(ctx, "movie", movieID, oldID, true); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want the clash refused, got %v", err)
	}
	if still, _, _ := store.GetMovieDetail(ctx, db, movieID); still.RootFolderID != newID {
		t.Fatal("a refused move mustn't change the library")
	}

	// A series without Move files: only the path changes.
	oldTV, newTV := t.TempDir(), t.TempDir()
	oldTVID, _ := store.CreateRootFolder(ctx, db, oldTV, "series")
	newTVID, _ := store.CreateRootFolder(ctx, db, newTV, "series")
	smeta, _ := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{Title: metadata.Field[string]{Value: "Show", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "1"}})
	seriesID, _ := store.UpsertSeries(ctx, db, smeta, profile, oldTVID, true)
	sd, _, _ := store.GetSeriesDetail(ctx, db, seriesID)
	os.MkdirAll(sd.Path.String, 0o755)
	if err := svc.ChangeRootFolder(ctx, "series", seriesID, newTVID, false); err != nil {
		t.Fatal(err)
	}
	after, _, _ := store.GetSeriesDetail(ctx, db, seriesID)
	if after.RootFolderID != newTVID || after.Path.String != filepath.Join(newTV, filepath.Base(sd.Path.String)) {
		t.Fatalf("want the series pointed at the new folder, got %+v", after.SeriesSummary)
	}
	if _, err := os.Stat(sd.Path.String); err != nil {
		t.Fatal("without Move files the old folder stays")
	}
	if err := svc.ChangeRootFolder(ctx, "series", seriesID, newID, false); err == nil || !strings.Contains(err.Error(), "isn't a series folder") {
		t.Fatalf("want a movie folder refused for a series, got %v", err)
	}
	if err := svc.ChangeRootFolder(ctx, "album", 1, 1, false); err == nil {
		t.Fatal("want an unknown kind refused")
	}
}
