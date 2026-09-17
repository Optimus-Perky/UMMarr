// Package tmdb is a client for The Movie Database API (api.themoviedb.org),
// used as the primary movie and TV metadata source. Requires a free v4
// read-access token the user obtains themselves from themoviedb.org - see
// internal/config. Attribution ("This product uses the TMDB API but is not
// endorsed or certified by TMDB") is contractually required by TMDB even
// for self-hosted personal use; UMMarr's web UI must display it once built.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"

	"golang.org/x/time/rate"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/httpclient"
)

const defaultBaseURL = "https://api.themoviedb.org/3"

// ErrMissingCredential is returned by any call made without a token set.
var ErrMissingCredential = errors.New("tmdb: no API token configured (set UMMARR_TMDB_TOKEN)")

// Client talks to the TMDB API. Construct with New; the zero value is not
// usable (Token/UserAgent are required).
type Client struct {
	http  httpclient.Client
	token string
}

// Options configures a Client. BaseURL and HTTP are only ever overridden
// in tests, to point at an httptest.Server instead of the real API.
type Options struct {
	Token     string
	UserAgent string
	BaseURL   string
	HTTP      *http.Client
}

// New builds a TMDB client. TMDB has had no hard rate limit since 2019
// (soft abuse throttling instead), so the limiter here is generous - its
// job is to smooth bursts, not enforce a real cap like MusicBrainz's.
func New(opts Options) *Client {
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		token: opts.Token,
		http: httpclient.Client{
			HTTP:       opts.HTTP,
			BaseURL:    baseURL,
			UserAgent:  opts.UserAgent,
			Limiter:    rate.NewLimiter(rate.Limit(10), 5),
			MaxRetries: 3,
		},
	}
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	if c.token == "" {
		return ErrMissingCredential
	}
	headers := http.Header{"Authorization": []string{"Bearer " + c.token}}
	body, err := c.http.Get(ctx, path, query, headers)
	if err != nil {
		return fmt.Errorf("tmdb: %w", err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("tmdb: decode %s: %w", path, err)
	}
	return nil
}

// SearchMovies searches for movies by title, optionally narrowed by year
// (pass 0 to omit).
func (c *Client) SearchMovies(ctx context.Context, title string, year int) ([]MovieSearchResult, error) {
	query := url.Values{"query": {title}}
	if year > 0 {
		query.Set("year", fmt.Sprintf("%d", year))
	}
	var resp SearchMovieResponse
	if err := c.get(ctx, "/search/movie", query, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// GetMovie fetches full movie details including external IDs (IMDb) in one
// call via append_to_response.
func (c *Client) GetMovie(ctx context.Context, tmdbID int) (*Movie, error) {
	var movie Movie
	query := url.Values{"append_to_response": {"external_ids"}}
	if err := c.get(ctx, fmt.Sprintf("/movie/%d", tmdbID), query, &movie); err != nil {
		return nil, err
	}
	return &movie, nil
}

// SearchSeries searches for TV series by title.
func (c *Client) SearchSeries(ctx context.Context, title string) ([]SeriesSearchResult, error) {
	query := url.Values{"query": {title}}
	var resp SearchSeriesResponse
	if err := c.get(ctx, "/search/tv", query, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// GetSeries fetches full series details including external IDs and the
// season summary list (episode counts, not full episode lists - see
// GetSeason for that).
func (c *Client) GetSeries(ctx context.Context, tmdbID int) (*Series, error) {
	var series Series
	query := url.Values{"append_to_response": {"external_ids"}}
	if err := c.get(ctx, fmt.Sprintf("/tv/%d", tmdbID), query, &series); err != nil {
		return nil, err
	}
	return &series, nil
}

// GetSeason fetches one season's full episode list.
func (c *Client) GetSeason(ctx context.Context, tmdbID, seasonNumber int) (*Season, error) {
	var season Season
	if err := c.get(ctx, fmt.Sprintf("/tv/%d/season/%d", tmdbID, seasonNumber), nil, &season); err != nil {
		return nil, err
	}
	return &season, nil
}

// ParseReleaseDate parses TMDB's "YYYY-MM-DD" date strings, returning nil
// for an empty string (unreleased/unknown) rather than an error.
func ParseReleaseDate(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &t
}

// ListItem is one entry of a TMDB list, discover, popular or find result:
// a movie or a TV series.
type ListItem struct {
	ID           int    `json:"id"`
	MediaType    string `json:"media_type"` // "movie" or "tv"; empty when the endpoint implies it
	Title        string `json:"title"`
	Name         string `json:"name"`
	ReleaseDate  string `json:"release_date"`
	FirstAirDate string `json:"first_air_date"`
}

// DisplayTitle is the movie title or series name.
func (i ListItem) DisplayTitle() string {
	if i.Title != "" {
		return i.Title
	}
	return i.Name
}

// Year is the release or first-air year, 0 when unknown.
func (i ListItem) Year() int {
	for _, d := range []string{i.ReleaseDate, i.FirstAirDate} {
		if len(d) >= 4 {
			var y int
			fmt.Sscanf(d[:4], "%d", &y)
			return y
		}
	}
	return 0
}

// ListItems reads a TMDB list (v3: /list/{id}), all pages.
func (c *Client) ListItems(ctx context.Context, listID string) ([]ListItem, error) {
	var out []ListItem
	for page := 1; page <= 50; page++ {
		var resp struct {
			Items      []ListItem `json:"items"`
			Page       int        `json:"page"`
			TotalPages int        `json:"total_pages"`
		}
		if err := c.get(ctx, "/list/"+url.PathEscape(listID), url.Values{"page": {fmt.Sprint(page)}}, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Items...)
		if resp.TotalPages == 0 || page >= resp.TotalPages || len(resp.Items) == 0 {
			break
		}
	}
	return out, nil
}

// Results reads a paged "results" endpoint - popular, top rated, trending,
// discover - up to limit items. kind is "movie" or "tv".
func (c *Client) Results(ctx context.Context, path string, query url.Values, limit int) ([]ListItem, error) {
	var out []ListItem
	if query == nil {
		query = url.Values{}
	}
	for page := 1; page <= 25 && len(out) < limit; page++ {
		query.Set("page", fmt.Sprint(page))
		var resp struct {
			Results    []ListItem `json:"results"`
			TotalPages int        `json:"total_pages"`
		}
		if err := c.get(ctx, path, query, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Results...)
		if page >= resp.TotalPages || len(resp.Results) == 0 {
			break
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Find resolves an IMDb (tt...) or TVDB id to TMDB movies and series.
func (c *Client) Find(ctx context.Context, externalID, source string) (movies, series []ListItem, err error) {
	var resp struct {
		Movies []ListItem `json:"movie_results"`
		Series []ListItem `json:"tv_results"`
	}
	if err := c.get(ctx, "/find/"+url.PathEscape(externalID), url.Values{"external_source": {source}}, &resp); err != nil {
		return nil, nil, err
	}
	return resp.Movies, resp.Series, nil
}

// ImageURL turns a TMDB file path into a URL at the given width, e.g.
// ImageURL("/abc.jpg", "w500").
func ImageURL(path, size string) string {
	if path == "" {
		return ""
	}
	return "https://image.tmdb.org/t/p/" + size + path
}

type imagesResponse struct {
	Posters []struct {
		FilePath    string  `json:"file_path"`
		VoteAverage float64 `json:"vote_average"`
		ISO639      *string `json:"iso_639_1"`
	} `json:"posters"`
}

// posterURLs reads one images endpoint and returns poster URLs, best first.
func (c *Client) posterURLs(ctx context.Context, path string) ([]string, error) {
	var resp imagesResponse
	if err := c.get(ctx, path, url.Values{"include_image_language": {"en,null"}}, &resp); err != nil {
		return nil, err
	}
	sort.SliceStable(resp.Posters, func(i, j int) bool { return resp.Posters[i].VoteAverage > resp.Posters[j].VoteAverage })
	var out []string
	for _, p := range resp.Posters {
		if u := ImageURL(p.FilePath, "w342"); u != "" {
			out = append(out, u)
		}
	}
	return out, nil
}

// MoviePosters lists the posters TMDB has for a movie, best first.
func (c *Client) MoviePosters(ctx context.Context, tmdbID int) ([]string, error) {
	return c.posterURLs(ctx, fmt.Sprintf("/movie/%d/images", tmdbID))
}

// SeriesPosters lists the posters TMDB has for a series, best first.
func (c *Client) SeriesPosters(ctx context.Context, tmdbID int) ([]string, error) {
	return c.posterURLs(ctx, fmt.Sprintf("/tv/%d/images", tmdbID))
}
