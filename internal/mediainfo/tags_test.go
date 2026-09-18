package mediainfo_test

import (
	"os"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
)

// The three spellings below are copied from real files in a Picard-tagged
// library: FLAC (Vorbis comments, SHOUTY_UNDERSCORES), MP4/M4A ("MusicBrainz
// Track Id", and track/disc as "1/20"), and MP3 (ID3v2 TXXX frames, which
// FFprobe hands back with the MP4-ish spacing). One normaliser has to cover
// all of them, because the library has all of them.

func TestParseAudioTags_EveryContainerSpelling(t *testing.T) {
	flac := map[string]string{
		"ALBUM": "Avril Lavigne", "ARTIST": "Avril Lavigne", "TITLE": "Rock ’n’ Roll",
		"track": "1", "TRACKTOTAL": "13", "disc": "1", "TOTALDISCS": "1", "DATE": "2013-01-01",
		"MUSICBRAINZ_TRACKID":        "49d6c691-5c85-4443-af24-86aea22d99c9",
		"MUSICBRAINZ_RELEASETRACKID": "5f5d4a00-164e-4a93-979d-1f7ad68a59db",
		"MUSICBRAINZ_ALBUMID":        "2e7dd2ba-9402-40cf-bd08-c3a16366a1c6",
		"MUSICBRAINZ_RELEASEGROUPID": "f8c7022d-2cb2-4e19-bd46-b0e9460347a5",
		"MUSICBRAINZ_ARTISTID":       "0103c1cc-4a09-4a5d-a344-56ad99a77193",
		"MUSICBRAINZ_ALBUMARTISTID":  "0103c1cc-4a09-4a5d-a344-56ad99a77193",
	}
	mp4 := map[string]string{
		"album": "The Definitive Collection", "artist": "ABBA", "album_artist": "ABBA",
		"title": "People Need Love", "track": "1/20", "disc": "1/2", "date": "2001-01-01",
		"MusicBrainz Track Id":         "5edf5416-9766-4ad7-9aa5-b8119f55da20",
		"MusicBrainz Album Id":         "a3ded330-7c94-3482-996b-782948bff0e7",
		"MusicBrainz Release Group Id": "e2996feb-86b9-3550-88ec-f1c9c9af3961",
		"MusicBrainz Artist Id":        "d87e52c5-bb8d-4da8-b941-9f4928627dc8",
	}

	got := mediainfo.ParseAudioTags(flac)
	if got.RecordingMBID != "49d6c691-5c85-4443-af24-86aea22d99c9" || got.TrackMBID != "5f5d4a00-164e-4a93-979d-1f7ad68a59db" {
		t.Errorf("flac: recording/track ids = %q / %q", got.RecordingMBID, got.TrackMBID)
	}
	if got.ReleaseMBID != "2e7dd2ba-9402-40cf-bd08-c3a16366a1c6" || got.ReleaseGroupMBID != "f8c7022d-2cb2-4e19-bd46-b0e9460347a5" {
		t.Errorf("flac: release/group ids = %q / %q", got.ReleaseMBID, got.ReleaseGroupMBID)
	}
	if got.TrackNumber != 1 || got.TrackTotal != 13 || got.DiscNumber != 1 || got.DiscTotal != 1 {
		t.Errorf("flac: numbers = track %d/%d disc %d/%d", got.TrackNumber, got.TrackTotal, got.DiscNumber, got.DiscTotal)
	}

	got = mediainfo.ParseAudioTags(mp4)
	// "1/20" and "1/2" carry the totals with them.
	if got.TrackNumber != 1 || got.TrackTotal != 20 || got.DiscNumber != 1 || got.DiscTotal != 2 {
		t.Errorf("mp4: numbers = track %d/%d disc %d/%d", got.TrackNumber, got.TrackTotal, got.DiscNumber, got.DiscTotal)
	}
	if got.RecordingMBID != "5edf5416-9766-4ad7-9aa5-b8119f55da20" || got.ReleaseGroupMBID != "e2996feb-86b9-3550-88ec-f1c9c9af3961" {
		t.Errorf("mp4: ids = %q / %q", got.RecordingMBID, got.ReleaseGroupMBID)
	}
	if got.AlbumArtist != "ABBA" || got.Album != "The Definitive Collection" {
		t.Errorf("mp4: album artist %q, album %q", got.AlbumArtist, got.Album)
	}

	// The spelling itself must not matter.
	spellings := []string{"MUSICBRAINZ_ALBUMID", "MusicBrainz Album Id", "musicbrainz-album-id", "musicbrainz album id"}
	for _, key := range spellings {
		if got := mediainfo.ParseAudioTags(map[string]string{key: "abc"}); got.ReleaseMBID != "abc" {
			t.Errorf("%q wasn't recognised as the album MBID", key)
		}
	}
}

func TestParseAudioTags_UntaggedFile(t *testing.T) {
	// A file with only container noise has nothing to match on, and has to
	// say so - that is what sends it to manual matching instead of being
	// guessed at.
	got := mediainfo.ParseAudioTags(map[string]string{
		"major_brand": "M4A ", "minor_version": "0", "encoder": "iTunes", "compatible_brands": "M4A mp42isom",
	})
	if !got.Empty() {
		t.Errorf("want nothing parsed from container noise, got %+v", got)
	}
	if got.HasMusicBrainz() {
		t.Error("want HasMusicBrainz false for an untagged file")
	}
	// Tagged but not by Picard: usable for display, not for exact matching.
	got = mediainfo.ParseAudioTags(map[string]string{"TITLE": "Untitled", "track": "3"})
	if got.Empty() || got.HasMusicBrainz() {
		t.Errorf("want tags without MusicBrainz ids to parse but not claim an exact match: %+v", got)
	}
}

func TestParseFFprobe_KeepsTheTags(t *testing.T) {
	probe := []byte(`{"streams":[{"codec_type":"audio","codec_name":"flac","sample_rate":"44100","channels":2,"bits_per_raw_sample":"16"}],
		"format":{"format_name":"flac","duration":"215.0","bit_rate":"900000",
		"tags":{"TITLE":"Mysterons","MUSICBRAINZ_TRACKID":"abc-123","disc":"1/2"}}}`)
	info, err := mediainfo.ParseFFprobe(probe)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.Tags == nil {
		t.Fatal("want the file's tags kept on the Info")
	}
	if info.Tags.RecordingMBID != "abc-123" || info.Tags.DiscNumber != 1 || info.Tags.DiscTotal != 2 {
		t.Errorf("tags = %+v", *info.Tags)
	}
	// A file with no tags of its own doesn't carry an empty object around.
	bare, err := mediainfo.ParseFFprobe([]byte(`{"streams":[{"codec_type":"video","codec_name":"hevc","width":1920,"height":1080}],
		"format":{"format_name":"matroska","duration":"1.0"}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if bare.Tags != nil {
		t.Errorf("want no Tags for an untagged file, got %+v", *bare.Tags)
	}
}

// The fixtures here are real ffprobe output from a hand-managed library: a
// multi-disc M4A rip (disc 2 of 2) and a Picard-tagged FLAC. Parsing them
// is what the whole matching change rests on, so it is checked against the
// real thing rather than only against hand-written maps.
func TestParseFFprobe_RealLibraryFiles(t *testing.T) {
	for _, c := range []struct {
		file             string
		wantDisc, wantNo int
		wantMBID         bool
	}{
		{"testdata/tagged-m4a.json", 2, 1, true},
		{"testdata/tagged-flac.json", 1, 1, true},
	} {
		raw, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatalf("read %s: %v", c.file, err)
		}
		info, err := mediainfo.ParseFFprobe(raw)
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		if info.Tags == nil {
			t.Fatalf("%s: no tags parsed", c.file)
		}
		if info.Tags.DiscNumber != c.wantDisc || info.Tags.TrackNumber != c.wantNo {
			t.Errorf("%s: disc %d track %d, want disc %d track %d",
				c.file, info.Tags.DiscNumber, info.Tags.TrackNumber, c.wantDisc, c.wantNo)
		}
		if info.Tags.HasMusicBrainz() != c.wantMBID {
			t.Errorf("%s: HasMusicBrainz = %v", c.file, info.Tags.HasMusicBrainz())
		}
		if info.Tags.Album == "" || info.Tags.Title == "" {
			t.Errorf("%s: album %q title %q", c.file, info.Tags.Album, info.Tags.Title)
		}
	}
}
