package sync

import (
	"context"
	"testing"

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
