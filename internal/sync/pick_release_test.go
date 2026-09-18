package sync

import (
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/musicbrainz"
)

// The real Avril Lavigne release group, as MusicBrainz lists it. Taking the
// first Official release gave the Taiwanese 16-track pressing and left
// three tracks missing for ever; the rules Mark asked for are: what the
// files say, then the track count the folder implies, then his countries
// in order.
func avrilReleases() []musicbrainz.ReleaseRef {
	release := func(id, country, date, status string, tracks int) musicbrainz.ReleaseRef {
		ref := musicbrainz.ReleaseRef{ID: id, Title: "Avril Lavigne", Country: country, Date: date, Status: status}
		ref.Media = []struct {
			Format     string `json:"format"`
			TrackCount int    `json:"track-count"`
		}{{Format: "CD", TrackCount: tracks}}
		return ref
	}
	return []musicbrainz.ReleaseRef{
		release("au", "AU", "2013-11-01", "Withdrawn", 13),
		release("ca", "CA", "2013-11-05", "Withdrawn", 13),
		release("tw", "TW", "2013-11-05", "Official", 16),
		release("us", "US", "2013-11-05", "Official", 13),
		release("us14", "US", "2013-11-05", "Official", 14),
		release("jp", "JP", "2013-11-06", "Official", 16),
		release("xe", "XE", "2013-11", "Official", 13),
		release("br", "BR", "2013", "Official", 13),
		release("gb", "GB", "2013-11-04", "Official", 13),
	}
}

func TestPickRelease(t *testing.T) {
	countries := []string{"GB", "US"}
	cases := []struct {
		name string
		hint ReleaseHint
		want string
	}{
		{"nothing known picks the preferred country", ReleaseHint{}, "gb"},
		{"the release the files name always wins", ReleaseHint{ReleaseMBID: "br", HighestTrack: 13}, "br"},
		{"a tagged release wins even against the country", ReleaseHint{ReleaseMBID: "jp"}, "jp"},
		// Files numbered to 13 with 4, 7 and 8 missing: still a 13-track
		// release, not a 10-track one.
		{"gaps in the rip don't change the track count", ReleaseHint{HighestTrack: 13, Files: 10}, "gb"},
		{"a 16-track folder takes a 16-track release", ReleaseHint{HighestTrack: 16, Files: 16}, "tw"},
		{"a 14-track folder takes the 14-track US release", ReleaseHint{HighestTrack: 14, Files: 14}, "us14"},
		// With no tags at all the file count is the only clue.
		{"untagged files fall back to counting them", ReleaseHint{Files: 16}, "tw"},
	}
	for _, c := range cases {
		got := PickRelease(avrilReleases(), c.hint, countries)
		if got == nil {
			t.Errorf("%s: nothing picked", c.name)
			continue
		}
		if got.ID != c.want {
			t.Errorf("%s: picked %s, want %s", c.name, got.ID, c.want)
		}
	}
}

func TestPickRelease_PrefersCountryOrderThenOfficial(t *testing.T) {
	// No GB pressing: US is next. Withdrawn loses to Official within the
	// same country.
	refs := avrilReleases()[:8] // drops the GB release
	got := PickRelease(refs, ReleaseHint{HighestTrack: 13}, []string{"GB", "US"})
	if got == nil || got.ID != "us" {
		t.Fatalf("want the 13-track US release, got %+v", got)
	}
	// No preference configured at all: still a 13-track Official one.
	got = PickRelease(refs, ReleaseHint{HighestTrack: 13}, nil)
	if got == nil || (got.Status != "Official" || got.TrackCount() != 13) {
		t.Fatalf("want an official 13-track release, got %+v", got)
	}
	if got := PickRelease(nil, ReleaseHint{}, nil); got != nil {
		t.Errorf("want nothing picked from no releases, got %+v", got)
	}
}
