package releaseparse_test

import (
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
)

// TestParse uses real release titles seen live from the user's actual
// Prowlarr/IPTorrents deployment this session, not invented examples.
func TestParse(t *testing.T) {
	cases := []struct {
		title            string
		wantSource       string
		wantResolution   string
		wantReleaseGroup string
	}{
		{
			title:      "2 Fast 2 Furious 2003 UHD BluRay 2160p DD 7 1 HDR x265-hallowed",
			wantSource: "Bluray", wantResolution: "2160p", wantReleaseGroup: "hallowed",
		},
		{
			title:      "Terminator The Sarah Connor Chronicles S01 1080p AMZN WEB-DL DDP5 1 SDR H 264-OnlyWeb",
			wantSource: "WEBDL", wantResolution: "1080p", wantReleaseGroup: "OnlyWeb",
		},
		{
			title:      "2 Fast 2 Furious 2003 PROPER BluRay 1080p DTS-X 7 1 AVC HYBRID REMUX-FraMeSToR",
			wantSource: "Remux", wantResolution: "1080p", wantReleaseGroup: "FraMeSToR",
		},
		{
			title:      "2 Fast 2 Furious 2003 1080p BluRay x265-YAWNTiC",
			wantSource: "Bluray", wantResolution: "1080p", wantReleaseGroup: "YAWNTiC",
		},
		{
			title:      "Terminator The Sarah Connor Chronicles S01E08 Vicks Chip XviD-AFG",
			wantSource: "", wantResolution: "", wantReleaseGroup: "AFG",
		},
		{
			title:      "No group or quality info at all",
			wantSource: "", wantResolution: "", wantReleaseGroup: "",
		},
		{
			// A downloaded FILE's own name (not a raw search-result title)
			// always carries an extension after the release group - this
			// caught a real bug where the group regex only matched titles
			// with no trailing ".ext".
			title:      "Inception 2010 BluRay 1080p x265-hallowed.mkv",
			wantSource: "Bluray", wantResolution: "1080p", wantReleaseGroup: "hallowed",
		},
	}

	for _, c := range cases {
		got := releaseparse.Parse(c.title)
		if got.Source != c.wantSource || got.Resolution != c.wantResolution || got.ReleaseGroup != c.wantReleaseGroup {
			t.Errorf("Parse(%q) = %+v, want {Source:%q Resolution:%q ReleaseGroup:%q}",
				c.title, got, c.wantSource, c.wantResolution, c.wantReleaseGroup)
		}
	}
}

func TestFileQuality_String(t *testing.T) {
	cases := []struct {
		quality releaseparse.FileQuality
		want    string
	}{
		{releaseparse.FileQuality{Source: "Bluray", Resolution: "1080p"}, "Bluray-1080p"},
		{releaseparse.FileQuality{Source: "HDTV"}, "HDTV"},
		{releaseparse.FileQuality{Resolution: "720p"}, "720p"},
		{releaseparse.FileQuality{}, ""},
	}
	for _, c := range cases {
		if got := c.quality.String(); got != c.want {
			t.Errorf("%+v.String() = %q, want %q", c.quality, got, c.want)
		}
	}
}

func TestFileQuality_Key(t *testing.T) {
	cases := []struct {
		quality releaseparse.FileQuality
		want    string
	}{
		{releaseparse.FileQuality{Source: "Bluray", Resolution: "1080p"}, "Bluray-1080p"},
		{releaseparse.FileQuality{Source: "Remux", Resolution: "2160p"}, "Remux-2160p"},
		{releaseparse.FileQuality{Source: "DVD"}, "DVD"},
		{releaseparse.FileQuality{Source: "DVD", Resolution: "720p"}, "DVD"}, // DVD has no resolution tiers in the catalog
		{releaseparse.FileQuality{Resolution: "1080p"}, "Unknown"},           // resolution with no source isn't a reliable catalog match
		{releaseparse.FileQuality{}, "Unknown"},
	}
	for _, c := range cases {
		if got := c.quality.Key(); got != c.want {
			t.Errorf("%+v.Key() = %q, want %q", c.quality, got, c.want)
		}
	}
}

func TestScore(t *testing.T) {
	items := []releaseparse.QualityProfileItem{
		{Quality: "Bluray-1080p", Weight: 100, Allowed: true},
		{Quality: "HDTV-720p", Weight: 10, Allowed: false},
	}

	weight, allowed := releaseparse.Score(items, releaseparse.FileQuality{Source: "Bluray", Resolution: "1080p"})
	if weight != 100 || !allowed {
		t.Errorf("want weight=100 allowed=true for an allowed quality, got weight=%d allowed=%v", weight, allowed)
	}

	weight, allowed = releaseparse.Score(items, releaseparse.FileQuality{Source: "HDTV", Resolution: "720p"})
	if weight != 10 || allowed {
		t.Errorf("want weight=10 allowed=false for a disallowed quality, got weight=%d allowed=%v", weight, allowed)
	}

	weight, allowed = releaseparse.Score(items, releaseparse.FileQuality{Source: "Remux", Resolution: "2160p"})
	if weight != 0 || allowed {
		t.Errorf("want weight=0 allowed=false for a quality missing from items, got weight=%d allowed=%v", weight, allowed)
	}
}

// TestParse_Codec covers the {Video Codec} naming token. The first five titles
// are the real ones TestParse uses, from the user's own indexers.
func TestParse_Codec(t *testing.T) {
	cases := []struct{ title, want string }{
		{"2 Fast 2 Furious 2003 UHD BluRay 2160p DD 7 1 HDR x265-hallowed", "x265"},
		{"Terminator The Sarah Connor Chronicles S01 1080p AMZN WEB-DL DDP5 1 SDR H 264-OnlyWeb", "AVC"},
		{"2 Fast 2 Furious 2003 PROPER BluRay 1080p DTS-X 7 1 AVC HYBRID REMUX-FraMeSToR", "AVC"},
		{"Terminator The Sarah Connor Chronicles S01E08 Vicks Chip XviD-AFG", "XviD"},
		{"Terminator The Sarah Connor Chronicles S01E01 Pilot 1080p HEVC x265-MeGusta", "x265"},
		{"Some.Movie.2019.1080p.WEB-DL.H.265-GRP.mkv", "HEVC"},
		{"Some.Movie.2019.1080p.BluRay.x264-GRP.mkv", "x264"},
		{"Some Movie 2021 2160p WEB AV1-GRP", "AV1"},
		{"Some Movie 1999 BluRay 1080p VC-1 REMUX-GRP", "VC-1"},
		{"No group or quality info at all", ""},
	}
	for _, c := range cases {
		if got := releaseparse.Parse(c.title).Codec; got != c.want {
			t.Errorf("Parse(%q).Codec = %q, want %q", c.title, got, c.want)
		}
	}
}

// TestParse_SonarrFallbacks: releases seen live on the user's indexers with a
// resolution but no source came out Unknown, which quality profiles refuse.
// Sonarr and Radarr call those HDTV (SDTV below 720p), and recognise more
// source tokens than UMMarr did.
func TestParse_SonarrFallbacks(t *testing.T) {
	cases := []struct{ title, want string }{
		{"Terminator The Sarah Connor Chronicles S01E01 Pilot 1080p HEVC x265-MeGusta", "HDTV-1080p"},
		{"Terminator The Sarah Connor Chronicles S01E01 Pilot 720p HEVC x265-MeGusta", "HDTV-720p"},
		{"Terminator The Sarah Connor Chronicles S01E08 Vicks Chip XviD-AFG", "Unknown"},
		{"Show.S01E01.480p.x264-GRP", "SDTV"},
		{"Show.S01E01.x264-LOL", "SDTV"},
		{"Show.S01E01.PDTV.XviD-GRP", "SDTV"},
		{"Show.S01E01.1080p.TVRip.x264", "HDTV-1080p"},
		{"Show.S01E01.1080i.HDTV.MPEG2", "HDTV-1080p"},
		{"Movie 2019 1080p WEB H264-GRP", "WEBDL-1080p"},
		{"Movie.2019.2160p.WEB.H265-GRP", "WEBDL-2160p"},
		{"Movie 2019 1080p AMZN WEB-DL DDP5 1 H 264-GRP", "WEBDL-1080p"},
		{"Movie 2019 720p AMZN WEBRip x264-GRP", "WEBRip-720p"},
		{"Movie.2019.1080p.BDRip.x264", "Bluray-1080p"},
		{"Movie 2019 UHD BluRay x265-GRP", "Bluray-2160p"},
		{"Movie 2019 1920x1080 BluRay x264", "Bluray-1080p"},
		{"Movie 2019 DVDRip XviD-GRP", "DVD"},
		{"Show S01E01 HD TV", "HDTV-720p"},
	}
	for _, c := range cases {
		if got := releaseparse.Parse(c.title).Key(); got != c.want {
			t.Errorf("Parse(%q).Key() = %q, want %q (parsed %+v)", c.title, got, c.want, releaseparse.Parse(c.title))
		}
	}
}
