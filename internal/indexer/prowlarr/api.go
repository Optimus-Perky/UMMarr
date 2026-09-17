// Package prowlarr reads a Prowlarr instance's indexers, so UMMarr can turn
// its old single Prowlarr connection into one Torznab/Newznab indexer per
// Prowlarr indexer - the same entries Prowlarr's own app sync creates.
package prowlarr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrMissingCredential is returned when the base URL or API key is empty.
var ErrMissingCredential = errors.New("prowlarr: base URL and API key are required")

// Client reads Prowlarr's v1 API.
type Client struct {
	baseURL   string
	apiKey    string
	userAgent string
	http      *http.Client
}

// Options configures a Client; HTTP is a test override.
type Options struct {
	BaseURL   string
	APIKey    string
	UserAgent string
	HTTP      *http.Client
}

// New builds a client. It does no I/O.
func New(opts Options) *Client {
	httpClient := opts.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(opts.BaseURL, "/"), apiKey: opts.APIKey, userAgent: opts.UserAgent, http: httpClient}
}

// BaseURL is the Prowlarr address the client talks to.
func (c *Client) BaseURL() string { return c.baseURL }

type category struct {
	ID            int        `json:"id"`
	Name          string     `json:"name"`
	SubCategories []category `json:"subCategories"`
}

// Indexer is one indexer as Prowlarr's GET /api/v1/indexer describes it.
type Indexer struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Enable       bool   `json:"enable"`
	Protocol     string `json:"protocol"` // "torrent" or "usenet"
	Priority     int    `json:"priority"`
	AppProfileID int    `json:"appProfileId"`
	Capabilities struct {
		Categories []category `json:"categories"`
	} `json:"capabilities"`
}

// CategoryIDs lists every category the indexer supports, subcategories included.
func (i Indexer) CategoryIDs() []int {
	var ids []int
	var walk func([]category)
	walk = func(cats []category) {
		for _, c := range cats {
			ids = append(ids, c.ID)
			walk(c.SubCategories)
		}
	}
	walk(i.Capabilities.Categories)
	return ids
}

// AppProfile is a Prowlarr sync profile: which searches synced indexers get.
type AppProfile struct {
	ID                      int  `json:"id"`
	EnableRss               bool `json:"enableRss"`
	EnableAutomaticSearch   bool `json:"enableAutomaticSearch"`
	EnableInteractiveSearch bool `json:"enableInteractiveSearch"`
	MinimumSeeders          int  `json:"minimumSeeders"`
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	if c.baseURL == "" || c.apiKey == "" {
		return ErrMissingCredential
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("prowlarr: %w", err)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("prowlarr: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("prowlarr: read %s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("prowlarr: %s returned HTTP %d", path, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("prowlarr: decode %s: %w", path, err)
	}
	return nil
}

// Indexers lists Prowlarr's indexers.
func (c *Client) Indexers(ctx context.Context) ([]Indexer, error) {
	var out []Indexer
	return out, c.getJSON(ctx, "/api/v1/indexer", &out)
}

// AppProfiles lists Prowlarr's sync profiles.
func (c *Client) AppProfiles(ctx context.Context) ([]AppProfile, error) {
	var out []AppProfile
	return out, c.getJSON(ctx, "/api/v1/appprofile", &out)
}
