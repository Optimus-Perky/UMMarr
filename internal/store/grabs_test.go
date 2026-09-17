package store_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func seedMovie(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	ctx := context.Background()
	rootFolderID := seedRootFolder(t, db, "movie")
	qualityProfileID := seedQualityProfile(t, db)
	metadataID, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
		Title:       metadata.Field[string]{Value: "Inception", Provider: "tmdb"},
		ExternalIDs: map[string]string{"tmdb": "27205"},
	})
	if err != nil {
		t.Fatalf("upsert movie_metadata: %v", err)
	}
	movieID, err := store.UpsertMovie(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatalf("upsert movie: %v", err)
	}
	return movieID
}

func TestInsertAndListGrabs(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	movieID := seedMovie(t, db)

	id, err := store.InsertGrab(ctx, db, store.Grab{
		MovieID: sql.NullInt64{Int64: movieID, Valid: true}, ReleaseTitle: "Inception 2010 1080p",
		Indexer: "SomeIndexer", Protocol: "torrent", DownloadClient: "deluge",
		DownloadClientID: sql.NullString{String: "deadbeef", Valid: true}, Status: "grabbed",
	})
	if err != nil {
		t.Fatalf("insert grab: %v", err)
	}

	grabs, err := store.ListGrabs(ctx, db)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	if len(grabs) != 1 || grabs[0].ID != id || grabs[0].ReleaseTitle != "Inception 2010 1080p" {
		t.Fatalf("want 1 grab matching created one, got %+v", grabs)
	}
	if grabs[0].Status != "grabbed" {
		t.Errorf("want status grabbed, got %q", grabs[0].Status)
	}

	if err := store.UpdateGrabStatus(ctx, db, id, "downloading", sql.NullString{}); err != nil {
		t.Fatalf("update grab status: %v", err)
	}
	grabs, err = store.ListGrabs(ctx, db)
	if err != nil {
		t.Fatalf("list grabs after update: %v", err)
	}
	if grabs[0].Status != "downloading" {
		t.Errorf("want status downloading after update, got %q", grabs[0].Status)
	}
}

func TestInsertGrab_RejectsZeroOrMultipleTargets(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	movieID := seedMovie(t, db)

	_, err := store.InsertGrab(ctx, db, store.Grab{
		ReleaseTitle: "no target", Indexer: "x", Protocol: "torrent", DownloadClient: "deluge", Status: "grabbed",
	})
	if err == nil {
		t.Fatal("want an error inserting a grab with no movie/series/album set")
	}

	_, err = store.InsertGrab(ctx, db, store.Grab{
		MovieID: sql.NullInt64{Int64: movieID, Valid: true}, SeriesID: sql.NullInt64{Int64: 1, Valid: true},
		ReleaseTitle: "two targets", Indexer: "x", Protocol: "torrent", DownloadClient: "deluge", Status: "grabbed",
	})
	if err == nil {
		t.Fatal("want an error inserting a grab with both movie and series set")
	}
	if !strings.Contains(err.Error(), "grab") {
		t.Errorf("want a wrapped 'insert grab' error, got %v", err)
	}
}
