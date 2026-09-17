// Package mediainfo reads what is inside a media file - video and audio
// codecs, resolution, bit depth, HDR, audio tracks, subtitles, runtime - the
// way Sonarr, Radarr and Lidarr do: with FFprobe, or from Plex's own
// analysis of the same file.
package mediainfo

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Schema is bumped when Info gains fields worth re-reading files for.
const Schema = 1

// Where an Info came from.
const (
	SourceFFprobe = "ffprobe"
	SourcePlex    = "plex"
)

// Info is one file's media information, stored as JSON in the media_info
// column of movie_files, episode_files and track_files.
type Info struct {
	Schema     int       `json:"schema"`
	Source     string    `json:"source,omitempty"`
	AnalyzedAt time.Time `json:"analyzedAt"`
	// Error is why the file couldn't be read; the file isn't retried until
	// it's re-analyzed on purpose.
	Error string `json:"error,omitempty"`

	Container      string  `json:"container,omitempty"`
	RunTimeSeconds float64 `json:"runTime,omitempty"`
	OverallBitrate int64   `json:"bitrate,omitempty"`

	VideoCodec            string  `json:"videoCodec,omitempty"`  // as Sonarr names it: x265, h264, AV1
	VideoFormat           string  `json:"videoFormat,omitempty"` // FFprobe's codec name: hevc
	VideoProfile          string  `json:"videoProfile,omitempty"`
	VideoBitDepth         int     `json:"videoBitDepth,omitempty"`
	VideoBitrate          int64   `json:"videoBitrate,omitempty"`
	Width                 int     `json:"width,omitempty"`
	Height                int     `json:"height,omitempty"`
	VideoFPS              float64 `json:"videoFps,omitempty"`
	VideoDynamicRange     string  `json:"videoDynamicRange,omitempty"`     // HDR or blank
	VideoDynamicRangeType string  `json:"videoDynamicRangeType,omitempty"` // DV HDR10, HDR10, HLG, DV

	AudioCodec         string   `json:"audioCodec,omitempty"` // DTS-HD MA, EAC3 Atmos, FLAC
	AudioFormat        string   `json:"audioFormat,omitempty"`
	AudioProfile       string   `json:"audioProfile,omitempty"`
	AudioChannels      float64  `json:"audioChannels,omitempty"` // 5.1
	AudioBitrate       int64    `json:"audioBitrate,omitempty"`
	AudioSampleRate    int      `json:"audioSampleRate,omitempty"`
	AudioBitsPerSample int      `json:"audioBitsPerSample,omitempty"`
	AudioStreamCount   int      `json:"audioStreams,omitempty"`
	AudioLanguages     []string `json:"audioLanguages,omitempty"` // ISO 639-2, e.g. eng
	Subtitles          []string `json:"subtitles,omitempty"`
}

// Decode reads a stored media_info value; "" and "{}" give the zero Info.
func Decode(raw string) Info {
	var i Info
	if raw == "" || raw == "{}" {
		return i
	}
	_ = json.Unmarshal([]byte(raw), &i)
	return i
}

// Encode is the value stored in media_info.
func (i Info) Encode() string {
	b, _ := json.Marshal(i)
	return string(b)
}

// Analyzed reports whether the file was read successfully.
func (i Info) Analyzed() bool { return i.Schema > 0 && i.Error == "" }

// Failed builds the stored record of a file that couldn't be read.
func Failed(source string, err error, at time.Time) Info {
	return Info{Schema: Schema, Source: source, AnalyzedAt: at, Error: err.Error()}
}

// Resolution is the quality tier of the picture: 2160p, 1080p, 720p or
// 480p, judged by width or height as Sonarr does, so a 2160x1080 or
// 1920x800 file still counts as 1080p.
func (i Info) Resolution() string {
	w, h := i.Width, i.Height
	switch {
	case w == 0 && h == 0:
		return ""
	case w >= 3200 || h >= 2100:
		return "2160p"
	case w >= 1800 || h >= 1000:
		return "1080p"
	case w >= 1200 || h >= 700:
		return "720p"
	}
	return "480p"
}

// ChannelsText is 5.1, 2.0 or blank.
func (i Info) ChannelsText() string {
	if i.AudioChannels <= 0 {
		return ""
	}
	return strconv.FormatFloat(i.AudioChannels, 'f', 1, 64)
}

// VideoSummary is "x265 · 10-bit · DV HDR10" for the pages.
func (i Info) VideoSummary() string {
	var parts []string
	if i.VideoCodec != "" {
		parts = append(parts, i.VideoCodec)
	}
	if i.VideoBitDepth > 0 {
		parts = append(parts, fmt.Sprintf("%d-bit", i.VideoBitDepth))
	}
	if i.VideoDynamicRangeType != "" {
		parts = append(parts, i.VideoDynamicRangeType)
	}
	return strings.Join(parts, " · ")
}

// AudioSummary is "DTS-HD MA 5.1", with "+1 more" when there are more tracks.
func (i Info) AudioSummary() string {
	s := strings.TrimSpace(i.AudioCodec + " " + i.ChannelsText())
	if s != "" && i.AudioStreamCount > 1 {
		s += fmt.Sprintf(" +%d more", i.AudioStreamCount-1)
	}
	return s
}

// TrackSummary is what a music track shows: "FLAC · 24-bit · 44.1 kHz" or
// "MP3 · 320 kbps · 44.1 kHz".
func (i Info) TrackSummary() string {
	var parts []string
	if i.AudioCodec != "" {
		parts = append(parts, i.AudioCodec)
	}
	if lossless(i.AudioFormat) {
		if i.AudioBitsPerSample > 0 {
			parts = append(parts, fmt.Sprintf("%d-bit", i.AudioBitsPerSample))
		}
	} else if i.AudioBitrate > 0 {
		parts = append(parts, fmt.Sprintf("%d kbps", int(math.Round(float64(i.AudioBitrate)/1000))))
	}
	if i.AudioSampleRate > 0 {
		parts = append(parts, sampleRateText(i.AudioSampleRate)+" kHz")
	}
	return strings.Join(parts, " · ")
}

// ResolutionText is "3840x2076".
func (i Info) ResolutionText() string {
	if i.Width == 0 || i.Height == 0 {
		return ""
	}
	return fmt.Sprintf("%dx%d", i.Width, i.Height)
}

// RuntimeText is "1h 24m" or "3m 22s".
func (i Info) RuntimeText() string {
	d := time.Duration(i.RunTimeSeconds * float64(time.Second)).Round(time.Second)
	switch {
	case d <= 0:
		return ""
	case d >= time.Hour:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func sampleRateText(hz int) string {
	return strconv.FormatFloat(float64(hz)/1000, 'f', -1, 64)
}

func lossless(format string) bool {
	switch {
	case format == "flac", format == "alac", format == "ape", format == "wavpack", format == "truehd", format == "wmalossless",
		strings.HasPrefix(format, "pcm"), strings.HasPrefix(format, "dsd"):
		return true
	}
	return false
}

// Tokens are Sonarr's, Radarr's and Lidarr's {MediaInfo ...} naming tokens.
func (i Info) Tokens() map[string]string {
	t := map[string]string{
		"MediaInfo VideoCodec":            i.VideoCodec,
		"MediaInfo VideoDynamicRange":     i.VideoDynamicRange,
		"MediaInfo VideoDynamicRangeType": i.VideoDynamicRangeType,
		"MediaInfo AudioCodec":            i.AudioCodec,
		"MediaInfo AudioChannels":         i.ChannelsText(),
		"MediaInfo AudioLanguages":        languagesToken(i.AudioLanguages, true),
		"MediaInfo AudioLanguagesAll":     languagesToken(i.AudioLanguages, false),
		"MediaInfo SubtitleLanguages":     languagesToken(i.Subtitles, false),
	}
	if i.VideoBitDepth > 0 {
		t["MediaInfo VideoBitDepth"] = strconv.Itoa(i.VideoBitDepth)
	}
	if i.AudioBitrate > 0 {
		t["MediaInfo AudioBitRate"] = fmt.Sprintf("%d kbps", int(math.Round(float64(i.AudioBitrate)/1000)))
	}
	if i.AudioBitsPerSample > 0 {
		t["MediaInfo AudioBitsPerSample"] = strconv.Itoa(i.AudioBitsPerSample)
	}
	if i.AudioSampleRate > 0 {
		t["MediaInfo AudioSampleRate"] = sampleRateText(i.AudioSampleRate)
	}
	simple := strings.TrimSpace(i.VideoCodec + " " + i.AudioCodec)
	t["MediaInfo Simple"] = simple
	t["MediaInfo Full"] = strings.TrimSpace(simple + " " + t["MediaInfo AudioLanguages"])
	return t
}

// languagesToken is "[EN+DE]"; with hideEnglishOnly an English-only file
// gives "", as Sonarr's {MediaInfo AudioLanguages} does.
func languagesToken(codes []string, hideEnglishOnly bool) string {
	var shorts []string
	for _, c := range codes {
		shorts = appendUnique(shorts, LanguageShort(c))
	}
	if len(shorts) == 0 || (hideEnglishOnly && len(shorts) == 1 && shorts[0] == "EN") {
		return ""
	}
	return "[" + strings.Join(shorts, "+") + "]"
}

func appendUnique(list []string, v string) []string {
	for _, have := range list {
		if have == v {
			return list
		}
	}
	return append(list, v)
}

// ErrNoStreams is a file with neither audio nor video in it.
var ErrNoStreams = errors.New("no audio or video streams in the file")
