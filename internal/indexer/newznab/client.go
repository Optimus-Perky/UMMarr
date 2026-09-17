// Package newznab talks to Torznab and Newznab indexers, the generic indexer
// types Radarr, Sonarr and Lidarr use - including each indexer Prowlarr
// exposes at {prowlarr}/{id}/api.
package newznab

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Implementations.
const (
	Torznab = "Torznab"
	Newznab = "Newznab"
)

// PageSize is how many results one request asks for, as in Radarr.
const PageSize = 100

// maxBody caps how much of a response is read.
const maxBody = 32 << 20

// Settings is how to reach one indexer.
type Settings struct {
	Implementation       string
	BaseURL              string
	APIPath              string
	APIKey               string
	AdditionalParameters string // e.g. "&foo=bar", appended as typed
}

// Protocol is the download protocol the implementation serves.
func (s Settings) Protocol() string {
	if s.Implementation == Newznab {
		return ProtocolUsenet
	}
	return ProtocolTorrent
}

// Client queries one indexer. It holds no connection state, so building one
// per request is cheap.
type Client struct {
	settings  Settings
	http      *http.Client
	raw       *http.Client
	userAgent string
}

// Options are test and deployment overrides.
type Options struct {
	UserAgent string
	HTTP      *http.Client
}

// New builds a client for s.
func New(s Settings, opts Options) *Client {
	httpClient := opts.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 90 * time.Second}
	}
	// FetchRelease reads redirects itself: Prowlarr's download links often
	// redirect straight to a magnet: URI, which the transport can't follow.
	raw := *httpClient
	raw.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{settings: s, http: httpClient, raw: &raw, userAgent: opts.UserAgent}
}

// apiURL builds the request URL for params, in Radarr's shape:
// {base}{apiPath}?t=...&cat=...&extended=1{additional}&apikey=...
func (c *Client) apiURL(params url.Values) string {
	apiPath := c.settings.APIPath
	if apiPath == "" {
		apiPath = "/api"
	}
	u := strings.TrimRight(c.settings.BaseURL, "/") + "/" + strings.Trim(apiPath, "/")
	q := params.Encode()
	if c.settings.AdditionalParameters != "" {
		q += c.settings.AdditionalParameters
	}
	if c.settings.APIKey != "" {
		q += "&apikey=" + url.QueryEscape(c.settings.APIKey)
	}
	return u + "?" + strings.TrimPrefix(q, "&")
}

// redact turns a transport error into a short message without the request
// URL (which carries the API key), and removes the key from anything else
// that might quote it.
func (c *Client) redact(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = fmt.Errorf("couldn't reach the indexer: %w", urlErr.Err)
	}
	if c.settings.APIKey == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, c.settings.APIKey) && !strings.Contains(msg, url.QueryEscape(c.settings.APIKey)) {
		return err
	}
	msg = strings.ReplaceAll(msg, url.QueryEscape(c.settings.APIKey), "(apikey)")
	return errors.New(strings.ReplaceAll(msg, c.settings.APIKey, "(apikey)"))
}

func (c *Client) get(ctx context.Context, params url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL(params), nil)
	if err != nil {
		return nil, c.redact(err)
	}
	req.Header.Set("Accept", "application/rss+xml, application/xml, text/xml")
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.redact(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, c.redact(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if apiErr := checkError(body); apiErr != nil {
			return nil, apiErr
		}
		return nil, fmt.Errorf("indexer returned HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// Caps fetches what the indexer supports.
func (c *Client) Caps(ctx context.Context) (Caps, error) {
	body, err := c.get(ctx, url.Values{"t": {"caps"}})
	if err != nil {
		return Caps{}, err
	}
	return ParseCaps(body)
}

// Query is one search request.
type Query struct {
	Mode       string // "search", "tvsearch", "movie" or "music"
	Params     url.Values
	Categories []int
}

// Search runs q and returns the releases, newest page only (PageSize items).
func (c *Client) Search(ctx context.Context, q Query) ([]Release, error) {
	if len(q.Categories) == 0 {
		return nil, nil
	}
	params := url.Values{}
	for k, v := range q.Params {
		params[k] = v
	}
	mode := q.Mode
	if mode == "" {
		mode = "search"
	}
	params.Set("t", mode)
	cats := make([]string, 0, len(q.Categories))
	seen := map[int]bool{}
	for _, cat := range q.Categories {
		if !seen[cat] {
			seen[cat] = true
			cats = append(cats, fmt.Sprint(cat))
		}
	}
	params.Set("cat", strings.Join(cats, ","))
	params.Set("extended", "1")
	params.Set("offset", "0")
	params.Set("limit", fmt.Sprint(PageSize))
	body, err := c.get(ctx, params)
	if err != nil {
		return nil, err
	}
	return ParseFeed(body, c.settings.Protocol())
}

// Recent fetches the newest releases in categories - the RSS feed Radarr's
// RSS sync reads.
func (c *Client) Recent(ctx context.Context, categories []int) ([]Release, error) {
	return c.Search(ctx, Query{Mode: "search", Categories: categories})
}

// Kinds of fetched release.
const (
	KindMagnet = iota
	KindTorrentFile
	KindNZB
)

// FetchedRelease is a release's download, ready for a download client.
type FetchedRelease struct {
	Kind      int
	MagnetURI string
	FileName  string
	Data      []byte
}

// FetchRelease resolves r's download link. A magnet needs no request; a
// link either redirects to a magnet or returns the .torrent/.nzb file.
func (c *Client) FetchRelease(ctx context.Context, r Release) (*FetchedRelease, error) {
	if r.MagnetURL != "" && (r.DownloadURL == "" || r.Protocol == ProtocolTorrent) {
		return &FetchedRelease{Kind: KindMagnet, MagnetURI: r.MagnetURL}, nil
	}
	if r.DownloadURL == "" {
		return nil, fmt.Errorf("release %q has neither a magnet nor a download link", r.Title)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.DownloadURL, nil)
	if err != nil {
		return nil, c.redact(err)
	}
	if c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	resp, err := c.raw.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch release: %w", c.redact(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		location := resp.Header.Get("Location")
		if strings.HasPrefix(location, "magnet:") {
			return &FetchedRelease{Kind: KindMagnet, MagnetURI: location}, nil
		}
		return nil, fmt.Errorf("fetch release: the indexer redirected somewhere other than a magnet link")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("fetch release: %w", c.redact(err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if apiErr := checkError(data); apiErr != nil {
			return nil, apiErr
		}
		return nil, fmt.Errorf("fetch release: indexer returned HTTP %d", resp.StatusCode)
	}
	if strings.HasPrefix(string(data), "magnet:") {
		return &FetchedRelease{Kind: KindMagnet, MagnetURI: strings.TrimSpace(string(data))}, nil
	}
	if apiErr := checkError(data); apiErr != nil {
		return nil, apiErr
	}
	if r.Protocol == ProtocolUsenet {
		return &FetchedRelease{Kind: KindNZB, FileName: fileName(r.Title, ".nzb"), Data: data}, nil
	}
	return &FetchedRelease{Kind: KindTorrentFile, FileName: fileName(r.Title, ".torrent"), Data: data}, nil
}

func fileName(title, ext string) string {
	if title == "" {
		return fmt.Sprintf("release-%d%s", time.Now().UnixNano(), ext)
	}
	return title + ext
}
