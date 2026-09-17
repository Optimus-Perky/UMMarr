package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestIndexers_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	in := store.Indexer{
		Name: "IPTorrents (Prowlarr)", Implementation: "Torznab",
		EnableRSS: true, EnableInteractiveSearch: true, Priority: 10,
		BaseURL: "http://prowlarr:9696/3/", APIPath: "/api", APIKey: "key",
		Categories: []int{2000, 5000}, AnimeCategories: []int{5070}, AdditionalParameters: "&x=1",
		MinimumSeeders: 2, SeedRatio: sql.NullFloat64{Float64: 1.5, Valid: true}, SeedTime: sql.NullInt64{Int64: 60, Valid: true},
		RequiredFlags: []int{1}, Tags: []int{3}, Synced: true, ExtraFields: []byte(`[{"name":"multiLanguages","value":[]}]`),
	}
	id, err := store.CreateIndexer(ctx, db, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := store.GetIndexer(ctx, db, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != in.Name || got.Priority != 10 || len(got.Categories) != 2 || got.AnimeCategories[0] != 5070 ||
		!got.SeedRatio.Valid || got.SeedRatio.Float64 != 1.5 || got.SeedTime.Int64 != 60 || got.RequiredFlags[0] != 1 ||
		!got.Synced || string(got.ExtraFields) != string(in.ExtraFields) || got.Protocol() != "torrent" || got.EnableAutomaticSearch {
		t.Fatalf("round trip lost something: %+v", got)
	}

	got.Name, got.Categories = "Renamed", []int{3000}
	if err := store.UpdateIndexer(ctx, db, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	list, err := store.ListIndexers(ctx, db)
	if err != nil || len(list) != 1 || list[0].Name != "Renamed" || list[0].Categories[0] != 3000 {
		t.Fatalf("want the update listed, got %+v %v", list, err)
	}

	if err := store.DeleteIndexer(ctx, db, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetIndexer(ctx, db, id); !errors.Is(err, store.ErrIndexerNotFound) {
		t.Fatalf("want ErrIndexerNotFound after delete, got %v", err)
	}
	if err := store.DeleteIndexer(ctx, db, id); !errors.Is(err, store.ErrIndexerNotFound) {
		t.Fatalf("want ErrIndexerNotFound deleting twice, got %v", err)
	}
}

func TestIndexers_FailuresBackOffAndRecover(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	id, err := store.CreateIndexer(ctx, db, store.Indexer{Name: "x", Implementation: "Torznab", Priority: 25, BaseURL: "http://x", APIPath: "/api"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := store.RecordIndexerFailure(ctx, db, id, "timed out", now); err != nil {
			t.Fatalf("record failure: %v", err)
		}
	}
	ix, _ := store.GetIndexer(ctx, db, id)
	if ix.Failures != 3 || ix.LastError != "timed out" || !ix.BackedOff(now.Add(9*time.Minute)) || ix.BackedOff(now.Add(11*time.Minute)) {
		t.Fatalf("want 3 failures resting the indexer 10 minutes, got failures %d until %v", ix.Failures, ix.DisabledUntil)
	}
	if err := store.RecordIndexerSuccess(ctx, db, id); err != nil {
		t.Fatalf("record success: %v", err)
	}
	ix, _ = store.GetIndexer(ctx, db, id)
	if ix.Failures != 0 || ix.LastError != "" || ix.BackedOff(now) {
		t.Fatalf("want a success to clear the failures, got %+v", ix)
	}
	if store.IndexerBackoff(100) != 24*time.Hour {
		t.Fatalf("want the back-off capped at a day")
	}
}

func TestAPIKey_GeneratedOnceAndRegenerated(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	first, err := store.EnsureAPIKey(ctx, db)
	if err != nil || len(first) != 32 {
		t.Fatalf("want a 32 character key, got %q %v", first, err)
	}
	again, _ := store.EnsureAPIKey(ctx, db)
	if again != first {
		t.Fatalf("want the same key kept")
	}
	next, _ := store.RegenerateAPIKey(ctx, db)
	if next == first || len(next) != 32 {
		t.Fatalf("want a new key, got %q", next)
	}
}

func TestAbsorbConvertedIndexer(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	converted := store.Indexer{Name: "IPTorrents (Prowlarr)", Implementation: "Torznab", Priority: 25, BaseURL: "http://prowlarr:9696/3/", APIPath: "/api",
		Categories: []int{2000, 2040, 5000, 3000}, AnimeCategories: []int{5070}, Converted: true}
	convertedID, _ := store.CreateIndexer(ctx, db, converted)
	other := converted
	other.BaseURL = "http://prowlarr:9696/4/"
	otherID, _ := store.CreateIndexer(ctx, db, other)

	if taken, _ := store.IndexerNameTaken(ctx, db, "iptorrents (prowlarr)", 0); taken {
		t.Fatalf("want converted entries not to block Prowlarr's names")
	}

	synced := store.Indexer{Name: "IPTorrents (Prowlarr)", Implementation: "Torznab", Priority: 25, BaseURL: "http://prowlarr:9696/3", APIPath: "/api",
		Categories: []int{2000}, Synced: true}
	synced.ID, _ = store.CreateIndexer(ctx, db, synced)
	if taken, _ := store.IndexerNameTaken(ctx, db, "IPTorrents (Prowlarr)", 0); !taken {
		t.Fatalf("want a synced indexer's name taken")
	}
	if taken, _ := store.IndexerNameTaken(ctx, db, "IPTorrents (Prowlarr)", synced.ID); taken {
		t.Fatalf("want an indexer's own name not to count against it")
	}

	if err := store.AbsorbConvertedIndexer(ctx, db, synced); err != nil {
		t.Fatalf("absorb: %v", err)
	}
	c, err := store.GetIndexer(ctx, db, convertedID)
	if err != nil || len(c.Categories) != 2 || c.Categories[0] != 5000 || c.Categories[1] != 3000 || len(c.AnimeCategories) != 1 {
		t.Fatalf("want a synced entry with any movie category to take all movie categories, leaving TV, audio and anime, got %+v %v", c, err)
	}

	synced.Categories = []int{2000, 5000, 3000}
	if err := store.AbsorbConvertedIndexer(ctx, db, synced); err != nil {
		t.Fatalf("absorb: %v", err)
	}
	if _, err := store.GetIndexer(ctx, db, convertedID); !errors.Is(err, store.ErrIndexerNotFound) {
		t.Fatalf("want the fully covered converted entry removed, got %v", err)
	}
	if _, err := store.GetIndexer(ctx, db, otherID); err != nil {
		t.Fatalf("want another endpoint's converted entry left alone, got %v", err)
	}
}
