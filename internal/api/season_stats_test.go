package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// A season's count badge is green when nothing is actually missing -
// episodes that haven't aired don't count against it - and takes the
// Downloading colour when the only gap is a download in flight.
func TestSeasonStats_ColourFollowsWhatIsOutstanding(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()
	seriesID := seedTestSeries(t, db)
	var seasonID, firstEpisode int64
	db.QueryRow(`SELECT season_id, id FROM episodes WHERE series_id = ?`, seriesID).Scan(&seasonID, &firstEpisode)
	aired, future := time.Now().AddDate(0, 0, -7), time.Now().AddDate(0, 0, 14)
	db.Exec(`UPDATE episodes SET air_date = ? WHERE id = ?`, aired.Format("2006-01-02"), firstEpisode)
	store.AttachEpisodeFile(ctx, db, firstEpisode, "Season 01/ep1.mkv", 1)
	unaired, _ := store.UpsertEpisode(ctx, db, seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: 2, Title: metadata.Field[string]{Value: "Next month", Provider: "tmdb"}, AirDate: metadata.Field[*time.Time]{Value: &future, Provider: "tmdb"}})
	_ = unaired

	badge := func(t *testing.T) string {
		t.Helper()
		_, body := get(t, srv, "/tv/"+itoa(seriesID))
		i := strings.Index(body, `id="season-stats-1"`)
		if i < 0 {
			t.Fatal("no season badge on the page")
		}
		return body[i : i+140]
	}
	if got := badge(t); !strings.Contains(got, "chip-good") || !strings.Contains(got, "1/2") {
		t.Errorf("one downloaded, one not yet aired: want a green 1/2, got %q", got)
	}

	// An episode that aired without a file turns it amber.
	third, _ := store.UpsertEpisode(ctx, db, seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: 3, Title: metadata.Field[string]{Value: "Aired", Provider: "tmdb"}, AirDate: metadata.Field[*time.Time]{Value: &aired, Provider: "tmdb"}})
	if got := badge(t); !strings.Contains(got, "chip-warn") {
		t.Errorf("an aired episode with no file: want amber, got %q", got)
	}

	// Once it's downloading, the badge takes the Downloading colour.
	db.Exec(`INSERT INTO grabs (series_id, season_number, episode_number, release_title, indexer, protocol, download_client, status) VALUES (?, 1, 3, 'x', 'ix', 'torrent', 'deluge', 'downloading')`, seriesID)
	_ = third
	if got := badge(t); !strings.Contains(got, "chip-info") {
		t.Errorf("the only gap is downloading: want the Downloading colour, got %q", got)
	}

	// The live poll swaps these badges in every few seconds, so it has to
	// colour them the same way - otherwise they go plain while you watch.
	_, polled := get(t, srv, "/tv/"+itoa(seriesID)+"/episodes/status")
	i := strings.Index(polled, `id="season-stats-1"`)
	if i < 0 {
		t.Fatal("the poll should re-render the season badge")
	}
	end := i + 140
	if end > len(polled) {
		end = len(polled)
	}
	if got := polled[i:end]; !strings.Contains(got, "chip-info") {
		t.Errorf("the polled badge lost its colour: %q", got)
	}
}
