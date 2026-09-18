package releaseparse

import (
	"regexp"
	"strconv"
	"strings"
)

// Audio quality, which the video catalog has nothing to say about: a music
// file is FLAC or MP3 at some bitrate, not WEBDL-1080p. Kept as a separate
// catalog so a music quality profile offers formats and a movie profile
// offers resolutions, rather than one list of mostly-irrelevant rows.

// AllAudioQualities is every audio tier UMMarr recognizes, worst to best -
// the row set an audio quality profile's weight table is built from.
var AllAudioQualities = []string{
	"Unknown",
	"MP3-128", "MP3-192", "MP3-256", "MP3-320", "MP3-VBR",
	"AAC", "Vorbis", "Opus",
	"ALAC", "FLAC", "FLAC-24bit", "WAV",
}

// AudioQuality is what a music file or release is, as the audio catalog
// sees it.
type AudioQuality struct {
	// Format is the codec family: FLAC, MP3, AAC, ALAC, Vorbis, Opus, WAV.
	Format string `json:"format,omitempty"`
	// Bitrate is kbps for lossy formats, 0 when unknown or lossless.
	Bitrate int `json:"bitrate,omitempty"`
	// BitDepth is 16 or 24 for lossless formats, 0 otherwise.
	BitDepth int `json:"bitDepth,omitempty"`
	// VBR marks a variable-bitrate encode, which has no single bitrate.
	VBR bool `json:"vbr,omitempty"`
}

// Empty reports whether nothing about the audio was worked out.
func (a AudioQuality) Empty() bool { return a == AudioQuality{} }

// Key maps an AudioQuality onto one row of AllAudioQualities. An unknown
// format, or a bitrate too low to be one of the MP3 tiers, is "Unknown" -
// the catalog is closed on purpose, as the video one is.
func (a AudioQuality) Key() string {
	switch strings.ToUpper(a.Format) {
	case "FLAC":
		if a.BitDepth >= 24 {
			return "FLAC-24bit"
		}
		return "FLAC"
	case "ALAC":
		return "ALAC"
	case "WAV", "PCM":
		return "WAV"
	case "AAC", "M4A", "MP4A":
		return "AAC"
	case "VORBIS", "OGG":
		return "Vorbis"
	case "OPUS":
		return "Opus"
	case "MP3", "MPEG AUDIO":
		if a.VBR {
			return "MP3-VBR"
		}
		switch {
		case a.Bitrate >= 320:
			return "MP3-320"
		case a.Bitrate >= 256:
			return "MP3-256"
		case a.Bitrate >= 192:
			return "MP3-192"
		case a.Bitrate >= 128:
			return "MP3-128"
		}
		return "Unknown"
	}
	return "Unknown"
}

// String is the key, or "" when nothing is known - what the UI shows.
func (a AudioQuality) String() string {
	if key := a.Key(); key != "Unknown" {
		return key
	}
	return ""
}

var (
	audioFormatPatterns = []struct {
		pattern *regexp.Regexp
		format  string
	}{
		{regexp.MustCompile(`(?i)\bflac\b`), "FLAC"},
		{regexp.MustCompile(`(?i)\balac\b`), "ALAC"},
		{regexp.MustCompile(`(?i)\bwav(?:e|pack)?\b`), "WAV"},
		{regexp.MustCompile(`(?i)\b(?:aac|m4a)\b`), "AAC"},
		{regexp.MustCompile(`(?i)\b(?:vorbis|ogg)\b`), "Vorbis"},
		{regexp.MustCompile(`(?i)\bopus\b`), "Opus"},
		{regexp.MustCompile(`(?i)\bmp3\b`), "MP3"},
	}
	audioBitrate  = regexp.MustCompile(`(?i)\b(\d{2,4})\s?kbps\b|\b(\d{3})\b(?:\s?kbps)?`)
	audioBitDepth = regexp.MustCompile(`(?i)\b(16|24)[\s-]?bits?\b`)
	audioVBR      = regexp.MustCompile(`(?i)\b(?:vbr|v0|v2|aps|apx)\b`)
)

// ParseAudio reads what a release title says about its audio: "Artist -
// Album (2013) [FLAC 24bit]" or "Album [MP3 320kbps]".
func ParseAudio(title string) AudioQuality {
	var a AudioQuality
	for _, p := range audioFormatPatterns {
		if p.pattern.MatchString(title) {
			a.Format = p.format
			break
		}
	}
	if m := audioBitDepth.FindStringSubmatch(title); m != nil {
		a.BitDepth, _ = strconv.Atoi(m[1])
	}
	if audioVBR.MatchString(title) {
		a.VBR = true
	}
	// A bare 3-digit number is only a bitrate for a lossy format - "24" in
	// "24bit" and a year like "1997" must not read as one.
	if a.Format == "MP3" || a.Format == "AAC" {
		for _, m := range audioBitrate.FindAllStringSubmatch(title, -1) {
			value := m[1]
			if value == "" {
				value = m[2]
			}
			if n, err := strconv.Atoi(value); err == nil && n >= 64 && n <= 1411 {
				a.Bitrate = n
				break
			}
		}
	}
	return a
}
