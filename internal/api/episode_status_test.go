package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// An episode that hasn't aired yet reads as Unreleased, not Missing - the
// series page's counts already leave unaired episodes out.
func TestSeriesPage_UnairedEpisodesAreUnreleased(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()
	seriesID := seedTestSeries(t, db)
	var seasonID int64
	db.QueryRow(`SELECT season_id FROM episodes WHERE series_id = ?`, seriesID).Scan(&seasonID)
	aired := time.Now().AddDate(0, 0, -7)
	future := time.Now().AddDate(0, 0, 14)
	past, _ := store.UpsertEpisode(ctx, db, seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: 2, Title: metadata.Field[string]{Value: "Aired", Provider: "tmdb"}, AirDate: metadata.Field[*time.Time]{Value: &aired, Provider: "tmdb"}})
	soon, _ := store.UpsertEpisode(ctx, db, seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: 3, Title: metadata.Field[string]{Value: "Next week", Provider: "tmdb"}, AirDate: metadata.Field[*time.Time]{Value: &future, Provider: "tmdb"}})
	undated, _ := store.UpsertEpisode(ctx, db, seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: 4, Title: metadata.Field[string]{Value: "No date yet", Provider: "tmdb"}})

	_, body := get(t, srv, "/tv/"+itoa(seriesID))
	cell := func(id int64) string {
		marker := `id="episode-status-` + itoa(id) + `"`
		i := strings.Index(body, marker)
		if i < 0 {
			t.Fatalf("no status cell for episode %d", id)
		}
		return body[i : i+140]
	}
	if got := cell(past); !strings.Contains(got, "chip-warn") || !strings.Contains(got, ">Missing<") {
		t.Errorf("an aired episode with no file is Missing, got %q", got)
	}
	if got := cell(soon); !strings.Contains(got, "chip-muted") || !strings.Contains(got, ">Unreleased<") {
		t.Errorf("an episode airing next week is Unreleased, got %q", got)
	}
	if got := cell(undated); !strings.Contains(got, ">Missing<") {
		t.Errorf("an episode with no air date at all stays Missing, got %q", got)
	}
	_, dialog := get(t, srv, "/tv/"+itoa(seriesID)+"/episodes/"+itoa(soon))
	if !strings.Contains(dialog, "Unreleased") {
		t.Errorf("want the episode dialog to say Unreleased too, got:\n%s", dialog)
	}
}
