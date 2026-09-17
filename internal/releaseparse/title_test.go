package releaseparse

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseMovie(t *testing.T) {
	for _, tc := range []struct {
		release string
		want    MovieInfo
		ok      bool
	}{
		{"The.Matrix.1999.1080p.BluRay.x264-GROUP", MovieInfo{"The Matrix", 1999}, true},
		{"The Fast and the Furious 2001 2160p MA WEB-DL DoVi HDR H 265", MovieInfo{"The Fast and the Furious", 2001}, true},
		{"Blade.Runner.2049.2017.2160p.UHD.BluRay", MovieInfo{"Blade Runner 2049", 2017}, true},
		{"1917 (2019) 1080p", MovieInfo{"1917", 2019}, true},
		{"1917.1080p.WEB-DL", MovieInfo{"1917", 0}, true},
		{"[Group] Spirited Away (2001) [1080p]", MovieInfo{"Spirited Away", 2001}, true},
		{"Inception_2010_720p", MovieInfo{"Inception", 2010}, true},
		{"just some words", MovieInfo{}, false},
	} {
		got, ok := ParseMovie(tc.release)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%q: want %+v %v, got %+v %v", tc.release, tc.want, tc.ok, got, ok)
		}
	}
}

func TestParseEpisode(t *testing.T) {
	for _, tc := range []struct {
		release string
		want    EpisodeInfo
	}{
		{"Breaking.Bad.S01E03.720p.HDTV", EpisodeInfo{SeriesTitle: "Breaking Bad", Season: 1, Episodes: []int{3}}},
		{"Breaking Bad S01E03E04 1080p", EpisodeInfo{SeriesTitle: "Breaking Bad", Season: 1, Episodes: []int{3, 4}}},
		{"Breaking.Bad.S01E03-E05.1080p", EpisodeInfo{SeriesTitle: "Breaking Bad", Season: 1, Episodes: []int{3, 4, 5}}},
		{"Breaking.Bad.S01E03-05.1080p", EpisodeInfo{SeriesTitle: "Breaking Bad", Season: 1, Episodes: []int{3, 4, 5}}},
		{"Better Call Saul S01E05 Alpine Shepherd 1080p", EpisodeInfo{SeriesTitle: "Better Call Saul", Season: 1, Episodes: []int{5}}},
		{"Doctor.Who.2005.S10E01.720p", EpisodeInfo{SeriesTitle: "Doctor Who", Year: 2005, Season: 10, Episodes: []int{1}}},
		{"The Office (US) 3x07 HDTV", EpisodeInfo{SeriesTitle: "The Office (US)", Season: 3, Episodes: []int{7}}},
		{"Breaking.Bad.S02.1080p.BluRay.x264", EpisodeInfo{SeriesTitle: "Breaking Bad", Season: 2, FullSeason: true}},
		{"Breaking Bad Season 3 Complete 720p", EpisodeInfo{SeriesTitle: "Breaking Bad", Season: 3, FullSeason: true}},
		{"Breaking.Bad.S01-S05.Complete.1080p", EpisodeInfo{SeriesTitle: "Breaking Bad", Season: 1, FullSeason: true, MultiSeason: true}},
		{"The.Daily.Show.2024.05.01.720p.WEB", EpisodeInfo{SeriesTitle: "The Daily Show", AirDate: time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)}},
	} {
		got, ok := ParseEpisode(tc.release)
		if !ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: want %+v, got %+v (ok %v)", tc.release, tc.want, got, ok)
		}
	}
	if _, ok := ParseEpisode("The Matrix 1999 1080p"); ok {
		t.Errorf("want a movie title not read as an episode")
	}
}

func TestParseRevision(t *testing.T) {
	for _, tc := range []struct {
		release string
		want    Revision
	}{
		{"Movie 2010 1080p BluRay", Revision{Version: 1}},
		{"Movie 2010 PROPER 1080p", Revision{Version: 2}},
		{"Movie.2010.REPACK.1080p", Revision{Version: 2, IsRepack: true}},
		{"Movie.2010.REPACK2.1080p", Revision{Version: 3, IsRepack: true}},
		{"Show 01v2 1080p", Revision{Version: 2}},
		{"Movie 2010 REAL PROPER 1080p", Revision{Version: 2, Real: 1}},
		{"Real Steel 2011 1080p", Revision{Version: 1}},
	} {
		if got := ParseRevision(tc.release); got != tc.want {
			t.Errorf("%q: want %+v, got %+v", tc.release, tc.want, got)
		}
	}
	if (Revision{Version: 2}).Compare(Revision{Version: 1}) != 1 || (Revision{Version: 2}).Compare(Revision{Version: 2, Real: 1}) != -1 {
		t.Errorf("want newer versions, then more REALs, to compare higher")
	}
}

func TestHardcodedSubs(t *testing.T) {
	for release, want := range map[string]string{
		"Movie 2019 KORSUB 1080p WEBRip":         "KORSUB",
		"Movie.2019.HC.HDRip":                    "Generic Hardcoded Subs",
		"Movie 2019 1080p SUBBED":                "Generic Hardcoded Subs",
		"Movie 2019 1080p SOFTSUBS":              "",
		"Movie 2019 1080p MULTISUBS":             "",
		"Movie 2019 1080p BluRay x264":           "",
		"Movie 2019 NLSUBS 720p then HC version": "Generic Hardcoded Subs",
	} {
		if got := HardcodedSubs(release); got != want {
			t.Errorf("%q: want %q, got %q", release, want, got)
		}
	}
}

func TestLanguages(t *testing.T) {
	for title, want := range map[string]string{
		"1923 S02E07 A Dream and a Memory 2160p AMZN WEB-DL DDP5 1 H 265-NTb": "English",
		"Movie 2019 GERMAN DL 1080p BluRay x264-GRP":                          "German, English",
		"Movie 2019 MULTi VFF 1080p WEB":                                      "French, English",
		"Show S01E01 1080p WEB x264-ITA":                                      "English",
		"Show S01E01 ITA ENG 1080p WEB":                                       "Italian, English",
		"Film 2020 NORDIC 1080p BluRay":                                       "Nordic",
	} {
		if got := strings.Join(Languages(title), ", "); got != want {
			t.Errorf("%q: want %q, got %q", title, want, got)
		}
	}
}
