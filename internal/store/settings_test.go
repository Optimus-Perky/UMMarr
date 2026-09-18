package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestCreateAndListRootFolders(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	id, err := store.CreateRootFolder(ctx, db, "/media/movies", "movie")
	if err != nil {
		t.Fatalf("create root folder: %v", err)
	}
	if _, err := store.CreateRootFolder(ctx, db, "/media/tv", "series"); err != nil {
		t.Fatalf("create second root folder: %v", err)
	}

	folders, err := store.ListRootFolders(ctx, db, "movie")
	if err != nil {
		t.Fatalf("list root folders: %v", err)
	}
	if len(folders) != 1 || folders[0].ID != id || folders[0].Path != "/media/movies" {
		t.Fatalf("want 1 movie root folder matching created one, got %+v", folders)
	}
}

func TestCreateAndListQualityProfiles(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if _, err := store.CreateQualityProfile(ctx, db, "HD-1080p"); err != nil {
		t.Fatalf("create quality profile: %v", err)
	}
	if _, err := store.CreateQualityProfile(ctx, db, "Any"); err != nil {
		t.Fatalf("create second quality profile: %v", err)
	}

	profiles, err := store.ListQualityProfiles(ctx, db)
	if err != nil {
		t.Fatalf("list quality profiles: %v", err)
	}
	// The two created here, plus the audio profile migration 47 seeds so
	// music has something to point at from the start.
	var video, audio []store.QualityProfile
	for _, p := range profiles {
		if p.MediaKind == store.MediaKindAudio {
			audio = append(audio, p)
		} else {
			video = append(video, p)
		}
	}
	if len(video) != 2 {
		t.Fatalf("want the 2 created video profiles, got %d of %d", len(video), len(profiles))
	}
	if len(audio) != 1 {
		t.Fatalf("want the seeded audio profile, got %d", len(audio))
	}
	items, err := store.GetQualityProfileItems(ctx, db, audio[0].ID)
	if err != nil {
		t.Fatalf("audio profile items: %v", err)
	}
	if len(items) != len(releaseparse.AllAudioQualities) || items[0].Quality != "Unknown" {
		t.Fatalf("want the audio profile built from the audio catalog, got %d rows starting %q", len(items), items[0].Quality)
	}
	var flac24 releaseparse.QualityProfileItem
	for _, it := range items {
		if it.Quality == "FLAC-24bit" {
			flac24 = it
		}
	}
	if !flac24.Allowed || flac24.Weight == 0 {
		t.Errorf("want FLAC-24bit allowed and weighted, got %+v", flac24)
	}
}

// TestCreateQualityProfile_SeedsDefaultWeights proves a freshly created
// profile is immediately useful (every catalog quality allowed, weighted
// by catalog order) without anything being configured first: weight-based
// grabbing needs a sane starting point, not a blank slate.
func TestCreateQualityProfile_SeedsDefaultWeights(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	profileID, err := store.CreateQualityProfile(ctx, db, "HD-1080p")
	if err != nil {
		t.Fatalf("create quality profile: %v", err)
	}

	items, err := store.GetQualityProfileItems(ctx, db, profileID)
	if err != nil {
		t.Fatalf("get quality profile items: %v", err)
	}
	if len(items) != len(releaseparse.AllQualities) {
		t.Fatalf("want %d items (one per catalog entry), got %d", len(releaseparse.AllQualities), len(items))
	}
	for i, it := range items {
		if !it.Allowed {
			t.Fatalf("want every catalog entry allowed by default, %q was not", it.Quality)
		}
		if it.Weight != i {
			t.Fatalf("want weight %d (catalog order) for %q, got %d", i, it.Quality, it.Weight)
		}
	}
}

// TestGetQualityProfileItems_DefaultsMissingEntriesSafely proves a
// partial/stale saved list (e.g. from before AllQualities grew, or a
// hand-edited row) still returns the full catalog, degrading unknown
// entries to weight=0/disallowed instead of erroring or omitting them.
func TestGetQualityProfileItems_DefaultsMissingEntriesSafely(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	profileID, err := store.CreateQualityProfile(ctx, db, "Any")
	if err != nil {
		t.Fatalf("create quality profile: %v", err)
	}
	if err := store.UpdateQualityProfileItems(ctx, db, profileID, []releaseparse.QualityProfileItem{
		{Quality: "Bluray-1080p", Weight: 100, Allowed: true},
	}); err != nil {
		t.Fatalf("update quality profile items: %v", err)
	}

	items, err := store.GetQualityProfileItems(ctx, db, profileID)
	if err != nil {
		t.Fatalf("get quality profile items: %v", err)
	}
	if len(items) != len(releaseparse.AllQualities) {
		t.Fatalf("want the full catalog returned regardless of what was saved, got %d items", len(items))
	}
	var sawBluray1080p, sawUnknown bool
	for _, it := range items {
		if it.Quality == "Bluray-1080p" {
			sawBluray1080p = true
			if it.Weight != 100 || !it.Allowed {
				t.Fatalf("want the saved Bluray-1080p entry preserved, got %+v", it)
			}
		}
		if it.Quality == "Unknown" {
			sawUnknown = true
			if it.Weight != 0 || it.Allowed {
				t.Fatalf("want a never-saved entry to default to weight=0/disallowed, got %+v", it)
			}
		}
	}
	if !sawBluray1080p || !sawUnknown {
		t.Fatalf("want both a saved and a never-saved catalog entry present, got %+v", items)
	}
}

// TestUpdateQualityProfileItems_Persists proves a full save round-trips.
func TestUpdateQualityProfileItems_Persists(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	profileID, err := store.CreateQualityProfile(ctx, db, "Any")
	if err != nil {
		t.Fatalf("create quality profile: %v", err)
	}

	want := make([]releaseparse.QualityProfileItem, len(releaseparse.AllQualities))
	for i, quality := range releaseparse.AllQualities {
		want[i] = releaseparse.QualityProfileItem{Quality: quality, Weight: i * 10, Allowed: i%2 == 0}
	}
	if err := store.UpdateQualityProfileItems(ctx, db, profileID, want); err != nil {
		t.Fatalf("update quality profile items: %v", err)
	}

	got, err := store.GetQualityProfileItems(ctx, db, profileID)
	if err != nil {
		t.Fatalf("get quality profile items: %v", err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d: want %+v, got %+v", i, want[i], got[i])
		}
	}
}

// TestDefaultQualityProfile: the first profile is the default until
// Settings moves it, and only one profile is ever the default.
func TestDefaultQualityProfile(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	first, err := store.CreateQualityProfile(ctx, db, "HD-1080p")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	second, err := store.CreateQualityProfile(ctx, db, "Any")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	defaultOf := func() int64 {
		t.Helper()
		profiles, err := store.ListQualityProfiles(ctx, db)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var id int64
		for _, p := range profiles {
			if p.IsDefault {
				if id != 0 {
					t.Fatalf("two default profiles: %+v", profiles)
				}
				id = p.ID
			}
		}
		return id
	}
	if got := defaultOf(); got != first {
		t.Fatalf("want the first profile to start as the default, got %d", got)
	}
	if err := store.SetDefaultQualityProfile(ctx, db, second); err != nil {
		t.Fatalf("set default: %v", err)
	}
	if got := defaultOf(); got != second {
		t.Fatalf("want the second profile as the default after setting it, got %d", got)
	}
}

// TestRelinkMovieMetadata_RefusesATrackedTitle: pointing a movie at
// metadata another movie already uses is refused rather than merging them.
func TestRelinkMovieMetadata_RefusesATrackedTitle(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	profile, _ := store.CreateQualityProfile(ctx, db, "Any")
	root, _ := store.CreateRootFolder(ctx, db, t.TempDir(), "movie")
	a, _ := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{Title: metadata.Field[string]{Value: "A", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "1"}})
	b, _ := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{Title: metadata.Field[string]{Value: "B", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "2"}})
	movieA, _ := store.UpsertMovie(ctx, db, a, profile, root, true)
	if _, err := store.UpsertMovie(ctx, db, b, profile, root, true); err != nil {
		t.Fatal(err)
	}
	if err := store.RelinkMovieMetadata(ctx, db, movieA, b); !errors.Is(err, store.ErrAlreadyTracked) {
		t.Fatalf("want ErrAlreadyTracked, got %v", err)
	}
}
