// Package discogs talks to the Discogs API. It is here for what
// MusicBrainz is weakest at: cover art (MusicBrainz returns none at all,
// and Cover Art Archive has gaps) and the detail of a particular pressing.
//
// A token is optional. Without one the API answers 25 requests a minute;
// a personal access token - one string from a Discogs account, no OAuth
// dance - raises that to 60. Discogs requires a User-Agent that identifies
// the application either way.
package discogs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/httpclient"
)

const defaultBaseURL = "https://api.discogs.com"

// Requests per minute, as the API's own x-discogs-ratelimit header
// reports: 25 unauthenticated, 60 with a token.
const (
	anonymousPerMinute     = 25
	authenticatedPerMinute = 60
)

// ErrMissingUserAgent means no User-Agent was configured; Discogs rejects
// requests without one that identifies the application.
var ErrMissingUserAgent = fmt.Errorf("discogs: a User-Agent identifying the application is required")

// Client is one Discogs API client.
type Client struct {
	http  httpclient.Client
	token string
}

// Options configures a Client.
type Options struct {
	// Token is a personal access token. Optional: without it the API still
	// answers, at a lower rate.
	Token     string
	UserAgent string
	BaseURL   string
}

// New builds a client rate-limited to whatever the token allows.
func New(opts Options) (*Client, error) {
	if strings.TrimSpace(opts.UserAgent) == "" {
		return nil, ErrMissingUserAgent
	}
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	perMinute := anonymousPerMinute
	if opts.Token != "" {
		perMinute = authenticatedPerMinute
	}
	return &Client{
		token: strings.TrimSpace(opts.Token),
		http: httpclient.Client{
			BaseURL:   baseURL,
			UserAgent: opts.UserAgent,
			// Spread requests out rather than bursting the whole minute's
			// allowance: a burst is what gets a client throttled.
			Limiter:    rate.NewLimiter(rate.Every(time.Minute/time.Duration(perMinute)), 2),
			MaxRetries: 2,
		},
	}, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	if query == nil {
		query = url.Values{}
	}
	if c.token != "" {
		query.Set("token", c.token)
	}
	body, err := c.http.Get(ctx, path, query, nil)
	if err != nil {
		return fmt.Errorf("discogs: %w", err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("discogs: decode %s: %w", path, err)
	}
	return nil
}

// Image is one image of a release. Type is "primary" for the front cover.
type Image struct {
	Type   string `json:"type"`
	URI    string `json:"uri"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// SearchResult is one hit from a release search.
type SearchResult struct {
	ID          int      `json:"id"`
	MasterID    int      `json:"master_id"`
	Title       string   `json:"title"` // "Artist - Album"
	Year        string   `json:"year"`
	Country     string   `json:"country"`
	Format      []string `json:"format"`
	CoverImage  string   `json:"cover_image"`
	Thumb       string   `json:"thumb"`
	ResourceURL string   `json:"resource_url"`
}

type searchResponse struct {
	Results []SearchResult `json:"results"`
}

// SearchRelease finds releases by artist and album title.
func (c *Client) SearchRelease(ctx context.Context, artist, album string) ([]SearchResult, error) {
	query := url.Values{
		"type":          {"release"},
		"artist":        {artist},
		"release_title": {album},
		"per_page":      {"10"},
	}
	var out searchResponse
	if err := c.get(ctx, "/database/search", query, &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

// Release is one pressing, with the detail MusicBrainz is thin on.
type Release struct {
	ID          int      `json:"id"`
	Title       string   `json:"title"`
	Year        int      `json:"year"`
	Country     string   `json:"country"`
	Released    string   `json:"released"`
	MasterID    int      `json:"master_id"`
	Images      []Image  `json:"images"`
	Genres      []string `json:"genres"`
	Styles      []string `json:"styles"`
	Formats     []Format `json:"formats"`
	Tracklist   []Track  `json:"tracklist"`
	Notes       string   `json:"notes"`
	ResourceURL string   `json:"resource_url"`
}

// Format is a release's medium: CD, Vinyl, with its descriptions.
type Format struct {
	Name         string   `json:"name"`
	Quantity     string   `json:"qty"`
	Descriptions []string `json:"descriptions"`
}

// Track is one entry of a release's tracklist. Position is "A1" on a
// vinyl, "1" on a CD.
type Track struct {
	Position string `json:"position"`
	Title    string `json:"title"`
	Duration string `json:"duration"`
	Type     string `json:"type_"` // "track" or "heading"
}

// GetRelease reads one release.
func (c *Client) GetRelease(ctx context.Context, id int) (*Release, error) {
	var release Release
	if err := c.get(ctx, "/releases/"+strconv.Itoa(id), nil, &release); err != nil {
		return nil, err
	}
	return &release, nil
}

// FrontCover is the URL of the release's front image, or "" when it has
// none. Discogs marks it "primary"; failing that the first image is the
// front in practice.
func (r *Release) FrontCover() string {
	for _, image := range r.Images {
		if image.Type == "primary" && image.URI != "" {
			return image.URI
		}
	}
	for _, image := range r.Images {
		if image.URI != "" {
			return image.URI
		}
	}
	return ""
}

// TrackCount counts real tracks, ignoring the headings Discogs uses to
// label a side or a disc.
func (r *Release) TrackCount() int {
	n := 0
	for _, t := range r.Tracklist {
		if t.Type == "" || t.Type == "track" {
			n++
		}
	}
	return n
}

// Test checks the API answers, and that a token (when set) is accepted.
func (c *Client) Test(ctx context.Context) error {
	var out struct {
		ID int `json:"id"`
	}
	// A release that has existed since the site began: cheap and stable.
	return c.get(ctx, "/releases/1", nil, &out)
}
