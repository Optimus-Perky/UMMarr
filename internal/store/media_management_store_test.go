package store_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestUpdateRenameFiles_OnlyChangesThatMediaType(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := store.UpdateRenameFiles(ctx, db, "series", false); err != nil {
		t.Fatalf("update rename files: %v", err)
	}
	for mediaType, want := range map[string]bool{"movie": true, "series": false, "music": true} {
		c, err := store.GetNamingConfig(ctx, db, mediaType)
		if err != nil {
			t.Fatalf("get naming config %s: %v", mediaType, err)
		}
		if c.RenameFiles != want {
			t.Errorf("%s: want rename_files=%v, got %v", mediaType, want, c.RenameFiles)
		}
	}
}

func TestItemFolderPaths(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	movieID := seedMovie(t, db)
	var want string
	if err := db.QueryRow(`SELECT path FROM movies WHERE id = ?`, movieID).Scan(&want); err != nil {
		t.Fatalf("read movie path: %v", err)
	}
	got, err := store.ItemFolderPaths(ctx, db, "movie")
	if err != nil {
		t.Fatalf("item folder paths: %v", err)
	}
	if !reflect.DeepEqual(got, []string{want}) {
		t.Fatalf("want [%s], got %v", want, got)
	}
	if _, err := store.ItemFolderPaths(ctx, db, "books"); err == nil {
		t.Errorf("want an error for an unknown media type")
	}
}
