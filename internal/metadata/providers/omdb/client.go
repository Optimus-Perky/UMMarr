// Package omdb is a client for the Open Movie Database API (omdbapi.com),
// used as a supplementary movie source alongside TMDB - its main value is
// Rotten Tomatoes/Metacritic ratings TMDB doesn't have. Requires a free API
// key the user obtains themselves (1,000 requests/day cap) - see
// internal/config.
package omdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"golang.org/x/time/rate"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/httpclient"
)

const defaultBaseURL = "https://www.omdbapi.com"

// ErrMissingCredential is returned by any call made without an API key set.
var ErrMissingCredential = errors.New("omdb: no API key configured (set UMMARR_OMDB_API_KEY)")

// ErrNotFound is returned when OMDb reports Response:"False" for a lookup
// that isn't otherwise an HTTP error (that's how OMDb encodes "not found").
var ErrNotFound = errors.New("omdb: not found")

// Client talks to the OMDb API.
type Client struct {
	http   httpclient.Client
	apiKey string
}

// Options configures a Client. BaseURL and HTTP are only overridden in
// tests, to point at an httptest.Server instead of the real API.
type Options struct {
	APIKey    string
	UserAgent string
	BaseURL   string
	HTTP      *http.Client
}

// New builds an OMDb client. The 1,000/day cap is a daily quota, not a
// burst limit, so the limiter here just smooths request bursts.
func New(opts Options) *Client {
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		apiKey: opts.APIKey,
		http: httpclient.Client{
			HTTP:       opts.HTTP,
			BaseURL:    baseURL,
			UserAgent:  opts.UserAgent,
			Limiter:    rate.NewLimiter(rate.Limit(5), 3),
			MaxRetries: 3,
		},
	}
}

func (c *Client) get(ctx context.Context, query url.Values) (*Response, error) {
	if c.apiKey == "" {
		return nil, ErrMissingCredential
	}
	query.Set("apikey", c.apiKey)
	body, err := c.http.Get(ctx, "/", query, nil)
	if err != nil {
		return nil, fmt.Errorf("omdb: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("omdb: decode response: %w", err)
	}
	if resp.Response == "False" {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, resp.Error)
	}
	return &resp, nil
}

// GetByIMDbID looks up a movie by its IMDb id (e.g. "tt1375666").
func (c *Client) GetByIMDbID(ctx context.Context, imdbID string) (*Response, error) {
	return c.get(ctx, url.Values{"i": {imdbID}})
}

// GetByTitle looks up a movie by title, optionally narrowed by year (pass
// "" to omit).
func (c *Client) GetByTitle(ctx context.Context, title, year string) (*Response, error) {
	query := url.Values{"t": {title}}
	if year != "" {
		query.Set("y", year)
	}
	return c.get(ctx, query)
}
