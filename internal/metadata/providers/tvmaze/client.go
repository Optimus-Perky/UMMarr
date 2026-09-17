// Package tvmaze is a client for the TVMaze API (api.tvmaze.com) - fully
// free, no API key or registration needed. Used both as a TV metadata
// source in its own right and as a free cross-referencing bridge to IMDb
// (via its "externals" object) without needing a TVDB subscription.
package tvmaze

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/time/rate"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/httpclient"
)

const defaultBaseURL = "https://api.tvmaze.com"

// Client talks to the TVMaze API.
type Client struct {
	http httpclient.Client
}

// Options configures a Client. BaseURL and HTTP are only overridden in
// tests, to point at an httptest.Server instead of the real API.
type Options struct {
	UserAgent string
	BaseURL   string
	HTTP      *http.Client
}

// New builds a TVMaze client, rate-limited to stay under the documented
// ~20 requests/10s per IP.
func New(opts Options) *Client {
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		http: httpclient.Client{
			HTTP:       opts.HTTP,
			BaseURL:    baseURL,
			UserAgent:  opts.UserAgent,
			Limiter:    rate.NewLimiter(rate.Every(500*time.Millisecond), 2),
			MaxRetries: 3,
		},
	}
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	body, err := c.http.Get(ctx, path, query, nil)
	if err != nil {
		return fmt.Errorf("tvmaze: %w", err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("tvmaze: decode %s: %w", path, err)
	}
	return nil
}

// SearchShows searches by name, returning fuzzy matches ranked by score.
func (c *Client) SearchShows(ctx context.Context, name string) ([]SearchResult, error) {
	var results []SearchResult
	if err := c.get(ctx, "/search/shows", url.Values{"q": {name}}, &results); err != nil {
		return nil, err
	}
	return results, nil
}

// GetShow fetches one show by its TVMaze id.
func (c *Client) GetShow(ctx context.Context, id int) (*Show, error) {
	var show Show
	if err := c.get(ctx, fmt.Sprintf("/shows/%d", id), nil, &show); err != nil {
		return nil, err
	}
	return &show, nil
}

// GetEpisodes fetches a show's full flat episode list. Unlike TMDB,
// TVMaze has no season entity of its own - each episode just carries its
// own Season number, which the merge layer unions against TMDB's explicit
// seasons (see internal/metadata/merge/seasons.go).
func (c *Client) GetEpisodes(ctx context.Context, showID int) ([]Episode, error) {
	var episodes []Episode
	if err := c.get(ctx, fmt.Sprintf("/shows/%d/episodes", showID), nil, &episodes); err != nil {
		return nil, err
	}
	return episodes, nil
}

// LookupByIMDb finds a show by its IMDb id (e.g. "tt0944947"), useful for
// cross-referencing once another provider has already resolved one.
func (c *Client) LookupByIMDb(ctx context.Context, imdbID string) (*Show, error) {
	var show Show
	if err := c.get(ctx, "/lookup/shows", url.Values{"imdb": {imdbID}}, &show); err != nil {
		return nil, err
	}
	return &show, nil
}

// ParseAirdate parses TVMaze's "YYYY-MM-DD" date strings, returning nil
// for an empty string rather than an error.
func ParseAirdate(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &t
}
