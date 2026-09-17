package merge

import (
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tvdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tvmaze"
)

// TMDB and TVMaze disagreeing on how many episodes a season has is
// settled by TMDB, not by taking both. TVMaze splits a double-length
// premiere into two entries, which shifts the rest of the season and left
// an episode nobody has sitting next to the real finale - Shrinking
// season 3, 2026-09-16. The mismatch is still flagged in MergeReport.
func TestMergeSeasons_EpisodeListComesFromOneProvider(t *testing.T) {
	tmdbSeason3 := adaptTMDBSeason(3, &tmdb.Season{
		SeasonNumber: 3,
		Episodes: []tmdb.Episode{
			{EpisodeNumber: 1, Name: "Ep1 TMDB", Overview: "tmdb overview 1", AirDate: "2015-01-01"},
			{EpisodeNumber: 2, Name: "Ep2 TMDB", Overview: "tmdb overview 2", AirDate: "2015-01-08"},
		},
	})

	tvmazeEpisodes := []tvmaze.Episode{
		{Season: 3, Number: 1, Name: "Ep1 TVMaze", Airdate: "2015-01-01"},
		{Season: 3, Number: 2, Name: "Ep2 TVMaze", Airdate: "2015-01-08"},
		{Season: 3, Number: 3, Name: "Ep3 TVMaze only", Airdate: "2015-01-15"},
	}
	tvmazeSeasons := groupTVMazeEpisodesBySeason(tvmazeEpisodes)

	seasons, report := MergeSeasons([][]seasonSource{{tmdbSeason3}, tvmazeSeasons}, Options{})

	if len(seasons) != 1 {
		t.Fatalf("want 1 season, got %d", len(seasons))
	}
	season := seasons[0]
	if season.SeasonNumber != 3 {
		t.Fatalf("want season 3, got %d", season.SeasonNumber)
	}
	if len(season.Episodes) != 2 {
		t.Fatalf("want TMDB's 2 episodes, got %d", len(season.Episodes))
	}
	for _, ep := range season.Episodes {
		if ep.EpisodeNumber == 3 {
			t.Error("the episode only TVMaze listed should not be in the library")
		}
	}

	// TVMaze still describes the episodes TMDB does list, where TMDB left
	// a field empty - it just does not get to add or remove any.
	ep1 := season.Episodes[0]
	if ep1.Title.Value != "Ep1 TMDB" || ep1.Title.Provider != "tmdb" {
		t.Fatalf("want episode 1 title from tmdb, got %+v", ep1.Title)
	}
	if ep1.AirDate.Provider != "tmdb" {
		t.Fatalf("want episode 1 air_date from tmdb now it leads, got provider %s", ep1.AirDate.Provider)
	}

	if len(report.Conflicts) != 1 {
		t.Fatalf("want 1 conflict flagged for the episode-count mismatch, got %v", report.Conflicts)
	}
}

// TestMergeSeasons_SingleSourceNoConflict confirms a season present in
// only one provider's data doesn't get spuriously flagged as a conflict.
func TestMergeSeasons_SingleSourceNoConflict(t *testing.T) {
	tmdbSeason1 := adaptTMDBSeason(1, &tmdb.Season{
		SeasonNumber: 1,
		Episodes: []tmdb.Episode{
			{EpisodeNumber: 1, Name: "Pilot"},
		},
	})
	seasons, report := MergeSeasons([][]seasonSource{{tmdbSeason1}}, Options{})
	if len(seasons) != 1 || len(seasons[0].Episodes) != 1 {
		t.Fatalf("want 1 season with 1 episode, got %+v", seasons)
	}
	if len(report.Conflicts) != 0 {
		t.Fatalf("want no conflicts for single-source season, got %v", report.Conflicts)
	}
}

// The Shrinking season 3 shape exactly: TVMaze splits the double-length
// premiere in two, so every later episode shifts by one and its last entry
// repeats the real finale under the same title.
func TestMergeSeasons_TVMazeSplitPremiereDoesNotAddAFinale(t *testing.T) {
	tmdbSeason := adaptTMDBSeason(3, &tmdb.Season{
		SeasonNumber: 3,
		Episodes: []tmdb.Episode{
			{EpisodeNumber: 1, Name: "My Bad", AirDate: "2026-01-28"},
			{EpisodeNumber: 2, Name: "Happiness Mission", AirDate: "2026-01-28"},
			{EpisodeNumber: 3, Name: "And That's Our Time", AirDate: "2026-04-01"},
		},
	})
	// TVMaze counts the premiere as two, pushing everything along one.
	tvmazeSeasons := groupTVMazeEpisodesBySeason([]tvmaze.Episode{
		{Season: 3, Number: 1, Name: "My Bad (1)", Airdate: "2026-01-28"},
		{Season: 3, Number: 2, Name: "My Bad (2)", Airdate: "2026-01-28"},
		{Season: 3, Number: 3, Name: "Happiness Mission", Airdate: "2026-01-28"},
		{Season: 3, Number: 4, Name: "And That's Our Time", Airdate: "2026-04-08"},
	})

	seasons, _ := MergeSeasons([][]seasonSource{{tmdbSeason}, tvmazeSeasons}, Options{})

	if len(seasons) != 1 {
		t.Fatalf("want 1 season, got %d", len(seasons))
	}
	got := seasons[0].Episodes
	if len(got) != 3 {
		t.Fatalf("want TMDB's 3 episodes, got %d", len(got))
	}
	titles := map[string]int{}
	for _, ep := range got {
		titles[ep.Title.Value]++
	}
	if titles["And That's Our Time"] != 1 {
		t.Errorf("the finale should appear once, got %d", titles["And That's Our Time"])
	}
	if last := got[len(got)-1]; last.EpisodeNumber != 3 {
		t.Errorf("the season should end at episode 3, got %d", last.EpisodeNumber)
	}
}

// TVMaze is last, not gone: a season TMDB returned nothing for is still
// TVMaze's to describe.
func TestMergeSeasons_TVMazeStillOwnsASeasonTMDBDoesNotHave(t *testing.T) {
	tvmazeSeasons := groupTVMazeEpisodesBySeason([]tvmaze.Episode{
		{Season: 4, Number: 1, Name: "Only TVMaze Has This", Airdate: "2026-06-01"},
		{Season: 4, Number: 2, Name: "And This", Airdate: "2026-06-08"},
	})

	seasons, _ := MergeSeasons([][]seasonSource{tvmazeSeasons}, Options{})

	if len(seasons) != 1 || len(seasons[0].Episodes) != 2 {
		t.Fatalf("want TVMaze's 2 episodes kept, got %+v", seasons)
	}
	if got := seasons[0].Episodes[0].Title; got.Provider != "tvmaze" || got.Value != "Only TVMaze Has This" {
		t.Errorf("want the episode sourced from tvmaze, got %+v", got)
	}
}

// A placeholder title from TMDB doesn't hide a real one from TVMaze -
// MobLand season 2, 2026-09-17.
func TestMergeSeasons_RealTitleBeatsPlaceholder(t *testing.T) {
	tmdbSeason := adaptTMDBSeason(2, &tmdb.Season{
		SeasonNumber: 2,
		Episodes: []tmdb.Episode{
			{EpisodeNumber: 1, Name: "Episode 1", AirDate: "2026-09-18"},
			{EpisodeNumber: 5, Name: "Episode 5", AirDate: "2026-10-16"},
			{EpisodeNumber: 6, Name: "The Real Name", AirDate: "2026-10-23"},
		},
	})
	tvmazeSeasons := groupTVMazeEpisodesBySeason([]tvmaze.Episode{
		{Season: 2, Number: 1, Name: "I Wanna Be Your Dog", Airdate: "2026-09-18"},
		{Season: 2, Number: 5, Name: "Episode 5", Airdate: "2026-10-16"},
		{Season: 2, Number: 6, Name: "TVMaze's Other Name", Airdate: "2026-10-23"},
	})
	seasons, _ := MergeSeasons([][]seasonSource{{tmdbSeason}, tvmazeSeasons}, Options{})
	eps := seasons[0].Episodes
	if eps[0].Title.Value != "I Wanna Be Your Dog" || eps[0].Title.Provider != "tvmaze" {
		t.Errorf("want TVMaze's real title over TMDB's placeholder, got %+v", eps[0].Title)
	}
	if eps[1].Title.Value != "Episode 5" || eps[1].Title.Provider != "tmdb" {
		t.Errorf("want the placeholder kept, in priority order, when nobody has a real title, got %+v", eps[1].Title)
	}
	if eps[2].Title.Value != "The Real Name" || eps[2].Title.Provider != "tmdb" {
		t.Errorf("want TMDB still to lead when both titles are real, got %+v", eps[2].Title)
	}
}

func TestPlaceholderEpisodeTitle(t *testing.T) {
	for title, want := range map[string]bool{"Episode 1": true, "episode 12": true, "Ep. 3": true, "Chapter 4": true, "TBA": true,
		"Episode One": false, "Song 2": false, "Pilot": false, "Part of the Plan": false, "": false} {
		if got := placeholderEpisodeTitle(title); got != want {
			t.Errorf("placeholderEpisodeTitle(%q) = %v, want %v", title, got, want)
		}
	}
}

// TheTVDB fills in what TMDB and TVMaze leave as placeholders - MobLand
// season 2 episodes 5 and up, 2026-09-17.
func TestMergeSeasons_TVDBFillsPlaceholders(t *testing.T) {
	tmdbSeason := adaptTMDBSeason(2, &tmdb.Season{SeasonNumber: 2, Episodes: []tmdb.Episode{
		{EpisodeNumber: 5, Name: "Episode 5", AirDate: "2026-10-16"},
		{EpisodeNumber: 6, Name: "Episode 6", AirDate: "2026-10-23"},
	}})
	tvmazeSeasons := groupTVMazeEpisodesBySeason([]tvmaze.Episode{
		{Season: 2, Number: 5, Name: "Episode 5", Airdate: "2026-10-16"},
		{Season: 2, Number: 6, Name: "Episode 6", Airdate: "2026-10-23"},
	})
	tvdbSeasons := groupTVDBEpisodesBySeason([]tvdb.Episode{
		{SeasonNumber: 2, Number: 5, Name: "The Real Name", Aired: "2026-10-16"},
		{SeasonNumber: 2, Number: 7, Name: "Not in TMDB's list", Aired: "2026-10-30"},
	})

	seasons, _ := MergeSeasons([][]seasonSource{{tmdbSeason}, tvmazeSeasons, tvdbSeasons}, Options{})
	eps := seasons[0].Episodes
	if len(eps) != 2 {
		t.Fatalf("want TMDB to still own the episode list (2), got %d", len(eps))
	}
	if eps[0].Title.Value != "The Real Name" || eps[0].Title.Provider != "tvdb" {
		t.Errorf("want TheTVDB's real title over the placeholders, got %+v", eps[0].Title)
	}
	if eps[1].Title.Value != "Episode 6" {
		t.Errorf("want the placeholder kept when nobody has a real title, got %+v", eps[1].Title)
	}
}
