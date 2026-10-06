package sync

import (
	"context"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// A search for an item leaves out indexers restricted to tags it doesn't
// have, so they're not even asked; RSS, which is for no one item, still
// reads every feed. A tag id UMMarr doesn't know restricts nothing.
func TestUsable_SkipsTagRestrictedIndexersForAnItem(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	anime, _ := store.EnsureTags(ctx, db, []string{"anime"})
	add := func(name, host string, tags []int) {
		t.Helper()
		if _, err := store.CreateIndexer(ctx, db, store.Indexer{Name: name, Implementation: "Torznab", BaseURL: "http://" + host,
			Priority: 25, EnableRSS: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true, Categories: []int{2000}, Tags: tags}); err != nil {
			t.Fatal(err)
		}
	}
	add("General", "general", nil)
	add("Nyaa", "nyaa", []int{int(anime[0])})
	add("Synced", "synced", []int{999})

	svc := &IndexerService{DB: db}
	names := func(ixs []store.Indexer) map[string]bool {
		out := map[string]bool{}
		for _, ix := range ixs {
			out[ix.Name] = true
		}
		return out
	}
	plain, err := svc.usable(ctx, PurposeAutomatic, "movie", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(plain); !got["General"] || got["Nyaa"] || !got["Synced"] {
		t.Errorf("an untagged movie: want General and Synced asked, not Nyaa; got %v", got)
	}
	tagged, _ := svc.usable(ctx, PurposeInteractive, "movie", anime, true)
	if got := names(tagged); !got["General"] || !got["Nyaa"] {
		t.Errorf("an anime movie: want Nyaa asked too; got %v", got)
	}
	rss, _ := svc.usable(ctx, PurposeRSS, "", nil, false)
	if got := names(rss); !got["Nyaa"] {
		t.Errorf("RSS: want every feed read, Nyaa included; got %v", got)
	}
}
