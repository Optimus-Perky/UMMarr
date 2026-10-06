package sync

import (
	"context"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// A series whose TMDB id is unusable can't be refreshed; the refresh counts it as
// failed and carries on rather than stopping the whole task.
func TestRefreshLibrary_SkipsWhatItCannotRefresh(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	root, _ := store.CreateRootFolder(ctx, db, t.TempDir(), "series")
	profile, _ := store.CreateQualityProfile(ctx, db, "Any")
	for _, title := range []string{"Orphan One", "Orphan Two"} {
		metaID, err := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{Title: metadata.Field[string]{Value: title, Provider: "tvmaze"}, ExternalIDs: map[string]string{"tmdb": "bad-" + title}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpsertSeries(ctx, db, metaID, profile, root, true); err != nil {
			t.Fatal(err)
		}
	}
	report, err := RefreshLibrary(ctx, nil, &SeriesService{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	if report.Failed != 2 || report.Series != 0 || report.Summary() != "refreshed 0 movies and 0 series, 2 failed" {
		t.Fatalf("want both counted as failed, got %+v", report)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := RefreshLibrary(cancelled, nil, &SeriesService{DB: db}); err == nil {
		t.Fatal("want a cancelled context to stop the refresh")
	}
}

// The scheduled refresh asks only about what is due: an airing series
// refreshed an hour ago isn't, one refreshed a day ago is, and an ended
// series refreshed a day ago isn't due for another month.
func TestSeriesDueForRefresh_FollowsTheCadence(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	root, _ := store.CreateRootFolder(ctx, db, t.TempDir(), "series")
	profile, _ := store.CreateQualityProfile(ctx, db, "Any")
	add := func(title, status, synced string) int64 {
		t.Helper()
		metaID, err := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{
			Title:       metadata.Field[string]{Value: title, Provider: "tmdb"},
			Status:      metadata.Field[string]{Value: status, Provider: "tmdb"},
			ExternalIDs: map[string]string{"tmdb": title},
		})
		if err != nil {
			t.Fatal(err)
		}
		id, err := store.UpsertSeries(ctx, db, metaID, profile, root, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE series_metadata SET last_info_sync = datetime('now', ?) WHERE id = ?`, synced, metaID); err != nil {
			t.Fatal(err)
		}
		return id
	}
	airingFresh := add("Airing Fresh", "Returning Series", "-1 hours")
	airingStale := add("Airing Stale", "Returning Series", "-1 days")
	endedFresh := add("Ended Fresh", "Ended", "-1 days")
	endedStale := add("Ended Stale", "Ended", "-40 days")

	due, err := store.SeriesDueForRefresh(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]bool{}
	for _, id := range due {
		got[id] = true
	}
	if got[airingFresh] || got[endedFresh] || !got[airingStale] || !got[endedStale] {
		t.Fatalf("due = %v; want only the stale airing series (%d) and the month-old ended one (%d)", due, airingStale, endedStale)
	}
}

// Movies: an old released film refreshed a week ago isn't due; one this
// year is, and so is one not yet released.
func TestMoviesDueForRefresh_FollowsTheCadence(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	root, _ := store.CreateRootFolder(ctx, db, t.TempDir(), "movie")
	profile, _ := store.CreateQualityProfile(ctx, db, "Any")
	now := time.Now()
	add := func(title, status string, year int, synced string) int64 {
		t.Helper()
		metaID, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
			Title:       metadata.Field[string]{Value: title, Provider: "tmdb"},
			Status:      metadata.Field[string]{Value: status, Provider: "tmdb"},
			Year:        metadata.Field[int]{Value: year, Provider: "tmdb"},
			ExternalIDs: map[string]string{"tmdb": title},
		})
		if err != nil {
			t.Fatal(err)
		}
		id, err := store.UpsertMovie(ctx, db, metaID, profile, root, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE movie_metadata SET last_info_sync = datetime('now', ?) WHERE id = ?`, synced, metaID); err != nil {
			t.Fatal(err)
		}
		return id
	}
	classic := add("Heat", "Released", 1995, "-7 days")
	thisYear := add("New One", "Released", now.Year(), "-1 days")
	upcoming := add("Coming Soon", "Post Production", now.Year()+1, "-1 days")

	due, err := store.MoviesDueForRefresh(ctx, db, now)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]bool{}
	for _, id := range due {
		got[id] = true
	}
	if got[classic] || !got[thisYear] || !got[upcoming] {
		t.Fatalf("due = %v; want this year's (%d) and the upcoming one (%d), not the 1995 film (%d)", due, thisYear, upcoming, classic)
	}
}
