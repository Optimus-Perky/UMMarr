package decision

import (
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// A Bluray-1080p movie of 148 minutes with a 4-30 MB/min definition must be
// between 592MB and 4.3GB: a 300MB "1080p" is a fake or a sample, a 20GB
// one is over the limit.
func TestMovie_QualityDefinitionSizeLimits(t *testing.T) {
	e := engine()
	e.QualityDefinitions = map[string]store.QualityDefinition{
		"Bluray-1080p": {Quality: "Bluray-1080p", MinSize: 4, MaxSize: 30},
	}
	m := inception()
	m.Runtime = 148

	tiny := torrent("Inception 2010 1080p BluRay tiny")
	tiny.Size = 300 << 20
	huge := torrent("Inception 2010 1080p BluRay huge")
	huge.Size = 20 << 30
	right := torrent("Inception 2010 1080p BluRay right")
	right.Size = 3 << 30

	got := map[string]Decision{}
	for _, d := range e.Movie(m, []newznab.Release{tiny, huge, right}) {
		got[d.Release.GUID] = d
	}
	wantRejected(t, got[tiny.GUID], "300.0 MiB is smaller than 592.0 MiB, the minimum for Bluray-1080p over 148 minutes")
	wantRejected(t, got[huge.GUID], "20.0 GiB is bigger than 4.3 GiB, the maximum for Bluray-1080p over 148 minutes")
	wantApproved(t, got[right.GUID])

	// Without a runtime there's nothing to measure against.
	m.Runtime = 0
	wantApproved(t, e.Movie(m, []newznab.Release{tiny})[0])
}

// A season pack is measured against every episode it covers.
func TestSeries_SizeLimitsCoverTheWholePack(t *testing.T) {
	e := engine()
	e.QualityDefinitions = map[string]store.QualityDefinition{
		"Bluray-1080p": {Quality: "Bluray-1080p", MinSize: 4},
	}
	s := breakingBad()
	for i := range s.Episodes {
		s.Episodes[i].Runtime = 47
	}
	// Season 1 has three episodes in the fixture: 141 minutes, so at least
	// 564MB. 400MB would pass for one episode but not for the pack.
	pack := torrent("Breaking.Bad.S01.1080p.BluRay")
	pack.Size = 400 << 20
	single := torrent("Breaking.Bad.S01E01.1080p.BluRay")
	single.Size = 400 << 20
	got := map[string]Decision{}
	for _, d := range e.Series(s, SeriesScope{}, []newznab.Release{pack, single}) {
		got[d.Release.GUID] = d
	}
	wantRejected(t, got[pack.GUID], "the minimum for Bluray-1080p over 141 minutes")
	wantApproved(t, got[single.GUID])
}

// With a preferred size, the release nearest it ranks first instead of
// simply the biggest.
func TestSort_PreferredSizeWins(t *testing.T) {
	e := engine()
	e.QualityDefinitions = map[string]store.QualityDefinition{
		"Bluray-1080p": {Quality: "Bluray-1080p", PreferredSize: 20},
	}
	m := inception()
	m.Runtime = 100 // preferred: 2000MB
	big := torrent("Inception 2010 1080p BluRay big")
	big.Size = 12 << 30
	near := torrent("Inception 2010 1080p BluRay near")
	near.Size = 2 << 30
	decisions := e.Movie(m, []newznab.Release{big, near})
	if decisions[0].Release.GUID != near.GUID {
		t.Fatalf("want the release nearest 2000MB first, got %q", decisions[0].Release.Title)
	}
	// No preference: bigger wins, as before.
	e.QualityDefinitions = nil
	decisions = e.Movie(m, []newznab.Release{near, big})
	if decisions[0].Release.GUID != big.GUID {
		t.Fatalf("want the bigger release first without a preference, got %q", decisions[0].Release.Title)
	}
}
