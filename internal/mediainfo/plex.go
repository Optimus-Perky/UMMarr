package mediainfo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Plex reads media information Plex has already worked out, for every file
// in its movie, TV and music libraries, instead of opening the files again.
type Plex struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	// PageSize is how many items each library request asks for.
	PageSize int
}

// flexInt reads a Plex number whether it arrives as a number or a string.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*f = 0
		return nil
	}
	*f = flexInt(n)
	return nil
}

type plexMedia struct {
	Duration       flexInt `json:"duration"`
	Bitrate        flexInt `json:"bitrate"`
	Width          flexInt `json:"width"`
	Height         flexInt `json:"height"`
	AudioChannels  flexInt `json:"audioChannels"`
	AudioCodec     string  `json:"audioCodec"`
	AudioProfile   string  `json:"audioProfile"`
	VideoCodec     string  `json:"videoCodec"`
	VideoProfile   string  `json:"videoProfile"`
	VideoFrameRate string  `json:"videoFrameRate"`
	Container      string  `json:"container"`
	Part           []struct {
		File string `json:"file"`
	} `json:"Part"`
}

// PlexSection is one Plex library.
type PlexSection struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

func (p *Plex) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

func (p *Plex) get(ctx context.Context, path string, start, size int, into any) error {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" || p.Token == "" {
		return fmt.Errorf("plex: no server URL or token in Settings → Connect → Plex")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Token", p.Token)
	if size > 0 {
		req.Header.Set("X-Plex-Container-Start", strconv.Itoa(start))
		req.Header.Set("X-Plex-Container-Size", strconv.Itoa(size))
	}
	resp, err := p.client().Do(req)
	if err != nil {
		// The URL can carry the token in some setups; say only what failed.
		return fmt.Errorf("plex: can't reach the server: %v", unwrapURLError(err))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("plex: the token was refused")
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("plex: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("plex: bad reply: %w", err)
	}
	return nil
}

func unwrapURLError(err error) error {
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok && u.Unwrap() != nil {
		return u.Unwrap()
	}
	return err
}

// Sections lists Plex's libraries.
func (p *Plex) Sections(ctx context.Context) ([]PlexSection, error) {
	var out struct {
		MediaContainer struct {
			Directory []PlexSection `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if err := p.get(ctx, "/library/sections", 0, 0, &out); err != nil {
		return nil, err
	}
	return out.MediaContainer.Directory, nil
}

// plexItemType is the Plex item type holding files in each kind of library:
// movies, episodes and tracks.
var plexItemType = map[string]int{"movie": 1, "show": 4, "artist": 10}

// Library reads every movie, episode and track file Plex knows, keyed by
// the file's path as Plex sees it.
func (p *Plex) Library(ctx context.Context) (map[string]Info, []PlexSection, error) {
	sections, err := p.Sections(ctx)
	if err != nil {
		return nil, nil, err
	}
	size := p.PageSize
	if size <= 0 {
		size = 500
	}
	now := time.Now().UTC()
	files := map[string]Info{}
	var used []PlexSection
	for _, sec := range sections {
		itemType, ok := plexItemType[sec.Type]
		if !ok {
			continue
		}
		used = append(used, sec)
		for start := 0; ; start += size {
			var page struct {
				MediaContainer struct {
					Size      int `json:"size"`
					TotalSize int `json:"totalSize"`
					Metadata  []struct {
						Media []plexMedia `json:"Media"`
					} `json:"Metadata"`
				} `json:"MediaContainer"`
			}
			path := fmt.Sprintf("/library/sections/%s/all?type=%d", sec.Key, itemType)
			if err := p.get(ctx, path, start, size, &page); err != nil {
				return nil, nil, fmt.Errorf("%s library: %w", sec.Title, err)
			}
			for _, item := range page.MediaContainer.Metadata {
				for _, m := range item.Media {
					info := m.info(now, sec.Type == "artist")
					for _, part := range m.Part {
						if part.File != "" {
							files[filepath.Clean(part.File)] = info
						}
					}
				}
			}
			got := len(page.MediaContainer.Metadata)
			if got == 0 || got < size || (page.MediaContainer.TotalSize > 0 && start+got >= page.MediaContainer.TotalSize) {
				break
			}
		}
	}
	return files, used, nil
}

func (m plexMedia) info(at time.Time, music bool) Info {
	i := Info{Schema: Schema, Source: SourcePlex, AnalyzedAt: at}
	i.Container = strings.ToUpper(m.Container)
	if i.Container == "MATROSKA" {
		i.Container = "MKV"
	}
	i.RunTimeSeconds = float64(m.Duration) / 1000
	i.OverallBitrate = int64(m.Bitrate) * 1000
	if !music && m.VideoCodec != "" {
		i.VideoFormat = m.VideoCodec
		i.VideoProfile = m.VideoProfile
		i.VideoCodec = FormatVideoCodec(m.VideoCodec, "", "")
		i.Width, i.Height = int(m.Width), int(m.Height)
		switch profile := strings.ToLower(m.VideoProfile); {
		case strings.Contains(profile, "12"):
			i.VideoBitDepth = 12
		case strings.Contains(profile, "10"):
			i.VideoBitDepth = 10
		default:
			i.VideoBitDepth = 8
		}
		i.VideoFPS = plexFrameRate(m.VideoFrameRate)
	}
	if m.AudioCodec != "" {
		i.AudioFormat = plexAudioFormat(m.AudioCodec)
		i.AudioProfile = m.AudioProfile
		i.AudioCodec = FormatAudioCodec(m.AudioCodec, m.AudioProfile)
		i.AudioChannels = Channels(int(m.AudioChannels), "")
		i.AudioStreamCount = 1
		if music {
			i.AudioBitrate = i.OverallBitrate
		}
	}
	return i
}

func plexAudioFormat(codec string) string {
	switch strings.ToLower(codec) {
	case "dca", "dca-ma":
		return "dts"
	}
	return strings.ToLower(codec)
}

func plexFrameRate(rate string) float64 {
	switch strings.ToUpper(rate) {
	case "NTSC":
		return 29.97
	case "PAL":
		return 25
	}
	f, _ := strconv.ParseFloat(strings.TrimRight(strings.ToLower(rate), "pi"), 64)
	return f
}
