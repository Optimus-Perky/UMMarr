package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestPreferredWords(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if words, err := store.ListPreferredWords(ctx, db); err != nil || len(words) != 0 {
		t.Fatalf("want no preferred words yet, got %v, %v", words, err)
	}

	idNeg, err := store.CreatePreferredWord(ctx, db, "REPACK", -5)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	idPos, err := store.CreatePreferredWord(ctx, db, "x265", 10)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	words, err := store.ListPreferredWords(ctx, db)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(words) != 2 {
		t.Fatalf("want 2 preferred words, got %d", len(words))
	}
	// Alphabetical by term: "REPACK" before "x265".
	if words[0].ID != idNeg || words[0].Score != -5 || words[1].ID != idPos || words[1].Score != 10 {
		t.Errorf("want [REPACK -5, x265 10] in that order, got %+v", words)
	}

	if err := store.UpdatePreferredWord(ctx, db, idPos, "x265", 100); err != nil {
		t.Fatalf("update: %v", err)
	}
	if words, err := store.ListPreferredWords(ctx, db); err != nil || words[1].Score != 100 {
		t.Errorf("want x265's score updated to 100, got %+v, %v", words, err)
	}
	if err := store.UpdatePreferredWord(ctx, db, 999999, "x265", 5); !errors.Is(err, store.ErrPreferredWordNotFound) {
		t.Errorf("update unknown id: want ErrPreferredWordNotFound, got %v", err)
	}

	if err := store.DeletePreferredWord(ctx, db, idNeg); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if words, err := store.ListPreferredWords(ctx, db); err != nil || len(words) != 1 || words[0].ID != idPos {
		t.Errorf("want only x265 left, got %+v, %v", words, err)
	}

	if err := store.DeletePreferredWord(ctx, db, 999999); !errors.Is(err, store.ErrPreferredWordNotFound) {
		t.Errorf("want ErrPreferredWordNotFound, got %v", err)
	}
}
