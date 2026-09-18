package mediainfo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type probeOutput struct {
	Streams []probeStream `json:"streams"`
	Format  struct {
		FormatName string            `json:"format_name"`
		Duration   string            `json:"duration"`
		BitRate    string            `json:"bit_rate"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
}

type probeStream struct {
	CodecName        string            `json:"codec_name"`
	CodecType        string            `json:"codec_type"`
	CodecTag         string            `json:"codec_tag_string"`
	Profile          string            `json:"profile"`
	Width            int               `json:"width"`
	Height           int               `json:"height"`
	PixFmt           string            `json:"pix_fmt"`
	ColorTransfer    string            `json:"color_transfer"`
	RFrameRate       string            `json:"r_frame_rate"`
	AvgFrameRate     string            `json:"avg_frame_rate"`
	BitsPerRawSample string            `json:"bits_per_raw_sample"`
	BitsPerSample    int               `json:"bits_per_sample"`
	SampleRate       string            `json:"sample_rate"`
	Channels         int               `json:"channels"`
	ChannelLayout    string            `json:"channel_layout"`
	BitRate          string            `json:"bit_rate"`
	Disposition      map[string]int    `json:"disposition"`
	Tags             map[string]string `json:"tags"`
	SideData         []struct {
		Type string `json:"side_data_type"`
	} `json:"side_data_list"`
}

// ParseFFprobe reads the JSON of `ffprobe -print_format json -show_format
// -show_streams`.
func ParseFFprobe(data []byte) (Info, error) {
	var out probeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return Info{}, fmt.Errorf("read ffprobe output: %w", err)
	}
	info := Info{Schema: Schema, Source: SourceFFprobe}
	info.Container = containerName(out.Format.FormatName)
	info.RunTimeSeconds, _ = strconv.ParseFloat(out.Format.Duration, 64)
	info.OverallBitrate, _ = strconv.ParseInt(out.Format.BitRate, 10, 64)
	if tags := ParseAudioTags(out.Format.Tags); !tags.Empty() {
		info.Tags = &tags
	}

	var video, audio *probeStream
	for n := range out.Streams {
		s := &out.Streams[n]
		switch s.CodecType {
		case "video":
			// Cover art and attached pictures aren't the picture.
			if s.Disposition["attached_pic"] == 1 || imageCodec(s.CodecName) {
				continue
			}
			if video == nil {
				video = s
			}
		case "audio":
			info.AudioStreamCount++
			if lang := normalizeLanguage(tag(s.Tags, "language")); lang != "" {
				info.AudioLanguages = appendUnique(info.AudioLanguages, lang)
			}
			if audio == nil || (s.Disposition["default"] == 1 && audio.Disposition["default"] != 1) {
				audio = s
			}
		case "subtitle":
			if lang := normalizeLanguage(tag(s.Tags, "language")); lang != "" {
				info.Subtitles = appendUnique(info.Subtitles, lang)
			}
		}
	}
	if video == nil && audio == nil {
		return info, ErrNoStreams
	}
	if video != nil {
		info.VideoFormat, info.VideoProfile = video.CodecName, video.Profile
		encoder := strings.ToLower(tag(video.Tags, "encoder") + " " + tag(out.Format.Tags, "encoder") + " " + tag(out.Format.Tags, "writing_library"))
		info.VideoCodec = FormatVideoCodec(video.CodecName, video.CodecTag, encoder)
		info.Width, info.Height = video.Width, video.Height
		info.VideoBitDepth = videoBitDepth(video)
		info.VideoBitrate = streamBitrate(video)
		info.VideoFPS = frameRate(video.AvgFrameRate)
		if info.VideoFPS == 0 {
			info.VideoFPS = frameRate(video.RFrameRate)
		}
		info.VideoDynamicRangeType = dynamicRangeType(video)
		if info.VideoDynamicRangeType != "" {
			info.VideoDynamicRange = "HDR"
		}
	}
	if audio != nil {
		info.AudioFormat, info.AudioProfile = audio.CodecName, audio.Profile
		info.AudioCodec = FormatAudioCodec(audio.CodecName, audio.Profile)
		info.AudioChannels = Channels(audio.Channels, audio.ChannelLayout)
		info.AudioBitrate = streamBitrate(audio)
		if info.AudioBitrate == 0 && video == nil {
			info.AudioBitrate = info.OverallBitrate
		}
		sampleRate, _ := strconv.Atoi(audio.SampleRate)
		info.AudioSampleRate = sampleRate
		info.AudioBitsPerSample, _ = strconv.Atoi(audio.BitsPerRawSample)
		if info.AudioBitsPerSample == 0 {
			info.AudioBitsPerSample = audio.BitsPerSample
		}
	}
	return info, nil
}

// tag looks a tag up whatever its case (MKV writes ENCODER, MP4 encoder).
func tag(tags map[string]string, name string) string {
	for k, v := range tags {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func imageCodec(codec string) bool {
	switch codec {
	case "mjpeg", "png", "bmp", "gif", "webp", "tiff":
		return true
	}
	return false
}

func containerName(format string) string {
	first, _, _ := strings.Cut(format, ",")
	switch first {
	case "matroska":
		return "MKV"
	case "mov":
		return "MP4"
	case "mpegts":
		return "TS"
	case "":
		return ""
	}
	return strings.ToUpper(first)
}

// FormatVideoCodec names a video codec as Sonarr does: x264/x265 when the
// encoder says so, otherwise h264/h265, AV1, VP9, XviD and so on.
func FormatVideoCodec(codec, fourcc, encoder string) string {
	switch strings.ToLower(codec) {
	case "h264", "avc", "avc1":
		if strings.Contains(encoder, "x264") {
			return "x264"
		}
		return "h264"
	case "hevc", "h265":
		if strings.Contains(encoder, "x265") {
			return "x265"
		}
		return "h265"
	case "av1":
		return "AV1"
	case "vp9":
		return "VP9"
	case "vp8":
		return "VP8"
	case "mpeg2video", "mpeg2":
		return "MPEG2"
	case "mpeg1video":
		return "MPEG"
	case "vc1":
		return "VC1"
	case "wmv2", "wmv3":
		return "WMV"
	case "mpeg4":
		switch strings.ToUpper(fourcc) {
		case "XVID":
			return "XviD"
		case "DIVX", "DX50", "DIV3":
			return "DivX"
		}
		return "MPEG4"
	case "":
		return ""
	}
	return strings.ToUpper(codec)
}

// FormatAudioCodec names an audio codec as Sonarr does, using the profile
// to tell DTS-HD MA, DTS-X, Atmos and HE-AAC apart. It also takes Plex's
// codec names (dca, dca-ma) and short profiles (ma, hra).
func FormatAudioCodec(codec, profile string) string {
	c, p := strings.ToLower(codec), strings.ToLower(profile)
	switch {
	case c == "aac":
		if strings.Contains(p, "he-aac") || strings.HasPrefix(p, "he") {
			return "HE-AAC"
		}
		return "AAC"
	case c == "ac3":
		return "AC3"
	case c == "eac3":
		if strings.Contains(p, "atmos") {
			return "EAC3 Atmos"
		}
		return "EAC3"
	case c == "truehd":
		if strings.Contains(p, "atmos") {
			return "TrueHD Atmos"
		}
		return "TrueHD"
	case c == "dca-ma":
		return "DTS-HD MA"
	case c == "dts" || c == "dca":
		switch {
		case strings.Contains(p, "dts:x") || p == "x":
			return "DTS-X"
		case strings.Contains(p, "dts-hd ma") || p == "ma":
			return "DTS-HD MA"
		case strings.Contains(p, "hra"):
			return "DTS-HD HRA"
		case strings.Contains(p, "dts-es") || p == "es":
			return "DTS-ES"
		case strings.Contains(p, "express"):
			return "DTS Express"
		}
		return "DTS"
	case c == "flac":
		return "FLAC"
	case c == "alac":
		return "ALAC"
	case c == "mp3":
		return "MP3"
	case c == "mp2":
		return "MP2"
	case c == "opus":
		return "Opus"
	case c == "vorbis":
		return "Vorbis"
	case c == "wmav1" || c == "wmav2":
		return "WMA"
	case c == "wmapro":
		return "WMA Pro"
	case c == "wmalossless":
		return "WMA Lossless"
	case c == "ape":
		return "APE"
	case c == "wavpack":
		return "WavPack"
	case strings.HasPrefix(c, "pcm"):
		return "PCM"
	case strings.HasPrefix(c, "dsd"):
		return "DSD"
	case c == "":
		return ""
	}
	return strings.ToUpper(codec)
}

// Channels turns a channel count and layout into 2.0, 5.1, 7.1.
func Channels(count int, layout string) float64 {
	l := strings.ToLower(layout)
	if i := strings.IndexByte(l, '('); i >= 0 {
		l = l[:i]
	}
	switch l {
	case "mono":
		return 1
	case "stereo", "downmix":
		return 2
	case "quad":
		return 4
	}
	if f, err := strconv.ParseFloat(l, 64); err == nil {
		return f
	}
	switch count {
	case 3:
		return 2.1
	case 6:
		return 5.1
	case 7:
		return 6.1
	case 8:
		return 7.1
	}
	return float64(count)
}

func videoBitDepth(s *probeStream) int {
	if n, err := strconv.Atoi(s.BitsPerRawSample); err == nil && n > 0 {
		return n
	}
	pix := s.PixFmt
	switch {
	case pix == "":
		return 0
	case strings.Contains(pix, "12le") || strings.Contains(pix, "12be"):
		return 12
	case strings.Contains(pix, "10le") || strings.Contains(pix, "10be") || strings.HasPrefix(pix, "p010"):
		return 10
	}
	return 8
}

func dynamicRangeType(s *probeStream) string {
	dv := false
	for _, sd := range s.SideData {
		if strings.Contains(strings.ToUpper(sd.Type), "DOVI") {
			dv = true
		}
	}
	base := ""
	switch s.ColorTransfer {
	case "smpte2084":
		base = "HDR10"
	case "arib-std-b67":
		base = "HLG"
	}
	switch {
	case dv && base != "":
		return "DV " + base
	case dv:
		return "DV"
	}
	return base
}

func streamBitrate(s *probeStream) int64 {
	if n, err := strconv.ParseInt(s.BitRate, 10, 64); err == nil && n > 0 {
		return n
	}
	for k, v := range s.Tags {
		if strings.HasPrefix(strings.ToUpper(k), "BPS") {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

func frameRate(rate string) float64 {
	num, den, ok := strings.Cut(rate, "/")
	if !ok {
		f, _ := strconv.ParseFloat(rate, 64)
		return f
	}
	n, err1 := strconv.ParseFloat(num, 64)
	d, err2 := strconv.ParseFloat(den, 64)
	if err1 != nil || err2 != nil || d == 0 || n == 0 {
		return 0
	}
	return math.Round(n/d*1000) / 1000
}

// Prober runs FFprobe.
type Prober struct {
	Path    string
	Timeout time.Duration
}

// FindFFprobe returns a Prober for the ffprobe on PATH, or an error when
// there isn't one.
func FindFFprobe() (*Prober, error) {
	path, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, fmt.Errorf("ffprobe isn't installed: %w", err)
	}
	return &Prober{Path: path, Timeout: 2 * time.Minute}, nil
}

// Probe reads file.
func (p *Prober) Probe(ctx context.Context, file string) (Info, error) {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.Path, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", file)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		if msg == "" {
			msg = err.Error()
		}
		return Info{}, fmt.Errorf("ffprobe: %s", msg)
	}
	return ParseFFprobe(stdout.Bytes())
}
