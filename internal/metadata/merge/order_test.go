package merge

import (
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/omdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tvmaze"
)

func TestOptionsFromOrder_Movie(t *testing.T) {
	tmdbMovie := &tmdb.Movie{ID: 1, Title: "TMDB Title", Overview: "tmdb overview", PosterPath: "/p.jpg"}
	omdbResp := &omdb.Response{Title: "OMDb Title", Plot: "omdb plot", Response: "True"}

	merged, _, _ := MergeMovieFromProviders(tmdbMovie, omdbResp, OptionsFromOrder(ProviderOrder{}))
	if merged.Title.Provider != "tmdb" {
		t.Fatalf("want the built-in order with no setting, got title from %s", merged.Title.Provider)
	}
	merged, _, _ = MergeMovieFromProviders(tmdbMovie, omdbResp, OptionsFromOrder(ProviderOrder{Movie: []string{"omdb", "tmdb"}}))
	if merged.Title.Value != "OMDb Title" || merged.Title.Provider != "omdb" {
		t.Errorf("want OMDb first to win the title, got %+v", merged.Title)
	}
	if len(merged.Images.Value) == 0 || merged.Images.Provider != "tmdb" {
		t.Errorf("want TMDB's images kept - only TMDB supplies them")
	}
}

func TestOptionsFromOrder_SeriesEpisodes(t *testing.T) {
	tmdbSeasons := []tmdb.Season{{SeasonNumber: 1, Episodes: []tmdb.Episode{{EpisodeNumber: 1, Name: "TMDB One"}, {EpisodeNumber: 2, Name: "TMDB Two"}}}}
	tvmazeEpisodes := []tvmaze.Episode{{Season: 1, Number: 1, Name: "Maze One"}, {Season: 1, Number: 2, Name: "Maze Two"}, {Season: 1, Number: 3, Name: "Maze Three"}}

	seasons, _ := MergeSeasonsFromProviders(tmdbSeasons, tvmazeEpisodes, nil, OptionsFromOrder(ProviderOrder{Series: []string{"tvmaze", "tmdb"}}))
	eps := seasons[0].Episodes
	if len(eps) != 3 {
		t.Fatalf("want TVmaze first to decide the episode list (3), got %d", len(eps))
	}
	if eps[0].Title.Value != "Maze One" || eps[0].Title.Provider != "tvmaze" {
		t.Errorf("want TVmaze first to win episode titles, got %+v", eps[0].Title)
	}

	seasons, _ = MergeSeasonsFromProviders(tmdbSeasons, tvmazeEpisodes, nil, OptionsFromOrder(ProviderOrder{Series: []string{"tmdb", "tvmaze"}}))
	if n := len(seasons[0].Episodes); n != 2 {
		t.Errorf("want TMDB first to decide the list (2), got %d", n)
	}
}
