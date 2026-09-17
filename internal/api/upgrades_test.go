package api_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// TestQualityProfile_Upgrades: the profile editor saves Radarr's upgrade
// rule, and the Movies page flags a file below the cutoff as Cutoff Unmet.
func TestQualityProfile_Upgrades(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	movieID := seedTestMovie(t, db)
	profiles, _ := store.ListQualityProfiles(t.Context(), db)
	profile := profiles[0]
	fileID, err := store.InsertMovieFile(t.Context(), db, movieID, "Inception (2010).mkv", 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE movie_files SET quality = '{"source":"HDTV","resolution":"720p"}' WHERE id = ?`, fileID); err != nil {
		t.Fatal(err)
	}

	_, body := get(t, srv, "/settings/quality-profiles/"+itoa(profile.ID))
	for _, want := range []string{`name="upgrade_allowed"`, `name="cutoff"`, `<option value="">Best allowed quality</option>`, `<option value="Bluray-1080p"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("want %q on the profile editor, got:\n%s", want, body)
		}
	}
	if !strings.Contains(body, `name="upgrade_allowed" checked`) {
		t.Fatalf("want a new profile to allow upgrades, as in Radarr")
	}
	_, body = get(t, srv, "/movies")
	if !strings.Contains(body, `data-cutoffunmet="1"`) {
		t.Fatalf("want a 720p file below an unset cutoff flagged, got:\n%s", body)
	}

	form := url.Values{"upgrade_allowed": {"on"}, "cutoff": {"Bluray-1080p"}}
	for _, q := range []string{"HDTV-720p", "WEBDL-1080p", "Bluray-1080p", "Remux-2160p"} {
		form.Set("allowed_"+q, "on")
	}
	form.Set("weight_HDTV-720p", "10")
	form.Set("weight_WEBDL-1080p", "20")
	form.Set("weight_Bluray-1080p", "30")
	form.Set("weight_Remux-2160p", "40")
	if resp, b := postForm(t, srv, "/settings/quality-profiles/"+itoa(profile.ID)+"/items", form); resp.StatusCode != 200 {
		t.Fatalf("save profile: %d %s", resp.StatusCode, b)
	}
	profiles, _ = store.ListQualityProfiles(t.Context(), db)
	if !profiles[0].UpgradeAllowed || profiles[0].Cutoff != "Bluray-1080p" {
		t.Fatalf("want upgrades on until Bluray-1080p, got %+v", profiles[0])
	}
	_, body = get(t, srv, "/movies")
	for _, want := range []string{`data-cutoffunmet="1"`, `<option value="cutoff">Cutoff Unmet</option>`} {
		if !strings.Contains(body, want) {
			t.Fatalf("want %q on the Movies page, got:\n%s", want, body)
		}
	}
}

// TestQualityProfile_UpgradesOff: turning upgrades off clears the flag.
func TestQualityProfile_UpgradesOff(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	movieID := seedTestMovie(t, db)
	profiles, _ := store.ListQualityProfiles(t.Context(), db)
	fileID, _ := store.InsertMovieFile(t.Context(), db, movieID, "Inception (2010).mkv", 5)
	db.Exec(`UPDATE movie_files SET quality = '{"source":"HDTV","resolution":"720p"}' WHERE id = ?`, fileID)
	form := url.Values{"cutoff": {""}}
	for _, q := range []string{"HDTV-720p", "Bluray-1080p"} {
		form.Set("allowed_"+q, "on")
		form.Set("weight_"+q, "10")
	}
	postForm(t, srv, "/settings/quality-profiles/"+itoa(profiles[0].ID)+"/items", form)
	_, body := get(t, srv, "/movies")
	if !strings.Contains(body, `data-cutoffunmet="0"`) {
		t.Fatalf("want no cutoff-unmet flag with upgrades off, got:\n%s", body)
	}
}

// TestTVPage_UnmonitoredSeasonIsntMissing: an unmonitored season's episodes
// don't count as wanted on the TV page - no missing count, no red bar.
func TestTVPage_UnmonitoredSeasonIsntMissing(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	seriesID := seedTestSeries(t, db)
	season2, err := store.UpsertSeason(t.Context(), db, seriesID, metadata.SeasonMetadata{SeasonNumber: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertEpisode(t.Context(), db, seriesID, season2, 2, metadata.EpisodeMetadata{EpisodeNumber: 1, Title: metadata.Field[string]{Value: "S2E1", Provider: "tmdb"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE seasons SET monitored = 1 WHERE series_id = ?`, seriesID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE episodes SET monitored = 1, air_date = '2020-01-01' WHERE series_id = ?`, seriesID); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, srv, "/tv")
	if !strings.Contains(body, `data-episodes="2" data-missing="2"`) {
		t.Fatalf("want both aired episodes missing to start with, got:\n%s", body)
	}
	if _, err := db.Exec(`UPDATE seasons SET monitored = 0 WHERE series_id = ? AND season_number = 2`, seriesID); err != nil {
		t.Fatal(err)
	}
	_, body = get(t, srv, "/tv")
	if !strings.Contains(body, `data-episodes="1" data-missing="1"`) {
		t.Fatalf("want the unmonitored season left out of the count, got:\n%s", body)
	}
}
