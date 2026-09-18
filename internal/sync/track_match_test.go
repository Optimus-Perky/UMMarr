package sync

import (
	"context"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// A library is not a fresh download: its files have been sitting there for
// years, often tagged by Picard, and matching them by position writes wrong
// answers into the library the moment one track is missing. These cover the
// three rules in order - the file's own MusicBrainz id, the disc and track
// it claims, then position - and the promise that a file nothing identifies
// is left alone rather than guessed at.

// taggedProbe fakes FFprobe: tags by file name.
func taggedProbe(tags map[string]mediainfo.AudioTags) func(context.Context, string) (mediainfo.Info, error) {
	return func(_ context.Context, path string) (mediainfo.Info, error) {
		for name, t := range tags {
			if len(path) >= len(name) && path[len(path)-len(name):] == name {
				tag := t
				return mediainfo.Info{Schema: mediainfo.Schema, Tags: &tag}, nil
			}
		}
		return mediainfo.Info{Schema: mediainfo.Schema}, nil
	}
}

func testTracks() []store.TrackImportInfo {
	return []store.TrackImportInfo{
		{ID: 1, Title: "One", MBID: "mbid-1", Medium: 1, Number: "1"},
		{ID: 2, Title: "Two", MBID: "mbid-2", Medium: 1, Number: "2"},
		{ID: 3, Title: "Three", MBID: "mbid-3", Medium: 2, Number: "1"},
	}
}

func files(names ...string) []importer.File {
	out := make([]importer.File, 0, len(names))
	for _, n := range names {
		out = append(out, importer.File{Path: n, Size: 1})
	}
	return out
}

func TestMatchTracks_PrefersTheFilesOwnMusicBrainzID(t *testing.T) {
	// The files are deliberately in the wrong order and named nothing like
	// their titles: only the ids are right.
	svc := &ImportService{Probe: taggedProbe(map[string]mediainfo.AudioTags{
		"a.flac": {TrackMBID: "mbid-3", TrackNumber: 9},
		"b.flac": {TrackMBID: "mbid-1"},
		"c.flac": {TrackMBID: "mbid-2"},
	})}
	matches, report := svc.MatchTracks(context.Background(), "/music", files("a.flac", "b.flac", "c.flac"), testTracks(), false)
	if report.ByMBID != 3 || len(report.Unmatched) != 0 {
		t.Fatalf("want all three matched by id, got %+v", report)
	}
	want := map[string]int64{"a.flac": 3, "b.flac": 1, "c.flac": 2}
	for _, m := range matches {
		if want[m.File.Path] != m.Track.ID {
			t.Errorf("%s went to track %d, want %d", m.File.Path, m.Track.ID, want[m.File.Path])
		}
		if m.How != MatchMBID {
			t.Errorf("%s matched by %q", m.File.Path, m.How)
		}
	}
}

func TestMatchTracks_FallsBackToDiscAndTrackTags(t *testing.T) {
	// No ids stored yet (an album synced before UMMarr kept them), but the
	// files know their own disc and track.
	tracks := testTracks()
	for i := range tracks {
		tracks[i].MBID = ""
	}
	svc := &ImportService{Probe: taggedProbe(map[string]mediainfo.AudioTags{
		"x.flac": {DiscNumber: 2, TrackNumber: 1},
		"y.flac": {DiscNumber: 1, TrackNumber: 2},
	})}
	matches, report := svc.MatchTracks(context.Background(), "/music", files("x.flac", "y.flac"), tracks, false)
	if report.ByNumbers != 2 || len(matches) != 2 {
		t.Fatalf("want both matched by tags, got %+v", report)
	}
	for _, m := range matches {
		switch m.File.Path {
		case "x.flac":
			if m.Track.ID != 3 {
				t.Errorf("disc 2 track 1 went to track %d, want 3", m.Track.ID)
			}
		case "y.flac":
			if m.Track.ID != 2 {
				t.Errorf("disc 1 track 2 went to track %d, want 2", m.Track.ID)
			}
		}
	}
}

// The rule Mark set: a file nothing can identify is left alone and flagged,
// never attached to whichever track happened to line up.
func TestMatchTracks_LeavesUnidentifiableFilesAlone(t *testing.T) {
	svc := &ImportService{Probe: taggedProbe(map[string]mediainfo.AudioTags{
		"tagged.flac": {TrackMBID: "mbid-2"},
		// untagged.flac has no entry: FFprobe reads it, it says nothing.
	})}
	matches, report := svc.MatchTracks(context.Background(), "/music", files("tagged.flac", "untagged.flac"), testTracks(), false)

	if len(matches) != 1 || matches[0].Track.ID != 2 {
		t.Fatalf("want only the tagged file matched, got %+v", matches)
	}
	if len(report.Unmatched) != 1 || report.Unmatched[0].Path != "untagged.flac" {
		t.Fatalf("want the untagged file reported for manual matching, got %+v", report.Unmatched)
	}
	if report.ByPosition != 0 {
		t.Error("want nothing matched by position on a scan")
	}
	if got := report.Summary(); got != "1 by MusicBrainz id, 1 left for manual matching" {
		t.Errorf("summary = %q", got)
	}
}

// A download is different: it's a release UMMarr asked for, whose files
// arrive in order, so position remains the last resort there.
func TestMatchTracks_PositionIsAllowedForADownload(t *testing.T) {
	svc := &ImportService{Probe: taggedProbe(map[string]mediainfo.AudioTags{
		"02.flac": {TrackMBID: "mbid-2"},
	})}
	matches, report := svc.MatchTracks(context.Background(), "/dl", files("01.flac", "02.flac", "03.flac"), testTracks(), true)
	if len(matches) != 3 || len(report.Unmatched) != 0 {
		t.Fatalf("want every file placed, got %d matches %+v", len(matches), report)
	}
	if report.ByMBID != 1 || report.ByPosition != 2 {
		t.Errorf("want 1 by id and 2 by position, got %+v", report)
	}
	// The tagged file keeps its own track, and the others fill the gaps
	// around it rather than shifting onto it.
	for _, m := range matches {
		if m.File.Path == "02.flac" && m.Track.ID != 2 {
			t.Errorf("the tagged file went to track %d, want 2", m.Track.ID)
		}
		if m.File.Path == "01.flac" && m.Track.ID != 1 {
			t.Errorf("01.flac went to track %d, want 1", m.Track.ID)
		}
		if m.File.Path == "03.flac" && m.Track.ID != 3 {
			t.Errorf("03.flac went to track %d, want 3", m.Track.ID)
		}
	}
}

// Without FFprobe there are no tags to read at all, so refusing to match
// would stop music importing entirely - that falls back to the old
// behaviour rather than breaking.
func TestMatchTracks_NoProbeStillImports(t *testing.T) {
	svc := &ImportService{}
	matches, report := svc.MatchTracks(context.Background(), "/music", files("1.flac", "2.flac"), testTracks(), false)
	if len(matches) != 2 || report.ByPosition != 2 || len(report.Unmatched) != 0 {
		t.Fatalf("want position matching when nothing can read tags, got %+v", report)
	}
}
