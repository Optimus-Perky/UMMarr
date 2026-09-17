// Package importlist adds titles from outside lists to the library on a
// schedule - Radarr's and Sonarr's Import Lists: TMDB lists, popular / top
// rated / trending / discover, IMDb lists, a Plex watchlist RSS feed and
// Trakt lists.
package importlist

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// Item is one title a list offers.
type Item struct {
	TMDBID   int
	Title    string
	Year     int
	Tracked  bool // already in the library
	Excluded bool // on the exclusion list
}

// Service fetches lists and adds what they name.
type Service struct {
	DB     *sql.DB
	TMDB   *tmdb.Client
	Movies *sync.MovieService
	Series *sync.SeriesService
	Search *sync.SearchService
	Events *sync.Events
	HTTP   *http.Client
	// UserAgent identifies UMMarr to IMDb, Plex and Trakt.
	UserAgent string
	// TraktBase overrides the site for tests.
	TraktBase string
}

func (s *Service) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (s *Service) getBody(ctx context.Context, rawURL string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if s.UserAgent != "" {
		req.Header.Set("User-Agent", s.UserAgent)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, req.URL.Host)
	}
	return data, nil
}

func limitOf(settings map[string]string) int {
	n, _ := strconv.Atoi(settings["limit"])
	if n <= 0 {
		return 50
	}
	if n > 500 {
		n = 500
	}
	return n
}

func tmdbKind(mediaType string) string {
	if mediaType == "series" {
		return "tv"
	}
	return "movie"
}

func fromTMDB(items []tmdb.ListItem, mediaType string, filterType bool) []Item {
	want := tmdbKind(mediaType)
	var out []Item
	for _, it := range items {
		if filterType && it.MediaType != "" && it.MediaType != want {
			continue
		}
		if it.ID > 0 {
			out = append(out, Item{TMDBID: it.ID, Title: it.DisplayTitle(), Year: it.Year()})
		}
	}
	return out
}

// Fetch reads the list's titles from its source.
func (s *Service) Fetch(ctx context.Context, l store.ImportList) ([]Item, error) {
	if s.TMDB == nil {
		return nil, fmt.Errorf("TMDB isn't configured")
	}
	kind := tmdbKind(l.MediaType)
	switch l.Implementation {
	case store.ListTMDBList:
		id := strings.TrimSpace(l.Settings["list_id"])
		if id == "" {
			return nil, fmt.Errorf("a TMDB list id is required")
		}
		items, err := s.TMDB.ListItems(ctx, id)
		if err != nil {
			return nil, err
		}
		return fromTMDB(items, l.MediaType, true), nil
	case store.ListTMDBPopular:
		items, err := s.TMDB.Results(ctx, "/"+kind+"/popular", nil, limitOf(l.Settings))
		return fromTMDB(items, l.MediaType, false), err
	case store.ListTMDBTopRated:
		items, err := s.TMDB.Results(ctx, "/"+kind+"/top_rated", nil, limitOf(l.Settings))
		return fromTMDB(items, l.MediaType, false), err
	case store.ListTMDBTrending:
		items, err := s.TMDB.Results(ctx, "/trending/"+kind+"/week", nil, limitOf(l.Settings))
		return fromTMDB(items, l.MediaType, false), err
	case store.ListTMDBDiscover:
		q := url.Values{"sort_by": {"popularity.desc"}, "include_adult": {"false"}}
		if g := strings.TrimSpace(l.Settings["genres"]); g != "" {
			q.Set("with_genres", strings.ReplaceAll(g, " ", ""))
		}
		if v := strings.TrimSpace(l.Settings["min_rating"]); v != "" {
			q.Set("vote_average.gte", v)
		}
		if v := strings.TrimSpace(l.Settings["min_votes"]); v != "" {
			q.Set("vote_count.gte", v)
		}
		if v := strings.TrimSpace(l.Settings["language"]); v != "" {
			q.Set("with_original_language", v)
		}
		dateField := "primary_release_date"
		if kind == "tv" {
			dateField = "first_air_date"
		}
		if v := strings.TrimSpace(l.Settings["year_from"]); v != "" {
			q.Set(dateField+".gte", v+"-01-01")
		}
		if v := strings.TrimSpace(l.Settings["year_to"]); v != "" {
			q.Set(dateField+".lte", v+"-12-31")
		}
		items, err := s.TMDB.Results(ctx, "/discover/"+kind, q, limitOf(l.Settings))
		return fromTMDB(items, l.MediaType, false), err
	case store.ListPlexRSS:
		return s.plexRSS(ctx, l)
	case store.ListTrakt:
		return s.trakt(ctx, l)
	}
	return nil, fmt.Errorf("unknown import list %q", l.Implementation)
}

// resolveIMDb turns IMDb ids into TMDB items of the list's media type.
func (s *Service) resolveIMDb(ctx context.Context, mediaType string, ids []string, titles map[string]string) []Item {
	var out []Item
	for _, id := range ids {
		movies, series, err := s.TMDB.Find(ctx, id, "imdb_id")
		if err != nil {
			continue
		}
		found := movies
		if mediaType == "series" {
			found = series
		}
		if len(found) > 0 {
			out = append(out, Item{TMDBID: found[0].ID, Title: found[0].DisplayTitle(), Year: found[0].Year()})
		} else if t := titles[id]; t != "" {
			log.Printf("import list: no TMDB %s for %s (%s)", mediaType, id, t)
		}
	}
	return out
}

var guidPattern = regexp.MustCompile(`(imdb|tmdb|tvdb)://(tt\d+|\d+)`)

type rssItem struct {
	Title    string `xml:"title"`
	Category string `xml:"category"`
	Inner    string `xml:",innerxml"`
}

// plexRSS reads a Plex watchlist feed (Plex → Watchlist → RSS).
func (s *Service) plexRSS(ctx context.Context, l store.ImportList) ([]Item, error) {
	feed := strings.TrimSpace(l.Settings["url"])
	if feed == "" {
		return nil, fmt.Errorf("the watchlist RSS URL is required")
	}
	data, err := s.getBody(ctx, feed, map[string]string{"Accept": "application/rss+xml"})
	if err != nil {
		return nil, fmt.Errorf("plex rss: %w", err)
	}
	var doc struct {
		Items []rssItem `xml:"channel>item"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("plex rss: not an RSS feed: %w", err)
	}
	var out []Item
	var imdbIDs []string
	titles := map[string]string{}
	for _, it := range doc.Items {
		category := strings.ToLower(it.Category)
		if category != "" && ((l.MediaType == "series") != (category == "show" || category == "series")) {
			continue
		}
		var tmdbID int
		var imdbID string
		for _, m := range guidPattern.FindAllStringSubmatch(it.Inner, -1) {
			switch m[1] {
			case "tmdb":
				tmdbID, _ = strconv.Atoi(m[2])
			case "imdb":
				imdbID = m[2]
			}
		}
		switch {
		case tmdbID > 0:
			out = append(out, Item{TMDBID: tmdbID, Title: it.Title})
		case imdbID != "":
			imdbIDs = append(imdbIDs, imdbID)
			titles[imdbID] = it.Title
		}
	}
	return append(out, s.resolveIMDb(ctx, l.MediaType, imdbIDs, titles)...), nil
}

// trakt reads a public Trakt list with the user's own API client id.
func (s *Service) trakt(ctx context.Context, l store.ImportList) ([]Item, error) {
	user, list, clientID := strings.TrimSpace(l.Settings["user"]), strings.TrimSpace(l.Settings["list"]), strings.TrimSpace(l.Settings["client_id"])
	if user == "" || list == "" || clientID == "" {
		return nil, fmt.Errorf("trakt needs the list owner, list slug and your API client id")
	}
	base := s.TraktBase
	if base == "" {
		base = "https://api.trakt.tv"
	}
	kind := "movies"
	if l.MediaType == "series" {
		kind = "shows"
	}
	data, err := s.getBody(ctx, fmt.Sprintf("%s/users/%s/lists/%s/items/%s", base, url.PathEscape(user), url.PathEscape(list), kind),
		map[string]string{"Content-Type": "application/json", "trakt-api-version": "2", "trakt-api-key": clientID})
	if err != nil {
		return nil, fmt.Errorf("trakt: %w", err)
	}
	var entries []struct {
		Movie *struct {
			Title string `json:"title"`
			Year  int    `json:"year"`
			IDs   struct {
				TMDB int `json:"tmdb"`
			} `json:"ids"`
		} `json:"movie"`
		Show *struct {
			Title string `json:"title"`
			Year  int    `json:"year"`
			IDs   struct {
				TMDB int `json:"tmdb"`
			} `json:"ids"`
		} `json:"show"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("trakt: bad reply: %w", err)
	}
	var out []Item
	for _, e := range entries {
		switch {
		case e.Movie != nil && e.Movie.IDs.TMDB > 0:
			out = append(out, Item{TMDBID: e.Movie.IDs.TMDB, Title: e.Movie.Title, Year: e.Movie.Year})
		case e.Show != nil && e.Show.IDs.TMDB > 0:
			out = append(out, Item{TMDBID: e.Show.IDs.TMDB, Title: e.Show.Title, Year: e.Show.Year})
		}
	}
	return out, nil
}

// Preview fetches a list and marks what's already tracked or excluded.
func (s *Service) Preview(ctx context.Context, l store.ImportList) ([]Item, error) {
	items, err := s.Fetch(ctx, l)
	if err != nil {
		return nil, err
	}
	tracked, _ := store.TrackedTMDBIDs(ctx, s.DB, l.MediaType)
	excluded := s.excluded(ctx, l.MediaType)
	seen := map[int]bool{}
	out := items[:0]
	for _, it := range items {
		if seen[it.TMDBID] {
			continue
		}
		seen[it.TMDBID] = true
		it.Tracked, it.Excluded = tracked[it.TMDBID], excluded[it.TMDBID]
		out = append(out, it)
	}
	return out, nil
}

func (s *Service) excluded(ctx context.Context, mediaType string) map[int]bool {
	out := map[int]bool{}
	exclusions, _ := store.ListExclusions(ctx, s.DB)
	for _, e := range exclusions {
		if e.MediaType == mediaType {
			out[e.TMDBID] = true
		}
	}
	return out
}

// Result is what one list's sync did.
type Result struct {
	Added, Tracked, Excluded, Failed int
	Err                              error
}

func (r Result) String() string {
	if r.Err != nil {
		return "failed: " + r.Err.Error()
	}
	return fmt.Sprintf("%d added, %d already in the library, %d excluded, %d failed", r.Added, r.Tracked, r.Excluded, r.Failed)
}

// SyncList fetches one list and adds its new titles (when it auto-adds).
func (s *Service) SyncList(ctx context.Context, l store.ImportList) Result {
	items, err := s.Preview(ctx, l)
	if err != nil {
		_ = store.RecordImportListSync(ctx, s.DB, l.ID, "failed: "+err.Error())
		return Result{Err: err}
	}
	var r Result
	for _, it := range items {
		switch {
		case it.Tracked:
			r.Tracked++
			continue
		case it.Excluded:
			r.Excluded++
			continue
		case !l.AutoAdd:
			continue
		}
		if err := s.add(ctx, l, it); err != nil {
			log.Printf("import list %s: add %s: %v", l.Name, it.Title, err)
			r.Failed++
			continue
		}
		r.Added++
	}
	_ = store.RecordImportListSync(ctx, s.DB, l.ID, r.String())
	return r
}

func (s *Service) add(ctx context.Context, l store.ImportList, it Item) error {
	rootID, profileID := l.RootFolderID.Int64, l.QualityProfileID.Int64
	if rootID == 0 {
		folders, err := store.ListRootFolders(ctx, s.DB, l.MediaType)
		if err != nil || len(folders) == 0 {
			return fmt.Errorf("no %s library folder", l.MediaType)
		}
		rootID = folders[0].ID
	}
	if profileID == 0 {
		id, err := store.DefaultQualityProfileID(ctx, s.DB)
		if err != nil {
			return err
		}
		profileID = id
	}
	var e store.Event
	switch l.MediaType {
	case "movie":
		if s.Movies == nil {
			return fmt.Errorf("movies aren't available")
		}
		id, err := s.Movies.AddByTMDBID(ctx, it.TMDBID, rootID, profileID)
		if err != nil {
			return err
		}
		if !l.Monitored {
			_ = store.UpdateMovieMonitored(ctx, s.DB, id, false)
		}
		e = store.Event{Event: store.EventAdded, MediaType: "movie", MovieID: sql.NullInt64{Int64: id, Valid: true}}
		if l.SearchOnAdd && l.Monitored && s.Search != nil {
			go s.Search.SearchMovie(context.WithoutCancel(ctx), id)
		}
	case "series":
		if s.Series == nil {
			return fmt.Errorf("series aren't available")
		}
		id, err := s.Series.AddByTMDBID(ctx, it.TMDBID, rootID, profileID)
		if err != nil {
			return err
		}
		if !l.Monitored {
			_ = store.UpdateSeriesMonitored(ctx, s.DB, id, false)
		}
		e = store.Event{Event: store.EventAdded, MediaType: "series", SeriesID: sql.NullInt64{Int64: id, Valid: true}}
		if l.SearchOnAdd && l.Monitored && s.Search != nil {
			go s.Search.SearchSeries(context.WithoutCancel(ctx), id, nil)
		}
	default:
		return fmt.Errorf("unknown media type %q", l.MediaType)
	}
	e.Title = it.Title
	if it.Year > 0 {
		e.Title = fmt.Sprintf("%s (%d)", it.Title, it.Year)
	}
	e.Detail, e.Source = "From the import list "+l.Name, "import list"
	s.Events.Record(ctx, e)
	return nil
}

// SyncAll syncs every enabled list; the first failure is returned after
// the rest have run.
func (s *Service) SyncAll(ctx context.Context) error {
	lists, err := store.ListImportLists(ctx, s.DB)
	if err != nil {
		return err
	}
	var first error
	for _, l := range lists {
		if !l.Enabled {
			continue
		}
		r := s.SyncList(ctx, l)
		log.Printf("import list %s: %s", l.Name, r)
		if r.Err != nil && first == nil {
			first = fmt.Errorf("%s: %w", l.Name, r.Err)
		}
	}
	return first
}
