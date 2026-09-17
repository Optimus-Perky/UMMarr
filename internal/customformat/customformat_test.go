package customformat

import (
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
)

func release(title string, size int64, flags newznab.Flags) Release {
	return ReleaseFrom(newznab.Release{Title: title, Size: size, Flags: flags})
}

func TestMatches(t *testing.T) {
	remux := Format{Name: "Remux", Conditions: []Condition{{Implementation: QualityModifier, Value: "REMUX", Required: true}}}
	hdr := Format{Name: "HDR", Conditions: []Condition{
		{Implementation: ReleaseTitle, Value: `\bHDR(10)?\b`},
		{Implementation: ReleaseTitle, Value: `\bDV\b`},
		{Implementation: Resolution, Value: "2160p", Required: true},
	}}
	noX265 := Format{Name: "Not x265 1080p", Conditions: []Condition{
		{Implementation: ReleaseTitle, Value: `x265|HEVC`, Negate: true, Required: true},
		{Implementation: Resolution, Value: "1080p", Required: true},
	}}
	small := Format{Name: "Small", Conditions: []Condition{{Implementation: Size, Min: 1, Max: 5}}}
	free := Format{Name: "Freeleech", Conditions: []Condition{{Implementation: IndexerFlag, Value: "Freeleech"}}}
	pack := Format{Name: "Season pack", Conditions: []Condition{{Implementation: ReleaseType, Value: "SeasonPack"}}}
	french := Format{Name: "French", Conditions: []Condition{{Implementation: Language, Value: "French"}}}
	bluray := Format{Name: "Bluray", Conditions: []Condition{{Implementation: Source, Value: "Bluray"}}}
	group := Format{Name: "Tier 1", Conditions: []Condition{{Implementation: ReleaseGroup, Value: `^(FraMeSToR|BHDStudio)$`}}}

	cases := []struct {
		format Format
		r      Release
		want   bool
	}{
		{remux, release("Inception.2010.1080p.BluRay.REMUX.AVC-FraMeSToR", 30<<30, 0), true},
		{remux, release("Inception.2010.1080p.BluRay.x264-GRP", 10<<30, 0), false},
		{bluray, release("Inception.2010.1080p.BluRay.REMUX.AVC-FraMeSToR", 30<<30, 0), true},
		{hdr, release("Dune.2021.2160p.WEB-DL.DV.HDR10.x265-GRP", 20<<30, 0), true},
		{hdr, release("Dune.2021.1080p.WEB-DL.HDR10.x265-GRP", 8<<30, 0), false},
		{hdr, release("Dune.2021.2160p.WEB-DL.x265-GRP", 20<<30, 0), false},
		{noX265, release("Dune.2021.1080p.WEB-DL.x264-GRP", 8<<30, 0), true},
		{noX265, release("Dune.2021.1080p.WEB-DL.x265-GRP", 8<<30, 0), false},
		{small, release("Show.S01E01.720p.HDTV", 2<<30, 0), true},
		{small, release("Show.S01E01.720p.HDTV", 6<<30, 0), false},
		{small, release("Show.S01E01.720p.HDTV", 0, 0), false},
		{free, release("Show.S01E01.720p.HDTV", 1<<30, newznab.FlagFreeleech|newznab.FlagInternal), true},
		{free, release("Show.S01E01.720p.HDTV", 1<<30, newznab.FlagInternal), false},
		{pack, release("Show.S01.1080p.WEB-DL", 30<<30, 0), true},
		{pack, release("Show.S01E02.1080p.WEB-DL", 3<<30, 0), false},
		{french, release("Amelie.2001.FRENCH.1080p.BluRay.x264-GRP", 9<<30, 0), true},
		{group, release("Inception.2010.1080p.BluRay.REMUX.AVC-FraMeSToR", 30<<30, 0), true},
		{group, release("Inception.2010.1080p.BluRay.x264-FraMeSToRS", 10<<30, 0), false},
		{Format{Name: "Empty"}, release("Anything", 1, 0), false},
	}
	for _, c := range cases {
		if got := c.format.Matches(c.r); got != c.want {
			t.Errorf("%s vs %q: got %v, want %v", c.format.Name, c.r.Title, got, c.want)
		}
	}
}

// A TRaSH Guides export, with Radarr's numbers.
const trashRadarr = `[{
  "name": "Remux Tier 01",
  "includeCustomFormatWhenRenaming": false,
  "specifications": [
    {"name": "BluRay", "implementation": "SourceSpecification", "negate": false, "required": true, "fields": {"value": 9}},
    {"name": "Remux", "implementation": "QualityModifierSpecification", "negate": false, "required": true, "fields": {"value": 5}},
    {"name": "FraMeSToR", "implementation": "ReleaseGroupSpecification", "negate": false, "required": false, "fields": {"value": "^(FraMeSToR)$"}}
  ]
}, {
  "name": "Uses lookahead",
  "specifications": [{"name": "x", "implementation": "ReleaseTitleSpecification", "fields": {"value": "^(?!.*x265)"}}]
}, {
  "name": "Workprint",
  "specifications": [{"name": "WP", "implementation": "SourceSpecification", "fields": {"value": 4}}]
}]`

func TestParseJSON(t *testing.T) {
	res, err := ParseJSON([]byte(trashRadarr), FromRadarr)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Formats) != 1 || res.Formats[0].Name != "Remux Tier 01" {
		t.Fatalf("want one importable format, got %+v", res.Formats)
	}
	f := res.Formats[0]
	if f.Conditions[0].Value != "Bluray" || f.Conditions[1].Value != "REMUX" || !f.Conditions[0].Required {
		t.Errorf("want Radarr's 9 and 5 read as Bluray and REMUX, got %+v", f.Conditions)
	}
	if !f.Matches(release("Inception.2010.1080p.BluRay.REMUX.AVC-FraMeSToR", 30<<30, 0)) {
		t.Errorf("want the imported format to match a FraMeSToR remux")
	}
	if len(res.Skipped) != 2 || !strings.Contains(strings.Join(res.Skipped, "|"), "Uses lookahead") || !strings.Contains(strings.Join(res.Skipped, "|"), "can't detect") {
		t.Errorf("want the lookahead pattern and the workprint source skipped with reasons, got %v", res.Skipped)
	}

	// Sonarr numbers sources differently: 6 is Bluray there, TV in Radarr.
	sonarr := `{"name": "Bluray", "specifications": [{"name": "b", "implementation": "SourceSpecification", "fields": [{"name": "value", "value": 6}]}]}`
	if res, _ := ParseJSON([]byte(sonarr), FromSonarr); len(res.Formats) != 1 || res.Formats[0].Conditions[0].Value != "Bluray" {
		t.Errorf("want Sonarr's 6 read as Bluray, got %+v", res)
	}
	if res, _ := ParseJSON([]byte(sonarr), FromRadarr); len(res.Formats) != 1 || res.Formats[0].Conditions[0].Value != "HDTV" {
		t.Errorf("want Radarr's 6 read as TV, got %+v", res)
	}

	// UMMarr's export imports back unchanged, whichever app is picked.
	tv, _ := ParseJSON([]byte(sonarr), FromRadarr)
	out, _ := ExportJSON(tv.Formats)
	again, err := ParseJSON(out, FromSonarr)
	if err != nil || len(again.Formats) != 1 || again.Formats[0].Conditions[0].Value != "HDTV" {
		t.Errorf("want an export to round-trip, got %+v %v", again, err)
	}
}
