// Package tvdb talks to TheTVDB's v4 API: login for a bearer token, then
// series details and episodes. A key comes from thetvdb.com/api-information;
// a subscriber key also needs that subscriber's PIN.
package tvdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is TheTVDB's v4 API.
const DefaultBaseURL = "https://api4.thetvdb.com/v4"

// Client is one TheTVDB account.
type Client struct {
	baseURL   string
	apiKey    string
	pin       string
	userAgent string
	http      *http.Client

	mu      sync.Mutex
	token   string
	expires time.Time
}

// Options configures a Client.
type Options struct {
	APIKey    string
	PIN       string
	BaseURL   string
	UserAgent string
	HTTP      *http.Client
}

// New builds a client; nothing is contacted until it's used.
func New(opts Options) (*Client, error) {
	if strings.TrimSpace(opts.APIKey) == "" {
		return nil, fmt.Errorf("thetvdb: no API key")
	}
	base := opts.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	httpClient := opts.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(base, "/"), apiKey: opts.APIKey, pin: opts.PIN, userAgent: opts.UserAgent, http: httpClient}, nil
}

// login gets a bearer token, reusing the last one until it's nearly a month
// old (TheTVDB's tokens last a month).
func (c *Client) login(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.token != "" && time.Now().Before(c.expires) {
		token := c.token
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	body, _ := json.Marshal(map[string]string{"apikey": c.apiKey, "pin": c.pin})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("thetvdb login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("thetvdb: the API key%s was refused", pinNote(c.pin))
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("thetvdb login: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Data.Token == "" {
		return "", fmt.Errorf("thetvdb login: no token in the reply")
	}
	c.mu.Lock()
	c.token, c.expires = out.Data.Token, time.Now().Add(24*time.Hour)
	c.mu.Unlock()
	return out.Data.Token, nil
}

func pinNote(pin string) string {
	if pin == "" {
		return ""
	}
	return " and PIN"
}

// get calls one endpoint and decodes its "data" into out.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	token, err := c.login(ctx)
	if err != nil {
		return err
	}
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("thetvdb %s: %w", path, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusUnauthorized:
		c.mu.Lock()
		c.token = "" // make the next call log in again
		c.mu.Unlock()
		return fmt.Errorf("thetvdb: the token was refused")
	default:
		return fmt.Errorf("thetvdb %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ErrNotFound means TheTVDB has no such series.
var ErrNotFound = fmt.Errorf("thetvdb: not found")

// SearchResult is one hit from a series search.
type SearchResult struct {
	ID       string `json:"tvdb_id"`
	Name     string `json:"name"`
	Year     string `json:"year"`
	Overview string `json:"overview"`
	Image    string `json:"image_url"`
}

// SearchSeries finds series by name.
func (c *Client) SearchSeries(ctx context.Context, query string) ([]SearchResult, error) {
	var out struct {
		Data []SearchResult `json:"data"`
	}
	if err := c.get(ctx, "/search", url.Values{"query": {query}, "type": {"series"}}, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// Series is a series as TheTVDB describes it.
type Series struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	Overview        string `json:"overview"`
	FirstAired      string `json:"firstAired"`
	LastAired       string `json:"lastAired"`
	AverageRuntime  int    `json:"averageRuntime"`
	Image           string `json:"image"`
	OriginalNetwork struct {
		Name string `json:"name"`
	} `json:"originalNetwork"`
	Status struct {
		Name string `json:"name"`
	} `json:"status"`
	Genres []struct {
		Name string `json:"name"`
	} `json:"genres"`
	RemoteIDs []struct {
		ID         string `json:"id"`
		SourceName string `json:"sourceName"`
	} `json:"remoteIds"`
}

// GetSeries reads one series' details.
func (c *Client) GetSeries(ctx context.Context, id int) (*Series, error) {
	var out struct {
		Data Series `json:"data"`
	}
	if err := c.get(ctx, "/series/"+strconv.Itoa(id)+"/extended", url.Values{"short": {"true"}}, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// Episode is one episode as TheTVDB describes it.
type Episode struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Overview     string `json:"overview"`
	Aired        string `json:"aired"`
	Runtime      int    `json:"runtime"`
	SeasonNumber int    `json:"seasonNumber"`
	Number       int    `json:"number"`
}

// GetEpisodes reads every episode of a series in its default (aired) order,
// following TheTVDB's paging.
func (c *Client) GetEpisodes(ctx context.Context, id int) ([]Episode, error) {
	var all []Episode
	for page := 0; page < 50; page++ { // 50 pages of 500 is far more than any series
		var out struct {
			Data struct {
				Episodes []Episode `json:"episodes"`
			} `json:"data"`
			Links struct {
				Next string `json:"next"`
			} `json:"links"`
		}
		if err := c.get(ctx, "/series/"+strconv.Itoa(id)+"/episodes/default", url.Values{"page": {strconv.Itoa(page)}}, &out); err != nil {
			return all, err
		}
		all = append(all, out.Data.Episodes...)
		if out.Links.Next == "" || len(out.Data.Episodes) == 0 {
			break
		}
	}
	return all, nil
}

// Test checks the key (and PIN) are accepted.
func (c *Client) Test(ctx context.Context) error {
	_, err := c.login(ctx)
	return err
}
