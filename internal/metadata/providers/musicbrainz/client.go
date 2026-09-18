// Package musicbrainz is a client for the MusicBrainz API
// (musicbrainz.org/ws/2/), UMMarr's music metadata source. No API key is
// needed for reads, but MusicBrainz enforces a strict 1 request/second
// average per IP and requires an identifying User-Agent header - both are
// handled by this client, not left to callers to get right.
package musicbrainz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/time/rate"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/httpclient"
)

const defaultBaseURL = "https://musicbrainz.org/ws/2"

// ErrMissingUserAgent is returned if a client is built without one -
// MusicBrainz blocks generic/empty User-Agents, so this fails fast at
// construction rather than surfacing as a mysterious 403 later.
var ErrMissingUserAgent = errors.New("musicbrainz: a User-Agent is required by MusicBrainz's API policy")

// Client talks to the MusicBrainz API.
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

// New builds a MusicBrainz client rate-limited to the mandatory 1
// request/second average - this is not a soft suggestion like TMDB's,
// sustained violation risks an IP ban.
func New(opts Options) (*Client, error) {
	if opts.UserAgent == "" {
		return nil, ErrMissingUserAgent
	}
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		http: httpclient.Client{
			HTTP:       opts.HTTP,
			BaseURL:    baseURL,
			UserAgent:  opts.UserAgent,
			Limiter:    rate.NewLimiter(rate.Every(time.Second), 1),
			MaxRetries: 3,
		},
	}, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	if query == nil {
		query = url.Values{}
	}
	query.Set("fmt", "json")
	body, err := c.http.Get(ctx, path, query, nil)
	if err != nil {
		return fmt.Errorf("musicbrainz: %w", err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("musicbrainz: decode %s: %w", path, err)
	}
	return nil
}

// GetArtist looks up one artist by MBID directly.
func (c *Client) GetArtist(ctx context.Context, mbid string) (*Artist, error) {
	var artist Artist
	if err := c.get(ctx, "/artist/"+mbid, nil, &artist); err != nil {
		return nil, err
	}
	return &artist, nil
}

// SearchArtist searches for an artist by name.
func (c *Client) SearchArtist(ctx context.Context, name string) ([]Artist, error) {
	var resp ArtistSearchResponse
	if err := c.get(ctx, "/artist", url.Values{"query": {name}}, &resp); err != nil {
		return nil, err
	}
	return resp.Artists, nil
}

// SearchReleaseGroup searches release-groups by title directly, useful
// when the query is an album/compilation title rather than an artist name
// (e.g. Lidarr's own fallback for compilation titles that don't resolve
// via artist search).
func (c *Client) SearchReleaseGroup(ctx context.Context, title string) ([]ReleaseGroup, error) {
	var resp ReleaseGroupBrowseResponse
	if err := c.get(ctx, "/release-group", url.Values{"query": {title}}, &resp); err != nil {
		return nil, err
	}
	return resp.ReleaseGroups, nil
}

// GetArtistReleaseGroups browses an artist's release-groups (albums) by
// MBID.

// GetReleaseGroup looks up one release-group by MBID, including its
// series relationships (needed to populate compilation_series - see
// SeriesInfo).
func (c *Client) GetReleaseGroup(ctx context.Context, mbid string) (*ReleaseGroup, error) {
	var rg ReleaseGroup
	query := url.Values{"inc": {"releases+artist-credits+series-rels"}}
	if err := c.get(ctx, "/release-group/"+mbid, query, &rg); err != nil {
		return nil, err
	}
	return &rg, nil
}

// GetRelease looks up one release (a specific pressing/edition) by MBID,
// including its full track/medium listing and per-track artist credits -
// the latter is what makes Various Artists compilations resolve correctly.
func (c *Client) GetRelease(ctx context.Context, mbid string) (*Release, error) {
	var release Release
	query := url.Values{"inc": {"recordings+media+artist-credits+labels"}}
	if err := c.get(ctx, "/release/"+mbid, query, &release); err != nil {
		return nil, err
	}
	return &release, nil
}

// GetArtistReleaseGroups browses an artist's release-groups (albums) by
// MBID - every page of them, since MusicBrainz serves 100 at most and a
// prolific artist has many more.
func (c *Client) GetArtistReleaseGroups(ctx context.Context, artistMBID string) ([]ReleaseGroup, error) {
	var all []ReleaseGroup
	for offset := 0; ; {
		var resp ReleaseGroupBrowseResponse
		query := url.Values{"artist": {artistMBID}, "limit": {"100"}, "offset": {strconv.Itoa(offset)}}
		if err := c.get(ctx, "/release-group", query, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.ReleaseGroups...)
		offset += len(resp.ReleaseGroups)
		if len(resp.ReleaseGroups) == 0 || offset >= resp.Count {
			return all, nil
		}
	}
}

// ListReleases returns every release (pressing/edition) of a release group,
// with the track count and country that tell them apart - a 13-track UK
// release against a 16-track Japanese one. Cheap next to fetching each
// release in full, which is why the picker uses it.
func (c *Client) ListReleases(ctx context.Context, releaseGroupMBID string) ([]ReleaseRef, error) {
	var rg ReleaseGroup
	query := url.Values{"inc": {"releases+media"}}
	if err := c.get(ctx, "/release-group/"+releaseGroupMBID, query, &rg); err != nil {
		return nil, err
	}
	return rg.Releases, nil
}
