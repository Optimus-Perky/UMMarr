package mediainfo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) Info {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	info, err := ParseFFprobe(data)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return info
}

// Real FFprobe output from library files.
func TestParseFFprobe_RealFiles(t *testing.T) {
	dv := fixture(t, "movie-airplane2.json")
	if dv.VideoCodec != "h265" || dv.VideoBitDepth != 10 || dv.VideoDynamicRangeType != "DV" || dv.VideoDynamicRange != "HDR" || dv.Width != 3840 || dv.Height != 2076 || dv.Resolution() != "2160p" || dv.VideoFPS != 23.976 {
		t.Errorf("Dolby Vision movie video: %+v", dv)
	}
	if dv.AudioCodec != "DTS-HD MA" || dv.ChannelsText() != "2.0" || dv.AudioStreamCount != 2 || dv.AudioBitsPerSample != 24 || !reflect.DeepEqual(dv.AudioLanguages, []string{"eng"}) {
		t.Errorf("Dolby Vision movie audio: %+v", dv)
	}
	if !reflect.DeepEqual(dv.Subtitles, []string{"eng", "bul", "gre", "fre", "spa"}) || dv.Container != "MKV" || dv.RunTimeSeconds < 5000 {
		t.Errorf("Dolby Vision movie subtitles/container: %v %s %v", dv.Subtitles, dv.Container, dv.RunTimeSeconds)
	}
	if dv.VideoSummary() != "h265 · 10-bit · DV" || dv.AudioSummary() != "DTS-HD MA 2.0 +1 more" || dv.RuntimeText() != "1h 24m" {
		t.Errorf("summaries: %q %q %q", dv.VideoSummary(), dv.AudioSummary(), dv.RuntimeText())
	}
	tokens := dv.Tokens()
	for k, want := range map[string]string{
		"MediaInfo Simple": "h265 DTS-HD MA", "MediaInfo Full": "h265 DTS-HD MA", "MediaInfo AudioLanguages": "", "MediaInfo AudioLanguagesAll": "[EN]",
		"MediaInfo SubtitleLanguages": "[EN+BG+EL+FR+ES]", "MediaInfo VideoBitDepth": "10", "MediaInfo VideoDynamicRangeType": "DV", "MediaInfo AudioChannels": "2.0",
	} {
		if tokens[k] != want {
			t.Errorf("token %s = %q, want %q", k, tokens[k], want)
		}
	}

	atmos := fixture(t, "movie-minecraft.json")
	if atmos.AudioCodec != "EAC3 Atmos" || atmos.ChannelsText() != "5.1" || atmos.VideoBitDepth != 8 || atmos.VideoDynamicRangeType != "" || atmos.Resolution() != "2160p" || len(atmos.Subtitles) != 28 {
		t.Errorf("Atmos movie: codec %q ch %q depth %d hdr %q res %q subs %d", atmos.AudioCodec, atmos.ChannelsText(), atmos.VideoBitDepth, atmos.VideoDynamicRangeType, atmos.Resolution(), len(atmos.Subtitles))
	}

	ep := fixture(t, "episode-cm.json")
	if ep.VideoCodec != "x265" || ep.VideoBitDepth != 10 || ep.Resolution() != "1080p" || ep.AudioCodec != "EAC3" || ep.ChannelsText() != "5.1" || ep.AudioBitrate != 640000 || len(ep.AudioLanguages) != 0 {
		t.Errorf("x265 episode: %+v", ep)
	}
	if ep.Tokens()["MediaInfo AudioLanguagesAll"] != "" || ep.Tokens()["MediaInfo Simple"] != "x265 EAC3" {
		t.Errorf("episode tokens: %v", ep.Tokens())
	}

	flac := fixture(t, "track-flac.json")
	if flac.VideoCodec != "" || flac.AudioCodec != "FLAC" || flac.AudioBitsPerSample != 24 || flac.AudioSampleRate != 44100 || flac.TrackSummary() != "FLAC · 24-bit · 44.1 kHz" || flac.Container != "FLAC" {
		t.Errorf("FLAC: %+v summary %q", flac, flac.TrackSummary())
	}
	mp3 := fixture(t, "track-mp3.json")
	if mp3.VideoCodec != "" || mp3.AudioCodec != "MP3" || mp3.AudioBitrate != 320000 || mp3.TrackSummary() != "MP3 · 320 kbps · 44.1 kHz" || mp3.Tokens()["MediaInfo AudioBitRate"] != "320 kbps" {
		t.Errorf("MP3: %+v summary %q", mp3, mp3.TrackSummary())
	}

	stored := Decode(dv.Encode())
	if !reflect.DeepEqual(stored, dv) || !stored.Analyzed() {
		t.Error("want Encode/Decode to round-trip")
	}
	if Decode("{}").Analyzed() || Decode("").Analyzed() {
		t.Error("an empty column isn't analyzed")
	}
}

func TestParseFFprobe_HDRKindsCoverArtAndErrors(t *testing.T) {
	stream := func(transfer, sideData string) string {
		sd := ""
		if sideData != "" {
			sd = `,"side_data_list":[{"side_data_type":"` + sideData + `"}]`
		}
		return `{"streams":[{"codec_type":"video","codec_name":"hevc","width":3840,"height":2160,"pix_fmt":"yuv420p10le","color_transfer":"` + transfer + `"` + sd + `}],"format":{"format_name":"matroska,webm"}}`
	}
	for _, c := range []struct{ transfer, side, want string }{
		{"smpte2084", "", "HDR10"}, {"smpte2084", "DOVI configuration record", "DV HDR10"}, {"arib-std-b67", "", "HLG"}, {"bt709", "", ""},
	} {
		info, err := ParseFFprobe([]byte(stream(c.transfer, c.side)))
		if err != nil || info.VideoDynamicRangeType != c.want || (c.want != "") != (info.VideoDynamicRange == "HDR") {
			t.Errorf("%s + %q: got %q (%v)", c.transfer, c.side, info.VideoDynamicRangeType, err)
		}
	}
	art := `{"streams":[{"codec_type":"audio","codec_name":"mp3","channels":2,"sample_rate":"44100","bit_rate":"192000"},{"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}],"format":{"format_name":"mp3"}}`
	if info, _ := ParseFFprobe([]byte(art)); info.VideoCodec != "" || info.AudioCodec != "MP3" {
		t.Errorf("cover art must not count as video: %+v", info)
	}
	if _, err := ParseFFprobe([]byte(`{"streams":[{"codec_type":"data"}],"format":{}}`)); err != ErrNoStreams {
		t.Errorf("want ErrNoStreams, got %v", err)
	}
	if _, err := ParseFFprobe([]byte(`not json`)); err == nil {
		t.Error("want bad JSON refused")
	}
}

func TestFormatting(t *testing.T) {
	for _, c := range [][3]string{
		{"dts", "DTS-HD MA + DTS:X", "DTS-X"}, {"dts", "DTS-HD HRA", "DTS-HD HRA"}, {"dts", "DTS-ES", "DTS-ES"}, {"dts", "", "DTS"},
		{"dca", "ma", "DTS-HD MA"}, {"truehd", "Dolby TrueHD + Dolby Atmos", "TrueHD Atmos"}, {"aac", "HE-AAC", "HE-AAC"}, {"aac", "LC", "AAC"},
		{"pcm_s24le", "", "PCM"}, {"opus", "", "Opus"}, {"ac3", "", "AC3"},
	} {
		if got := FormatAudioCodec(c[0], c[1]); got != c[2] {
			t.Errorf("audio %s/%s = %q, want %q", c[0], c[1], got, c[2])
		}
	}
	for _, c := range [][4]string{
		{"h264", "", "x264 core 164", "x264"}, {"h264", "", "", "h264"}, {"mpeg4", "XVID", "", "XviD"}, {"av1", "", "", "AV1"}, {"hevc", "", "lavc libx265", "x265"},
	} {
		if got := FormatVideoCodec(c[0], c[1], c[2]); got != c[3] {
			t.Errorf("video %v = %q", c, got)
		}
	}
	if Channels(8, "7.1") != 7.1 || Channels(6, "5.1(side)") != 5.1 || Channels(1, "mono") != 1 || Channels(6, "") != 5.1 {
		t.Error("channel layouts")
	}
	if (Info{Width: 1920, Height: 800}).Resolution() != "1080p" || (Info{Width: 1280, Height: 536}).Resolution() != "720p" || (Info{Width: 720, Height: 404}).Resolution() != "480p" {
		t.Error("resolution tiers")
	}
	if LanguageName("ger") != "German" || LanguageShort("deu") != "DE" || normalizeLanguage("en") != "eng" || normalizeLanguage("und") != "" || LanguageNames([]string{"eng", "fre", "fra"}) != "English, French" {
		t.Error("languages")
	}
}

func TestProber(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "ffprobe-good")
	abs, _ := filepath.Abs(filepath.Join("testdata", "episode-cm.json"))
	os.WriteFile(good, []byte("#!/bin/sh\ncat '"+abs+"'\n"), 0o755)
	info, err := (&Prober{Path: good}).Probe(context.Background(), "/anything.mkv")
	if err != nil || info.VideoCodec != "x265" {
		t.Fatalf("want the fixture parsed, got %+v (%v)", info, err)
	}
	bad := filepath.Join(dir, "ffprobe-bad")
	os.WriteFile(bad, []byte("#!/bin/sh\necho '/x.mkv: Invalid data found when processing input' >&2\nexit 1\n"), 0o755)
	if _, err := (&Prober{Path: bad}).Probe(context.Background(), "/x.mkv"); err == nil || !strings.Contains(err.Error(), "Invalid data found") {
		t.Fatalf("want FFprobe's own message, got %v", err)
	}
}

func TestPlexLibrary(t *testing.T) {
	var sawToken bool
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		sawToken = true
		switch {
		case r.URL.Path == "/library/sections":
			w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"1","type":"movie","title":"Movies"},{"key":"2","type":"show","title":"TV"},{"key":"3","type":"artist","title":"Music"},{"key":"4","type":"photo","title":"Photos"}]}}`))
		case r.URL.Path == "/library/sections/1/all" && r.URL.Query().Get("type") == "1":
			pages++
			if r.Header.Get("X-Plex-Container-Start") == "0" {
				w.Write([]byte(`{"MediaContainer":{"size":1,"totalSize":2,"Metadata":[{"Media":[{"duration":5052454,"bitrate":26000,"width":3840,"height":2076,"audioChannels":2,"audioCodec":"dca","audioProfile":"ma","videoCodec":"hevc","videoProfile":"main 10","videoFrameRate":"24p","container":"mkv","Part":[{"file":"/data/Movies/Airplane II (1982)/Airplane II.mkv"}]}]}]}}`))
				return
			}
			w.Write([]byte(`{"MediaContainer":{"size":1,"totalSize":2,"Metadata":[{"Media":[{"duration":"6069439","bitrate":"15000","width":"1920","height":"1080","audioChannels":"6","audioCodec":"eac3","videoCodec":"h264","container":"mp4","Part":[{"file":"/data/Movies/Other (2020)/Other.mp4"}]}]}]}}`))
		case r.URL.Path == "/library/sections/2/all" && r.URL.Query().Get("type") == "4":
			w.Write([]byte(`{"MediaContainer":{"size":1,"totalSize":1,"Metadata":[{"Media":[{"audioCodec":"eac3","audioChannels":6,"videoCodec":"hevc","width":2160,"height":1080,"Part":[{"file":"/data/TV/Current/Criminal Minds/Season 19/x.mkv"}]}]}]}}`))
		case r.URL.Path == "/library/sections/3/all" && r.URL.Query().Get("type") == "10":
			w.Write([]byte(`{"MediaContainer":{"size":1,"totalSize":1,"Metadata":[{"Media":[{"audioCodec":"mp3","audioChannels":2,"bitrate":320,"container":"mp3","Part":[{"file":"/data/Music/A/B/01.mp3"}]}]}]}}`))
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	files, sections, err := (&Plex{BaseURL: srv.URL + "/", Token: "secret", PageSize: 1}).Library(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !sawToken || len(sections) != 3 || len(files) != 4 || pages != 2 {
		t.Fatalf("want 3 libraries, 4 files over 2 movie pages, got %d libraries %d files %d pages", len(sections), len(files), pages)
	}
	m := files["/data/Movies/Airplane II (1982)/Airplane II.mkv"]
	if m.Source != SourcePlex || m.VideoCodec != "h265" || m.VideoBitDepth != 10 || m.AudioCodec != "DTS-HD MA" || m.ChannelsText() != "2.0" || m.Resolution() != "2160p" || m.VideoFPS != 24 || m.RunTimeSeconds != 5052.454 {
		t.Errorf("Plex movie: %+v", m)
	}
	if o := files["/data/Movies/Other (2020)/Other.mp4"]; o.Width != 1920 || o.ChannelsText() != "5.1" || o.Container != "MP4" {
		t.Errorf("string-typed numbers: %+v", o)
	}
	if tr := files["/data/Music/A/B/01.mp3"]; tr.VideoCodec != "" || tr.AudioCodec != "MP3" || tr.TrackSummary() != "MP3 · 320 kbps" {
		t.Errorf("Plex track: %+v %q", tr, tr.TrackSummary())
	}
	if _, _, err := (&Plex{BaseURL: srv.URL, Token: "wrong"}).Library(context.Background()); err == nil || !strings.Contains(err.Error(), "token was refused") {
		t.Errorf("want a refused token reported, got %v", err)
	}
	if _, _, err := (&Plex{}).Library(context.Background()); err == nil || !strings.Contains(err.Error(), "Settings → Connect → Plex") {
		t.Errorf("want the missing connection explained, got %v", err)
	}
}
