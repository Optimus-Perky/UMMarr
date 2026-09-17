package api

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// mediaInfoView is a file's media information as the pages show it.
type mediaInfoView struct {
	Analyzed     bool
	VideoCodec   string // x265
	Video        string // x265 · 10-bit · DV HDR10
	Audio        string // TrueHD Atmos 7.1 +1 more
	AudioShort   string // TrueHD Atmos 7.1
	DynamicRange string // DV HDR10
	Languages    string // English, German
	Subtitles    string // every subtitle language, for the expanded list
	// A long subtitle list is shown as "English +29", expandable.
	SubtitlesFirst string
	SubtitlesMore  int
	Details        string // 3840x2076 · 23.976 fps · 1h 24m · MKV · read by FFprobe 15 Sep 2026
	Note           string // not analyzed yet, or why it couldn't be read
}

func newMediaInfoView(info mediainfo.Info) mediaInfoView {
	switch {
	case info.Schema == 0:
		return mediaInfoView{Note: "Media info: not analyzed yet."}
	case info.Error != "":
		return mediaInfoView{Note: "Media info: the file couldn't be read (" + info.Error + ")."}
	}
	v := mediaInfoView{
		Analyzed: true, VideoCodec: info.VideoCodec, Video: info.VideoSummary(), Audio: info.AudioSummary(),
		AudioShort: strings.TrimSpace(info.AudioCodec + " " + info.ChannelsText()), DynamicRange: info.VideoDynamicRangeType,
		Languages: mediainfo.LanguageNames(info.AudioLanguages), Subtitles: mediainfo.LanguageNames(info.Subtitles),
	}
	v.SubtitlesFirst, v.SubtitlesMore = firstLanguageAndRest(info.Subtitles)
	var details []string
	add := func(s string) {
		if s != "" {
			details = append(details, s)
		}
	}
	add(info.ResolutionText())
	if info.VideoFPS > 0 {
		add(strconv.FormatFloat(info.VideoFPS, 'f', -1, 64) + " fps")
	}
	if bitrate := info.VideoBitrate; bitrate > 0 {
		add(fmt.Sprintf("%.1f Mbps", float64(bitrate)/1e6))
	}
	add(info.RuntimeText())
	add(info.Container)
	source := "FFprobe"
	if info.Source == mediainfo.SourcePlex {
		source = "Plex"
	}
	if info.AnalyzedAt.IsZero() {
		add("read by " + source)
	} else {
		add("read by " + source + " " + info.AnalyzedAt.Local().Format("2 Jan 2006"))
	}
	v.Details = strings.Join(details, " · ")
	return v
}

// fileLanguages is the audio languages the file holds, or what its name
// says when it hasn't been analyzed.
func fileLanguages(info mediainfo.Info, relativePath string) string {
	if len(info.AudioLanguages) > 0 {
		return mediainfo.LanguageNames(info.AudioLanguages)
	}
	return strings.Join(releaseparse.Languages(relativePath), ", ")
}

// setMediaAnalysisFlags fills what the Media Management tab needs to warn
// about: FFprobe missing, no Plex connection.
func (h *handler) setMediaAnalysisFlags(ctx context.Context, data *settingsPageData) {
	data.FFprobeAvailable = h.deps.MediaInfo.FFprobeAvailable()
	_, _, data.PlexConnected, _ = store.PlexConnection(ctx, h.deps.DB)
}

// TestPlexMediaInfo is Media Management's Test Plex button: how many of the
// library's files Plex can describe. Nothing is saved.
func (h *handler) TestPlexMediaInfo(w http.ResponseWriter, r *http.Request) {
	if h.deps.MediaInfo == nil {
		renderInlineError(w, "Media analysis isn't running in this UMMarr.")
		return
	}
	check, err := h.deps.MediaInfo.CheckPlex(r.Context())
	if err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<p class="indexer-test ok">%s</p>`, html.EscapeString(check.Summary()))
}

// mediaAnalysisText is System → Status's Media analysis line.
func (h *handler) mediaAnalysisText(ctx context.Context) string {
	if !h.deps.MediaInfo.FFprobeAvailable() {
		return "FFprobe isn't installed, so files aren't analyzed."
	}
	c, err := store.CountMediaInfo(ctx, h.deps.DB)
	if err != nil {
		return err.Error()
	}
	text := fmt.Sprintf("%s of %s files analyzed", groupDigits(c.Analyzed), groupDigits(c.Total))
	if c.FromPlex > 0 {
		text += fmt.Sprintf(" (%s from Plex)", groupDigits(c.FromPlex))
	}
	if c.Failed > 0 {
		text += fmt.Sprintf(" · %s couldn't be read", groupDigits(c.Failed))
	}
	st := h.deps.MediaInfo.Status()
	if st.Running {
		text += fmt.Sprintf(" · analyzing now, %s done this run", groupDigits(st.Done+st.Failed))
	}
	if st.Note != "" {
		text += " · " + st.Note
	}
	return text
}

// groupDigits writes 39423 as 39,423.
func groupDigits(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// firstLanguageAndRest is how a long subtitle list reads at a glance:
// English first when it's there, otherwise whichever came first, and how
// many others there are. One or two languages read fine as they are, so
// they aren't collapsed.
func firstLanguageAndRest(codes []string) (first string, more int) {
	names := []string{}
	for _, c := range codes {
		name := mediainfo.LanguageName(c)
		dup := false
		for _, have := range names {
			dup = dup || have == name
		}
		if !dup {
			names = append(names, name)
		}
	}
	if len(names) <= 2 {
		return "", 0
	}
	first = names[0]
	for _, name := range names {
		if name == "English" {
			first = name
			break
		}
	}
	return first, len(names) - 1
}
