package decision

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

var now = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

func engine() *Engine {
	return &Engine{
		Now:       now,
		Propers:   store.PropersPreferAndUpgrade,
		Indexers:  map[int64]store.Indexer{1: {ID: 1, Name: "Tracker", MinimumSeeders: 1}},
		Profiles:  Profiles{},
		Protocols: map[string]bool{newznab.ProtocolTorrent: true},
	}
}

func torrent(title string) newznab.Release {
	return newznab.Release{GUID: title, Title: title, Protocol: newznab.ProtocolTorrent, IndexerID: 1, Indexer: "Tracker",
		IndexerPriority: 25, Seeders: 20, Peers: 25, Size: 8 << 30, PublishDate: now.Add(-48 * time.Hour)}
}

func date(y int, m time.Month, d int) sql.NullTime {
	return sql.NullTime{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), Valid: true}
}

func inception() store.WantedMovie {
	return store.WantedMovie{ID: 7, Title: "Inception", Year: 2010, Monitored: true, MinimumAvailability: "released",
		PhysicalRelease: date(2010, 12, 7), TMDbID: 27205, IMDbID: "tt1375666"}
}

func reasons(d Decision) string { return strings.Join(d.Rejections, " | ") }

func wantRejected(t *testing.T, d Decision, fragment string) {
	t.Helper()
	if !strings.Contains(reasons(d), fragment) {
		t.Errorf("%q: want a rejection containing %q, got %q", d.Release.Title, fragment, reasons(d))
	}
}

func wantApproved(t *testing.T, d Decision) {
	t.Helper()
	if !d.Approved() {
		t.Errorf("%q: want approved, got %q", d.Release.Title, reasons(d))
	}
}

func TestMovie_Matching(t *testing.T) {
	e := engine()
	byTitle := torrent("Inception.2010.1080p.BluRay.x264-GROUP")
	nextYear := torrent("Inception 2011 1080p BluRay")
	wrongYear := torrent("Inception 2014 1080p BluRay")
	wrongTitle := torrent("Interstellar 2014 1080p BluRay")
	wrongID := torrent("Inception 2010 1080p BluRay")
	wrongID.GUID, wrongID.TMDbID = "wrong-id", 157336
	rightID := torrent("Some Scene Name 1080p BluRay")
	rightID.GUID, rightID.IMDbID = "right-id", 1375666

	got := map[string]Decision{}
	for _, d := range e.Movie(inception(), []newznab.Release{byTitle, nextYear, wrongYear, wrongTitle, wrongID, rightID}) {
		got[d.Release.GUID] = d
	}
	wantApproved(t, got[byTitle.GUID])
	wantApproved(t, got[nextYear.GUID])
	wantApproved(t, got[rightID.GUID])
	wantRejected(t, got[wrongYear.GUID], "Wrong year: release is from 2014")
	wantRejected(t, got[wrongTitle.GUID], "Wrong movie: release is for Interstellar (2014)")
	wantRejected(t, got[wrongID.GUID], "Wrong movie: the indexer says it's TMDb 157336")
}

func TestMovie_ReleaseRules(t *testing.T) {
	e := engine()
	e.Settings = store.IndexerSettings{MaximumSize: 4096, MinimumAge: 60, Retention: 30, WhitelistedHardcodedSubs: "nlsub"}
	e.Indexers[1] = store.Indexer{ID: 1, MinimumSeeders: 5, RequiredFlags: []int{int(newznab.FlagFreeleech), int(newznab.FlagInternal)}}

	usenetNew := torrent("Inception 2010 1080p BluRay usenet new")
	usenetNew.Protocol, usenetNew.PublishDate, usenetNew.Size = newznab.ProtocolUsenet, now.Add(-10*time.Minute), 1<<30
	usenetOld := torrent("Inception 2010 1080p BluRay usenet old")
	usenetOld.Protocol, usenetOld.PublishDate, usenetOld.Size = newznab.ProtocolUsenet, now.AddDate(0, 0, -40), 1<<30
	tooBig := torrent("Inception 2010 1080p BluRay big")
	fewSeeders := torrent("Inception 2010 1080p BluRay few")
	fewSeeders.Seeders, fewSeeders.Size = 2, 1<<30
	unknownSeeders := torrent("Inception 2010 1080p BluRay unknown seeders")
	unknownSeeders.Seeders, unknownSeeders.Size, unknownSeeders.Flags = -1, 1<<30, newznab.FlagInternal
	korsub := torrent("Inception 2010 KORSUB 1080p BluRay")
	korsub.Size, korsub.Flags = 1<<30, newznab.FlagFreeleech
	nlsub := torrent("Inception 2010 NLSUBS 1080p BluRay")
	nlsub.Size, nlsub.Flags = 1<<30, newznab.FlagFreeleech

	got := map[string]Decision{}
	for _, d := range e.Movie(inception(), []newznab.Release{usenetNew, usenetOld, tooBig, fewSeeders, unknownSeeders, korsub, nlsub}) {
		got[d.Release.GUID] = d
	}
	wantRejected(t, got[usenetNew.GUID], "No enabled usenet download client")
	wantRejected(t, got[usenetNew.GUID], "Only 10 minutes old, minimum age is 60 minutes")
	wantRejected(t, got[usenetOld.GUID], "Older than the 30 day retention")
	wantRejected(t, got[tooBig.GUID], "8.0 GiB is too big, maximum size is 4.0 GiB")
	wantRejected(t, got[fewSeeders.GUID], "Not enough seeders: 2. Minimum seeders: 5")
	wantRejected(t, got[fewSeeders.GUID], "required flags: Freeleech, Internal")
	wantApproved(t, got[unknownSeeders.GUID])
	wantRejected(t, got[korsub.GUID], "Hardcoded subs found: KORSUB")
	wantApproved(t, got[nlsub.GUID])

	e.Settings.AllowHardcodedSubs = true
	wantApproved(t, e.Movie(inception(), []newznab.Release{korsub})[0])
}

func TestMovie_QualityProfile(t *testing.T) {
	e := engine()
	items := store.DefaultQualityProfileItems()
	for i := range items {
		if items[i].Quality == "Unknown" || items[i].Quality == "HDTV-720p" {
			items[i].Allowed = false
		}
	}
	e.Profiles[3] = Profile{Items: items}
	m := inception()
	m.QualityProfileID = sql.NullInt64{Int64: 3, Valid: true}

	ds := e.Movie(m, []newznab.Release{torrent("Inception 2010 720p HDTV"), torrent("Inception 2010 CAM")})
	for _, d := range ds {
		if !strings.Contains(reasons(d), "isn't wanted in the quality profile") {
			t.Errorf("%q: want the quality refused by the profile, got %q", d.Release.Title, reasons(d))
		}
		if d.QualityAllowed {
			t.Errorf("%q: want QualityAllowed false alongside the rejection", d.Release.Title)
		}
	}
}

func TestMovie_ItemRules(t *testing.T) {
	release := torrent("Inception 2010 1080p BluRay")

	e := engine()
	m := inception()
	m.Monitored, m.HasFile, m.Queued = false, true, true
	d := e.Movie(m, []newznab.Release{release})[0]
	wantRejected(t, d, "Movie isn't monitored")
	wantRejected(t, d, "already has a file")
	wantRejected(t, d, "already in the download queue")

	e.UserInvoked = true
	if d = e.Movie(m, []newznab.Release{release})[0]; strings.Contains(reasons(d), "monitored") || !strings.Contains(reasons(d), "already has a file") {
		t.Errorf("want a search someone started to skip the monitored check only, got %q", reasons(d))
	}
}

func TestMovieAvailability(t *testing.T) {
	release := torrent("Inception 2010 1080p BluRay")
	for _, tc := range []struct {
		name  string
		movie func(*store.WantedMovie)
		delay int
		want  string // "" = available
	}{
		{"released with physical release", func(m *store.WantedMovie) {}, 0, ""},
		{"physical and digital: earliest counts", func(m *store.WantedMovie) {
			m.PhysicalRelease, m.DigitalRelease = date(2026, 10, 1), date(2026, 9, 1)
		}, 0, ""},
		{"delay pushes it out", func(m *store.WantedMovie) {
			m.PhysicalRelease = date(2026, 9, 10)
		}, 7, "won't be considered available until 17 Sep 2026"},
		{"in cinemas only: 90 days later", func(m *store.WantedMovie) {
			m.PhysicalRelease, m.InCinemas = sql.NullTime{}, date(2026, 8, 1)
		}, 0, "until 30 Oct 2026"},
		{"minimum availability in cinemas", func(m *store.WantedMovie) {
			m.MinimumAvailability, m.PhysicalRelease, m.InCinemas = "inCinemas", date(2027, 1, 1), date(2026, 8, 1)
		}, 0, ""},
		{"no dates at all", func(m *store.WantedMovie) {
			m.PhysicalRelease = sql.NullTime{}
		}, 0, "no release date yet"},
		{"announced is always available", func(m *store.WantedMovie) {
			m.MinimumAvailability, m.PhysicalRelease = "announced", sql.NullTime{}
		}, 0, ""},
	} {
		e := engine()
		e.Settings.AvailabilityDelay = tc.delay
		m := inception()
		tc.movie(&m)
		d := e.Movie(m, []newznab.Release{release})[0]
		if tc.want == "" && !d.Approved() || tc.want != "" && !strings.Contains(reasons(d), tc.want) {
			t.Errorf("%s: want %q, got %q", tc.name, tc.want, reasons(d))
		}
		e.UserInvoked = true
		if d = e.Movie(m, []newznab.Release{release})[0]; !d.Approved() {
			t.Errorf("%s: want availability skipped for a search someone started, got %q", tc.name, reasons(d))
		}
	}
}

func breakingBad() store.WantedSeries {
	s := store.WantedSeries{ID: 3, Title: "Breaking Bad", Year: 2008, Monitored: true}
	id := int64(100)
	for season := 1; season <= 2; season++ {
		for ep := 1; ep <= 3; ep++ {
			id++
			s.Episodes = append(s.Episodes, store.WantedEpisode{ID: id, SeasonNumber: season, EpisodeNumber: ep,
				AirDate: date(2008+season, time.Month(ep), 1), Monitored: true})
		}
	}
	return s
}

func TestSeries(t *testing.T) {
	e := engine()
	s := breakingBad()
	s.Episodes[1].HasFile = true               // S01E02
	s.Episodes[5].AirDate = date(2026, 12, 25) // S02E03 hasn't aired

	ds := map[string]Decision{}
	for _, d := range e.Series(s, SeriesScope{}, []newznab.Release{
		torrent("Breaking.Bad.S01E01.1080p.BluRay"),
		torrent("Breaking.Bad.S01E02.1080p.BluRay"),
		torrent("Breaking Bad S01E01E02 1080p BluRay"),
		torrent("Breaking.Bad.S02.1080p.BluRay"),
		torrent("Breaking.Bad.S02E03.1080p.WEB-DL"),
		torrent("Breaking.Bad.S01-S02.1080p.BluRay"),
		torrent("Better.Call.Saul.S01E01.1080p.BluRay"),
		torrent("Breaking.Bad.S05E01.1080p.BluRay"),
	}) {
		ds[d.Release.Title] = d
	}
	one := ds["Breaking.Bad.S01E01.1080p.BluRay"]
	wantApproved(t, one)
	if len(one.Target.Episodes) != 1 || one.Target.Episodes[0].ID != 101 || one.Target.SeriesID != 3 {
		t.Errorf("want the target to be S01E01, got %+v", one.Target)
	}
	wantRejected(t, ds["Breaking.Bad.S01E02.1080p.BluRay"], "S01E02 already has a file")
	wantRejected(t, ds["Breaking Bad S01E01E02 1080p BluRay"], "S01E02 already has a file")
	pack := ds["Breaking.Bad.S02.1080p.BluRay"]
	wantRejected(t, pack, "not every episode in season 2 has aired")
	if !pack.Target.FullSeason || pack.Target.Season != 2 || len(pack.Target.Episodes) != 3 {
		t.Errorf("want a season 2 pack target, got %+v", pack.Target)
	}
	wantRejected(t, ds["Breaking.Bad.S02E03.1080p.WEB-DL"], "S02E03 hasn't aired yet")
	wantRejected(t, ds["Breaking.Bad.S01-S02.1080p.BluRay"], "Multi-season packs aren't supported")
	wantRejected(t, ds["Better.Call.Saul.S01E01.1080p.BluRay"], "Wrong series: release is for Better Call Saul")
	wantRejected(t, ds["Breaking.Bad.S05E01.1080p.BluRay"], "Unknown episode S05E01")

	season, episode := 1, 3
	scoped := e.Series(s, SeriesScope{Season: &season, Episode: &episode}, []newznab.Release{
		torrent("Breaking.Bad.S01E03.720p.HDTV"), torrent("Breaking.Bad.S01E01.720p.HDTV"), torrent("Breaking.Bad.S01.720p.HDTV"),
	})
	byTitle := map[string]Decision{}
	for _, d := range scoped {
		byTitle[d.Release.Title] = d
	}
	wantApproved(t, byTitle["Breaking.Bad.S01E03.720p.HDTV"])
	wantRejected(t, byTitle["Breaking.Bad.S01E01.720p.HDTV"], "Wrong episode: release is S01E01")
	wantRejected(t, byTitle["Breaking.Bad.S01.720p.HDTV"], "Season pack, but only S01E03 was searched for")
}

func TestMatchSeriesTitle(t *testing.T) {
	doctor := store.WantedSeries{Title: "Doctor Who (2005)"}
	for release, want := range map[string]bool{
		"Doctor.Who.2005.S10E01.720p":      true,
		"Doctor.Who.S10E01.720p":           true,
		"Doctor.Who.1963.S01E01.720p":      false,
		"Doctor.Who.Confidential.S01E01.x": false,
	} {
		info, _ := releaseparse.ParseEpisode(release)
		if got := MatchSeriesTitle(doctor, info); got != want {
			t.Errorf("%q: want %v, got %v", release, want, got)
		}
	}
}

func TestAlbumAndTrack(t *testing.T) {
	e := engine()
	a := store.WantedAlbum{ID: 9, Artist: "Daft Punk", Title: "Homework", Monitored: true, ReleaseDate: date(1997, 1, 20),
		Tracks: []store.WantedTrack{{ID: 1, Title: "Daftendirekt"}, {ID: 2, Title: "Revolution 909", HasFile: true}}}

	ds := map[string]Decision{}
	for _, d := range e.Album(a, []newznab.Release{torrent("Daft Punk - Homework (1997) [FLAC]"), torrent("Daft Punk - Discovery (2001) [FLAC]")}) {
		ds[d.Release.Title] = d
	}
	wantRejected(t, ds["Daft Punk - Homework (1997) [FLAC]"], "Album already has files")
	wantRejected(t, ds["Daft Punk - Discovery (2001) [FLAC]"], "Wrong album")
	if hc := ds["Daft Punk - Homework (1997) [FLAC]"]; strings.Contains(reasons(hc), "Hardcoded") {
		t.Errorf("want no subtitle rules for music, got %q", reasons(hc))
	}

	track := e.Track(a, a.Tracks[0], []newznab.Release{torrent("Daft Punk - Daftendirekt [FLAC]")})[0]
	wantApproved(t, track)
	if track.Target.TrackID != 1 || track.Target.AlbumID != 9 {
		t.Errorf("want the track target, got %+v", track.Target)
	}
	a.Monitored, a.ReleaseDate = false, date(2027, 1, 1)
	d := e.Track(a, a.Tracks[1], []newznab.Release{torrent("Daft Punk - Revolution 909 [FLAC]")})[0]
	wantRejected(t, d, "Album isn't monitored")
	wantRejected(t, d, "Album isn't released until 1 Jan 2027")
	wantRejected(t, d, "Track already has a file")
}

func TestSort_RadarrComparer(t *testing.T) {
	e := engine()
	base := torrent("Inception 2010 1080p BluRay")

	cases := []struct {
		name          string
		better, worse func(*newznab.Release)
	}{
		{"quality weight", func(r *newznab.Release) { r.Title = "Inception 2010 2160p BluRay" }, func(r *newznab.Release) {}},
		{"proper beats original", func(r *newznab.Release) { r.Title = "Inception 2010 PROPER 1080p BluRay" }, func(r *newznab.Release) {}},
		{"lower priority number", func(r *newznab.Release) { r.IndexerPriority = 1 }, func(r *newznab.Release) {}},
		{"more seeders (log scale)", func(r *newznab.Release) { r.Seeders = 500 }, func(r *newznab.Release) { r.Seeders = 12 }},
		{"bigger, beyond 200 MB", func(r *newznab.Release) { r.Size += 1 << 30 }, func(r *newznab.Release) {}},
	}
	for _, tc := range cases {
		a, b := base, base
		a.GUID, b.GUID = "better", "worse"
		tc.better(&a)
		tc.worse(&b)
		ds := e.Movie(inception(), []newznab.Release{b, a})
		if ds[0].Release.GUID != "better" {
			t.Errorf("%s: want the better release first, got %q then %q", tc.name, ds[0].Release.Title, ds[1].Release.Title)
		}
	}

	proper, plain := base, base
	proper.GUID, proper.Title, proper.Seeders = "proper", "Inception 2010 PROPER 1080p BluRay", 12
	plain.GUID, plain.Seeders = "plain", 500
	e.Propers = store.PropersDoNotPrefer
	if ds := e.Movie(inception(), []newznab.Release{proper, plain}); ds[0].Release.GUID != "plain" {
		t.Errorf("want propers ignored when not preferred, got %q first", ds[0].Release.GUID)
	}

	flagged, unflagged := base, base
	flagged.GUID, flagged.Flags, flagged.Seeders = "flagged", newznab.FlagFreeleech, 12
	unflagged.GUID, unflagged.Seeders = "unflagged", 500
	e.Propers = store.PropersPreferAndUpgrade
	if ds := e.Movie(inception(), []newznab.Release{flagged, unflagged}); ds[0].Release.GUID != "unflagged" {
		t.Errorf("want flags ignored unless preferred, got %q first", ds[0].Release.GUID)
	}
	e.Settings.PreferIndexerFlags = true
	if ds := e.Movie(inception(), []newznab.Release{unflagged, flagged}); ds[0].Release.GUID != "flagged" {
		t.Errorf("want a freeleech release first when flags are preferred, got %q first", ds[0].Release.GUID)
	}

	rejected := base
	rejected.GUID, rejected.Title = "rejected", "Inception 2010 2160p BluRay KORSUB"
	ds := e.Movie(inception(), []newznab.Release{rejected, base})
	if ds[0].Release.GUID == "rejected" {
		t.Errorf("want approved releases before rejected ones")
	}
	if best, ok := Best(ds); !ok || best.Release.GUID != base.GUID {
		t.Errorf("want Best to pick the approved release, got %+v", best)
	}
}

func TestKeywordScore(t *testing.T) {
	words := []store.PreferredWord{{Term: "x265", Score: 10}, {Term: "REPACK", Score: -5}}
	cases := []struct {
		title string
		want  int
	}{
		{"Inception 2010 1080p BluRay x265", 10},
		{"inception 2010 1080p bluray X265", 10}, // case-insensitive on both sides
		{"Inception 2010 1080p BluRay x265 REPACK", 5},
		{"Inception 2010 1080p BluRay x265 x265 x265", 10}, // presence, not occurrence count
		{"Inception 2010 1080p BluRay x264", 0},
	}
	for _, tc := range cases {
		if got := keywordScore(tc.title, words); got != tc.want {
			t.Errorf("keywordScore(%q) = %d, want %d", tc.title, got, tc.want)
		}
	}
	if got := keywordScore("anything", nil); got != 0 {
		t.Errorf("keywordScore with no configured words = %d, want 0", got)
	}
}

// TestSort_KeywordScore checks that a Settings -> Preferred Words score
// breaks ties between otherwise-equal releases, and - since 2026-09-15 -
// outranks raw quality weight too (see Better's doc comment for why: a
// real case where a small, fairly arbitrary weight gap between two
// equally-allowed qualities beat a release the user had actually expressed a
// preference for via Preferred Words).
func TestSort_KeywordScore(t *testing.T) {
	e := engine()
	e.PreferredWords = []store.PreferredWord{{Term: "GROUP-A", Score: 10}}
	preferred, other := torrent("Inception 2010 1080p BluRay GROUP-A"), torrent("Inception 2010 1080p BluRay GROUP-B")
	preferred.GUID, other.GUID = "preferred", "other"

	if ds := e.Movie(inception(), []newznab.Release{other, preferred}); ds[0].Release.GUID != "preferred" {
		t.Errorf("want the preferred-word release first, got %q", ds[0].Release.GUID)
	}

	// A keyword score now beats a higher quality weight too.
	betterQuality := torrent("Inception 2010 2160p BluRay GROUP-B")
	betterQuality.GUID = "better-quality"
	if ds := e.Movie(inception(), []newznab.Release{preferred, betterQuality}); ds[0].Release.GUID != "preferred" {
		t.Errorf("want a keyword score to beat raw quality weight, got %q first", ds[0].Release.GUID)
	}

	// Equal keyword score (including zero, the common case) still falls
	// through to quality weight exactly as before.
	if ds := e.Movie(inception(), []newznab.Release{other, betterQuality}); ds[0].Release.GUID != "better-quality" {
		t.Errorf("want quality weight to decide when keyword scores are equal, got %q first", ds[0].Release.GUID)
	}
}

func profileWith(upgrades bool, cutoff string) Profile {
	items := store.DefaultQualityProfileItems()
	return Profile{Items: items, UpgradeAllowed: upgrades, Cutoff: cutoff}
}

// TestMovie_Upgrades: Radarr's rule - the profile must allow upgrades, the
// file must be below the cutoff, and only a better release qualifies (or a
// proper of the same quality when propers are preferred and upgraded).
func TestMovie_Upgrades(t *testing.T) {
	e := engine()
	e.UserInvoked = true
	m := inception()
	m.HasFile, m.FileQuality, m.FileRelease = true, releaseparse.Parse("Inception 2010 720p HDTV x264"), "Inception 2010 720p HDTV x264"
	m.QualityProfileID = sql.NullInt64{Int64: 3, Valid: true}

	e.Profiles[3] = profileWith(false, "")
	d := e.Movie(m, []newznab.Release{torrent("Inception 2010 1080p BluRay x264")})[0]
	wantRejected(t, d, "doesn't allow upgrades")

	e.Profiles[3] = profileWith(true, "Bluray-1080p")
	ds := e.Movie(m, []newznab.Release{
		torrent("Inception 2010 1080p BluRay x264"), torrent("Inception 2010 480p DVDRip"),
		torrent("Inception 2010 720p HDTV PROPER x264"), torrent("Inception 2010 720p HDTV x264-OTHER"),
	})
	byTitle := map[string]Decision{}
	for _, d := range ds {
		byTitle[d.Release.Title] = d
	}
	wantApproved(t, byTitle["Inception 2010 1080p BluRay x264"])
	if !byTitle["Inception 2010 1080p BluRay x264"].Upgrade {
		t.Errorf("want the approved release flagged as an upgrade")
	}
	wantRejected(t, byTitle["Inception 2010 480p DVDRip"], "Not an upgrade")
	wantApproved(t, byTitle["Inception 2010 720p HDTV PROPER x264"])
	wantRejected(t, byTitle["Inception 2010 720p HDTV x264-OTHER"], "Not an upgrade")

	e.Propers = store.PropersDoNotUpgrade
	d = e.Movie(m, []newznab.Release{torrent("Inception 2010 720p HDTV PROPER x264")})[0]
	wantRejected(t, d, "Not an upgrade")

	// At or above the cutoff nothing is wanted.
	m.FileQuality = releaseparse.Parse("Inception 2010 1080p BluRay x264")
	d = e.Movie(m, []newznab.Release{torrent("Inception 2010 2160p Remux")})[0]
	wantRejected(t, d, "already meets the profile's cutoff (Bluray-1080p)")

	// No cutoff means upgrade until the best allowed quality.
	e.Profiles[3] = profileWith(true, "")
	d = e.Movie(m, []newznab.Release{torrent("Inception 2010 2160p Remux")})[0]
	wantApproved(t, d)
	if !e.Profiles.CutoffUnmet(m.QualityProfileID, m.FileQuality) {
		t.Errorf("want a Bluray-1080p file below an unset cutoff to count as cutoff unmet")
	}
	m.FileQuality = releaseparse.Parse("Inception 2010 2160p Remux")
	if e.Profiles.CutoffUnmet(m.QualityProfileID, m.FileQuality) {
		t.Errorf("want the best quality to meet the cutoff")
	}
}
