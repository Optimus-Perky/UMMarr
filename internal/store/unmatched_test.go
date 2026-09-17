package store_test

import (
	"context"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestUnmatchedFolders(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	movieRoot := seedRootFolder(t, db, "movie")
	musicRoot := seedRootFolder(t, db, "music")

	if err := store.RecordUnmatchedFolder(ctx, db, store.UnmatchedFolder{RootFolderID: movieRoot, Kind: "movie", Path: "/media/movie/Some Film (2019)", Name: "Some Film (2019)"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordUnmatchedFolder(ctx, db, store.UnmatchedFolder{RootFolderID: musicRoot, Kind: "music", Path: "/media/music/Band/Album", Name: "Band / Album", Reason: store.UnmatchedNoAlbums, Detail: "loose tracks"}); err != nil {
		t.Fatal(err)
	}
	// The same folder again refreshes the row rather than adding another.
	if err := store.RecordUnmatchedFolder(ctx, db, store.UnmatchedFolder{RootFolderID: movieRoot, Kind: "movie", Path: "/media/movie/Some Film (2019)", Name: "Some Film (2019)", Reason: store.UnmatchedDuplicate, Detail: "already at /media/movie/Some Film"}); err != nil {
		t.Fatal(err)
	}
	list, err := store.ListUnmatchedFolders(ctx, db, false)
	if err != nil || len(list) != 2 {
		t.Fatalf("want two rows, got %d (%v)", len(list), err)
	}
	if list[0].Kind != "movie" || list[0].Reason != store.UnmatchedDuplicate || list[0].Detail != "already at /media/movie/Some Film" || list[0].ReasonLabel() != "Already in the library" {
		t.Fatalf("want the movie row refreshed, got %+v", list[0])
	}
	if list[1].Name != "Band / Album" || list[1].ReasonLabel() != "No album folders" {
		t.Fatalf("music row: %+v", list[1])
	}
	if n, _ := store.CountUnmatchedFolders(ctx, db); n != 2 {
		t.Fatalf("want 2 counted, got %d", n)
	}

	if err := store.IgnoreUnmatchedFolder(ctx, db, list[1].ID, true); err != nil {
		t.Fatal(err)
	}
	if shown, _ := store.ListUnmatchedFolders(ctx, db, false); len(shown) != 1 {
		t.Errorf("want the ignored row hidden, got %d", len(shown))
	}
	if all, _ := store.ListUnmatchedFolders(ctx, db, true); len(all) != 2 || !all[1].Ignored {
		t.Errorf("want the ignored row still listed when asked for, got %+v", all)
	}
	if n, _ := store.CountUnmatchedFolders(ctx, db); n != 1 {
		t.Error("want an ignored row left out of the count")
	}

	if err := store.ResolveUnmatchedFolder(ctx, db, "/media/movie/Some Film (2019)"); err != nil {
		t.Fatal(err)
	}
	if shown, _ := store.ListUnmatchedFolders(ctx, db, true); len(shown) != 1 || shown[0].Kind != "music" {
		t.Fatalf("want the resolved row gone, got %+v", shown)
	}
	// Seeing it again on a later scan brings it back.
	store.RecordUnmatchedFolder(ctx, db, store.UnmatchedFolder{RootFolderID: movieRoot, Kind: "movie", Path: "/media/movie/Some Film (2019)", Name: "Some Film (2019)"})
	if shown, _ := store.ListUnmatchedFolders(ctx, db, false); len(shown) != 1 || shown[0].Kind != "movie" {
		t.Fatalf("want a folder still unmatched on the next scan back on the report, got %+v", shown)
	}
	got, found, err := store.GetUnmatchedFolder(ctx, db, list[0].ID)
	if err != nil || !found || got.Path != "/media/movie/Some Film (2019)" {
		t.Fatalf("get: %+v %v %v", got, found, err)
	}
	if _, found, _ := store.GetUnmatchedFolder(ctx, db, 9999); found {
		t.Error("want a missing row reported as not found")
	}
	if err := store.RecordUnmatchedFolder(ctx, db, store.UnmatchedFolder{Kind: "movie"}); err == nil {
		t.Error("want a row without a path refused")
	}
}
