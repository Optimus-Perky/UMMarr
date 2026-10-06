package sync

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Radarr's DownloadClientProvider: a client sharing a tag with the item
// wins; an item with no matching client uses the untagged ones; a tagged
// client is never used for other items; and an indexer can name its client.
func TestClientForRelease_FollowsTagsAndTheIndexersChoice(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	root, _ := store.CreateRootFolder(ctx, db, t.TempDir(), "movie")
	profile, _ := store.CreateQualityProfile(ctx, db, "Any")
	addMovie := func(title string, tags ...string) int64 {
		t.Helper()
		metaID, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
			Title: metadata.Field[string]{Value: title, Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": title}})
		if err != nil {
			t.Fatal(err)
		}
		id, err := store.UpsertMovie(ctx, db, metaID, profile, root, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(tags) > 0 {
			if err := store.EditMovies(ctx, db, []int64{id}, store.LibraryEdit{TagMode: "replace", Tags: tags}); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	plain := addMovie("Heat")
	anime := addMovie("Akira", "anime")
	animeTag, _ := store.EnsureTags(ctx, db, []string{"anime"})

	client := func(name string, priority int, tags []int64) int64 {
		t.Helper()
		id, err := store.CreateDownloadClient(ctx, db, store.DownloadClient{Name: name, Implementation: store.ClientQBittorrent,
			Enabled: true, Priority: priority, BaseURL: "http://127.0.0.1:1", Tags: tags})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	client("Anime box", 1, animeTag) // the best priority, but tagged
	client("Main", 5, nil)
	spare := client("Spare", 9, nil)

	svc := &DownloadService{DB: db}
	release := newznab.Release{Title: "x", Protocol: newznab.ProtocolTorrent}
	pick := func(movieID int64, r newznab.Release) string {
		t.Helper()
		_, dc, err := svc.clientForRelease(ctx, store.Grab{MovieID: sql.NullInt64{Int64: movieID, Valid: true}}, r)
		if err != nil {
			t.Fatal(err)
		}
		return dc.Name
	}
	if got := pick(plain, release); got != "Main" {
		t.Errorf("an untagged movie: want Main (the tagged client is reserved), got %s", got)
	}
	if got := pick(anime, release); got != "Anime box" {
		t.Errorf("an anime movie: want Anime box, got %s", got)
	}

	ixID, err := store.CreateIndexer(ctx, db, store.Indexer{Name: "Picky", Implementation: "Torznab", BaseURL: "http://127.0.0.1:1", Priority: 25, DownloadClientID: int(spare)})
	if err != nil {
		t.Fatal(err)
	}
	fromPicky := release
	fromPicky.IndexerID = ixID
	if got := pick(plain, fromPicky); got != "Spare" {
		t.Errorf("an indexer naming its client: want Spare, got %s", got)
	}
}
