package importer_test

import (
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
)

func TestParseEpisode(t *testing.T) {
	cases := []struct {
		path       string
		season, ep int
		ok         bool
	}{
		{"Show.Name.S01E02.720p.mkv", 1, 2, true},
		{"Season 02/Show.Name.s02e05.mkv", 2, 5, true},
		{"Show.Name.S01E102.mkv", 1, 102, true},
		{"Show Name - S03E10 - Episode Title.mkv", 3, 10, true},
		{"Show Name - 1x02 - Pilot.mkv", 1, 2, true},
		{"Show.Name.12x103.mkv", 12, 103, true},
		{"Season 02/Show.Name.E05.mkv", 2, 5, true},
		{"Show.Name.1920x1080.HEVC.mkv", 0, 0, false},
		{"Show.Name.E05.mkv", 0, 0, false},
		{"Anime.Show.013.mkv", 0, 0, false},
	}

	for _, c := range cases {
		season, ep, ok := importer.ParseEpisode(c.path)
		if season != c.season || ep != c.ep || ok != c.ok {
			t.Errorf("ParseEpisode(%q) = (%d, %d, %v), want (%d, %d, %v)",
				c.path, season, ep, ok, c.season, c.ep, c.ok)
		}
	}
}

func TestParseEpisodesMulti(t *testing.T) {
	cases := []struct {
		path     string
		season   int
		episodes []int
		ok       bool
	}{
		{"Show - S01E23E24 - Title.mkv", 1, []int{23, 24}, true},
		{"Show - S01E23-E24 - Title.mkv", 1, []int{23, 24}, true},
		{"Show - S01E23-24 - Title.mkv", 1, []int{23, 24}, true},
		{"Show - S01E23E24E25 - Title.mkv", 1, []int{23, 24, 25}, true},
		// A single episode still returns a one-element slice.
		{"Show.Name.S01E02.720p.mkv", 1, []int{2}, true},
		// No separator between episode numbers is genuinely ambiguous and
		// deliberately NOT treated as multi-episode - it falls back to a
		// single (wrong) greedily-parsed episode number, same as today.
		{"2 Broke Girls - S01E2324 - And Martha Stewart Have a Ball.mkv", 1, []int{232}, true},
	}

	for _, c := range cases {
		season, episodes, ok := importer.ParseEpisodes(c.path)
		if season != c.season || ok != c.ok || !intSlicesEqual(episodes, c.episodes) {
			t.Errorf("ParseEpisodes(%q) = (%d, %v, %v), want (%d, %v, %v)",
				c.path, season, episodes, ok, c.season, c.episodes, c.ok)
		}
	}
}

func intSlicesEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
