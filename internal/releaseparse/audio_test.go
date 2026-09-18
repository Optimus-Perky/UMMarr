package releaseparse_test

import (
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
)

func TestParseAudio(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		{"Artist - Album (2013) [FLAC 24bit]", "FLAC-24bit"},
		{"Artist - Album (2013) [FLAC]", "FLAC"},
		{"Artist - Album [FLAC 16-bit]", "FLAC"},
		{"Artist - Album [MP3 320kbps]", "MP3-320"},
		{"Artist - Album MP3 192", "MP3-192"},
		{"Artist - Album [MP3 V0]", "MP3-VBR"},
		{"Artist - Album (1997) [ALAC]", "ALAC"},
		{"Artist - Album [AAC 256]", "AAC"},
		{"Artist - Album (WEB) [Opus]", "Opus"},
		{"Artist - Album 2013", "Unknown"},
		// A year is not a bitrate, and neither is the 24 in 24bit.
		{"Artist - Album (1997) [MP3]", "Unknown"},
	}
	for _, c := range cases {
		if got := releaseparse.ParseAudio(c.title).Key(); got != c.want {
			t.Errorf("ParseAudio(%q).Key() = %q, want %q", c.title, got, c.want)
		}
	}
}

func TestAudioQuality_KeyFromProbedValues(t *testing.T) {
	cases := []struct {
		quality releaseparse.AudioQuality
		want    string
	}{
		{releaseparse.AudioQuality{Format: "flac", BitDepth: 16}, "FLAC"},
		{releaseparse.AudioQuality{Format: "flac", BitDepth: 24}, "FLAC-24bit"},
		{releaseparse.AudioQuality{Format: "mp3", Bitrate: 320}, "MP3-320"},
		{releaseparse.AudioQuality{Format: "mp3", Bitrate: 245}, "MP3-192"},
		{releaseparse.AudioQuality{Format: "mp3", Bitrate: 64}, "Unknown"},
		{releaseparse.AudioQuality{Format: "aac", Bitrate: 256}, "AAC"},
		{releaseparse.AudioQuality{Format: "alac"}, "ALAC"},
		{releaseparse.AudioQuality{}, "Unknown"},
	}
	for _, c := range cases {
		if got := c.quality.Key(); got != c.want {
			t.Errorf("%+v.Key() = %q, want %q", c.quality, got, c.want)
		}
	}
	if !(releaseparse.AudioQuality{}).Empty() {
		t.Error("want the zero AudioQuality to read as empty")
	}
}
