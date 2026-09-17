package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestDeleteRootFolder(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	exists := func(id int64) bool {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM root_folders WHERE id = ?`, id).Scan(&n); err != nil {
			t.Fatalf("count root folder: %v", err)
		}
		return n == 1
	}

	t.Run("a folder the user added can be removed", func(t *testing.T) {
		id, err := store.CreateRootFolder(ctx, db, "/Something", "movie")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := store.DeleteRootFolder(ctx, db, id); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if exists(id) {
			t.Fatalf("want the root folder gone")
		}
	})

	t.Run("built-in folders are refused", func(t *testing.T) {
		for _, p := range []string{"/data/Movies", "/data/TV", "/data/Music/"} {
			id, err := store.CreateRootFolder(ctx, db, p, "movie")
			if err != nil {
				t.Fatalf("create %s: %v", p, err)
			}
			if err := store.DeleteRootFolder(ctx, db, id); !errors.Is(err, store.ErrRootFolderBuiltIn) {
				t.Errorf("%s: want ErrRootFolderBuiltIn, got %v", p, err)
			}
			if !exists(id) {
				t.Errorf("%s: want the built-in root folder kept", p)
			}
		}
	})

	t.Run("a folder something still uses is refused", func(t *testing.T) {
		movieID := seedMovie(t, db)
		var id int64
		if err := db.QueryRow(`SELECT root_folder_id FROM movies WHERE id = ?`, movieID).Scan(&id); err != nil {
			t.Fatalf("find root folder: %v", err)
		}
		if err := store.DeleteRootFolder(ctx, db, id); !errors.Is(err, store.ErrRootFolderInUse) {
			t.Errorf("want ErrRootFolderInUse, got %v", err)
		}
		if !exists(id) {
			t.Errorf("want the root folder kept")
		}
	})

	t.Run("an unknown id", func(t *testing.T) {
		if err := store.DeleteRootFolder(ctx, db, 999999); !errors.Is(err, store.ErrRootFolderNotFound) {
			t.Errorf("want ErrRootFolderNotFound, got %v", err)
		}
	})
}
