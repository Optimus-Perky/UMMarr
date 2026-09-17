package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	gosync "sync"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// What a search is for. Each indexer enables them separately, as in Radarr.
const (
	PurposeInteractive = "interactive"
	PurposeAutomatic   = "automatic"
	PurposeRSS         = "rss"
)

// ErrNoIndexers means no indexer is enabled for the search being run.
var ErrNoIndexers = errors.New("no indexers are enabled for this search - add or enable one in Settings → Indexers")

// NoResultsMessage is Radarr's test failure when an indexer answers but has
// nothing in the configured categories.
const NoResultsMessage = "Query successful, but no results in the configured categories were returned from your indexer. This may be an issue with the indexer or your indexer category settings."

const (
	capsTTL         = time.Hour
	capsFailureTTL  = 5 * time.Minute
	releaseCacheTTL = time.Hour
	indexerTimeout  = 90 * time.Second
)

// IndexerService searches the indexers in Settings -> Indexers. Settings are
// read fresh on every call, so saving an indexer takes effect immediately.
//
// Search results are kept for a while so a grab can refer to a release by
// indexer and GUID: download links (which often carry an indexer's API key)
// never have to go through the browser.
type IndexerService struct {
	DB        *sql.DB
	UserAgent string
	HTTP      *http.Client     // test override
	Now       func() time.Time // test override

	mu       gosync.Mutex
	caps     map[int64]capsEntry
	releases map[releaseKey]cachedRelease
}

type capsEntry struct {
	key     string
	caps    newznab.Caps
	ok      bool
	expires time.Time
}

type releaseKey struct {
	indexerID int64
	guid      string
}

type cachedRelease struct {
	release newznab.Release
	expires time.Time
}

// IndexerError is one indexer's failure during a search.
type IndexerError struct {
	IndexerID int64
	Indexer   string
	Message   string
}

// SearchResult is every release found plus each indexer that failed.
// Searched is how many indexers were asked.
type SearchResult struct {
	Releases []newznab.Release
	Errors   []IndexerError
	Searched int
}

func (s *IndexerService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Client builds a client for ix.
func (s *IndexerService) Client(ix store.Indexer) *newznab.Client {
	return newznab.New(newznab.Settings{
		Implementation: ix.Implementation, BaseURL: ix.BaseURL, APIPath: ix.APIPath,
		APIKey: ix.APIKey, AdditionalParameters: ix.AdditionalParameters,
	}, newznab.Options{UserAgent: s.UserAgent, HTTP: s.HTTP})
}

// Configured reports whether any indexer has interactive search enabled -
// whether Find release buttons can do anything. A nil service isn't.
func (s *IndexerService) Configured(ctx context.Context) bool {
	if s == nil || s.DB == nil {
		return false
	}
	indexers, err := store.ListIndexers(ctx, s.DB)
	if err != nil {
		return false
	}
	for _, ix := range indexers {
		if ix.EnableInteractiveSearch {
			return true
		}
	}
	return false
}

func enabledFor(ix store.Indexer, purpose string) bool {
	switch purpose {
	case PurposeRSS:
		return ix.EnableRSS
	case PurposeAutomatic:
		return ix.EnableAutomaticSearch
	default:
		return ix.EnableInteractiveSearch
	}
}

func endpointKey(ix store.Indexer) string {
	return strings.ToLower(strings.TrimRight(ix.BaseURL, "/")) + "|" + strings.Trim(ix.APIPath, "/")
}

// categoriesFor is the indexer's categories for mediaType ("" = all of them).
func categoriesFor(ix store.Indexer, mediaType string) []int {
	if mediaType == "" {
		return ix.AllCategories()
	}
	return newznab.CategoriesFor(ix.AllCategories(), mediaType)
}

// usable lists the indexers to ask for purpose and mediaType: enabled for it,
// with categories for it, not resting after failures, and one per endpoint
// (a Prowlarr-synced entry wins over a converted one).
func (s *IndexerService) usable(ctx context.Context, purpose, mediaType string) ([]store.Indexer, error) {
	if s == nil || s.DB == nil {
		return nil, nil
	}
	all, err := store.ListIndexers(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(all, func(a, b int) bool {
		if all[a].Synced != all[b].Synced {
			return all[a].Synced
		}
		if all[a].Converted != all[b].Converted {
			return !all[a].Converted
		}
		return all[a].ID < all[b].ID
	})
	now := s.now()
	seen := map[string]bool{}
	var out []store.Indexer
	for _, ix := range all {
		if !enabledFor(ix, purpose) || len(categoriesFor(ix, mediaType)) == 0 || ix.BackedOff(now) {
			continue
		}
		if key := endpointKey(ix); !seen[key] {
			seen[key] = true
			out = append(out, ix)
		}
	}
	return out, nil
}

// indexerCaps returns ix's capabilities, cached; ok is false when they
// couldn't be read, in which case searches fall back to plain text search.
func (s *IndexerService) indexerCaps(ctx context.Context, ix store.Indexer, c *newznab.Client) (newznab.Caps, bool) {
	key := endpointKey(ix) + "|" + ix.APIKey + "|" + ix.AdditionalParameters
	now := s.now()
	s.mu.Lock()
	if e, found := s.caps[ix.ID]; found && e.key == key && e.expires.After(now) {
		s.mu.Unlock()
		return e.caps, e.ok
	}
	s.mu.Unlock()

	caps, err := c.Caps(ctx)
	entry := capsEntry{key: key, caps: caps, ok: err == nil, expires: now.Add(capsTTL)}
	if err != nil {
		entry.expires = now.Add(capsFailureTTL)
	}
	s.mu.Lock()
	if s.caps == nil {
		s.caps = map[int64]capsEntry{}
	}
	s.caps[ix.ID] = entry
	s.mu.Unlock()
	return entry.caps, entry.ok
}

type searchFunc func(ctx context.Context, c *newznab.Client, caps newznab.Caps, capsOK bool, cats []int) ([]newznab.Release, error)

// run asks every usable indexer at once and merges what comes back. A
// failing indexer is recorded (and backs off) without failing the rest.
func (s *IndexerService) run(ctx context.Context, purpose, mediaType string, search searchFunc) SearchResult {
	indexers, err := s.usable(ctx, purpose, mediaType)
	if err != nil {
		return SearchResult{Errors: []IndexerError{{Message: err.Error()}}}
	}
	if len(indexers) == 0 {
		return SearchResult{}
	}
	type outcome struct {
		releases []newznab.Release
		err      error
	}
	outcomes := make([]outcome, len(indexers))
	var wg gosync.WaitGroup
	for i, ix := range indexers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ictx, cancel := context.WithTimeout(ctx, indexerTimeout)
			defer cancel()
			c := s.Client(ix)
			caps, capsOK := s.indexerCaps(ictx, ix, c)
			releases, err := search(ictx, c, caps, capsOK, categoriesFor(ix, mediaType))
			outcomes[i] = outcome{releases, err}
		}()
	}
	wg.Wait()

	result := SearchResult{Searched: len(indexers)}
	seen := map[releaseKey]bool{}
	for i, ix := range indexers {
		o := outcomes[i]
		if o.err != nil {
			if ctx.Err() == nil {
				_ = store.RecordIndexerFailure(context.WithoutCancel(ctx), s.DB, ix.ID, o.err.Error(), s.now())
			}
			result.Errors = append(result.Errors, IndexerError{IndexerID: ix.ID, Indexer: ix.Name, Message: o.err.Error()})
			continue
		}
		if ix.Failures > 0 || ix.LastError != "" {
			_ = store.RecordIndexerSuccess(ctx, s.DB, ix.ID)
		}
		for _, r := range o.releases {
			r.IndexerID, r.Indexer, r.IndexerPriority, r.Protocol = ix.ID, ix.Name, ix.Priority, ix.Protocol()
			key := releaseKey{ix.ID, r.GUID}
			if seen[key] {
				continue
			}
			seen[key] = true
			result.Releases = append(result.Releases, r)
		}
	}
	s.remember(result.Releases)
	return result
}

func (s *IndexerService) remember(releases []newznab.Release) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.releases == nil {
		s.releases = map[releaseKey]cachedRelease{}
	}
	for key, c := range s.releases {
		if !c.expires.After(now) {
			delete(s.releases, key)
		}
	}
	for _, r := range releases {
		s.releases[releaseKey{r.IndexerID, r.GUID}] = cachedRelease{release: r, expires: now.Add(releaseCacheTTL)}
	}
}

// CachedRelease finds a release from a recent search.
func (s *IndexerService) CachedRelease(indexerID int64, guid string) (newznab.Release, bool) {
	if s == nil {
		return newznab.Release{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.releases[releaseKey{indexerID, guid}]
	if !ok || !c.expires.After(s.now()) {
		return newznab.Release{}, false
	}
	return c.release, true
}

// FetchRelease downloads r's .torrent/.nzb (or resolves its magnet) through
// the indexer it came from.
func (s *IndexerService) FetchRelease(ctx context.Context, r newznab.Release) (*newznab.FetchedRelease, error) {
	if s == nil || s.DB == nil {
		return nil, ErrNoIndexers
	}
	ix, err := store.GetIndexer(ctx, s.DB, r.IndexerID)
	if err != nil {
		return nil, fmt.Errorf("fetch release %q: %w", r.Title, err)
	}
	return s.Client(ix).FetchRelease(ctx, r)
}

// Test checks an indexer the way Radarr's Test button does: it must answer
// with results in its categories.
func (s *IndexerService) Test(ctx context.Context, ix store.Indexer) error {
	if len(ix.AllCategories()) == 0 {
		return errors.New("'Categories' must be provided")
	}
	ctx, cancel := context.WithTimeout(ctx, indexerTimeout)
	defer cancel()
	releases, err := s.Client(ix).Recent(ctx, ix.AllCategories())
	if err != nil {
		return err
	}
	if len(releases) == 0 {
		return errors.New(NoResultsMessage)
	}
	return nil
}

var queryJunk = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// cleanQuery strips punctuation from a title for a text search, as Radarr's
// clean scene titles do ("Spider-Man: No Way Home" -> "Spider Man No Way Home").
func cleanQuery(s string) string {
	return strings.TrimSpace(queryJunk.ReplaceAllString(strings.ReplaceAll(s, "'", ""), " "))
}

func textSearch(ctx context.Context, c *newznab.Client, cats []int, q string) ([]newznab.Release, error) {
	return c.Search(ctx, newznab.Query{Mode: "search", Params: url.Values{"q": {q}}, Categories: cats})
}

// MovieCriteria identifies a movie to search for.
type MovieCriteria struct {
	Title  string
	Year   int
	IMDbID string // "tt1375666"
	TMDbID int
}

// SearchMovie follows Radarr: an id search where the indexer supports one,
// then "Title Year" as text if that found nothing.
func (s *IndexerService) SearchMovie(ctx context.Context, purpose string, m MovieCriteria) SearchResult {
	return s.run(ctx, purpose, newznab.MediaMovie, func(ctx context.Context, c *newznab.Client, caps newznab.Caps, capsOK bool, cats []int) ([]newznab.Release, error) {
		if capsOK {
			ids := url.Values{}
			if m.TMDbID > 0 && caps.Supports("movie", "tmdbid") {
				ids.Set("tmdbid", strconv.Itoa(m.TMDbID))
			} else if imdb := strings.TrimPrefix(m.IMDbID, "tt"); imdb != "" && caps.Supports("movie", "imdbid") {
				ids.Set("imdbid", imdb)
			}
			if len(ids) > 0 {
				releases, err := c.Search(ctx, newznab.Query{Mode: "movie", Params: ids, Categories: cats})
				if err != nil || len(releases) > 0 {
					return releases, err
				}
			}
			if caps.Search == nil && !caps.Supports("movie", "q") {
				return nil, nil
			}
		}
		q := cleanQuery(m.Title)
		if m.Year > 0 {
			q += " " + strconv.Itoa(m.Year)
		}
		if capsOK && caps.Search == nil {
			return c.Search(ctx, newznab.Query{Mode: "movie", Params: url.Values{"q": {q}}, Categories: cats})
		}
		return textSearch(ctx, c, cats, q)
	})
}

// SeriesCriteria identifies a series, and optionally a season or episode.
type SeriesCriteria struct {
	Title   string
	TVDBID  int
	Season  *int
	Episode *int
}

func (sc SeriesCriteria) text() string {
	q := cleanQuery(sc.Title)
	switch {
	case sc.Season != nil && sc.Episode != nil:
		q += fmt.Sprintf(" S%02dE%02d", *sc.Season, *sc.Episode)
	case sc.Season != nil:
		q += fmt.Sprintf(" S%02d", *sc.Season)
	}
	return q
}

// SearchSeries searches with t=tvsearch where the indexer supports it, by
// TVDB id if it can, otherwise by title; plain text search otherwise.
func (s *IndexerService) SearchSeries(ctx context.Context, purpose string, sc SeriesCriteria) SearchResult {
	return s.run(ctx, purpose, newznab.MediaSeries, func(ctx context.Context, c *newznab.Client, caps newznab.Caps, capsOK bool, cats []int) ([]newznab.Release, error) {
		if capsOK && caps.TVSearch != nil {
			params := url.Values{}
			if sc.TVDBID > 0 && caps.Supports("tvsearch", "tvdbid") {
				params.Set("tvdbid", strconv.Itoa(sc.TVDBID))
			} else if caps.Supports("tvsearch", "q") {
				params.Set("q", cleanQuery(sc.Title))
			}
			seasonOK := sc.Season == nil || caps.Supports("tvsearch", "season")
			episodeOK := sc.Episode == nil || caps.Supports("tvsearch", "ep")
			if len(params) > 0 && seasonOK && episodeOK {
				if sc.Season != nil {
					params.Set("season", strconv.Itoa(*sc.Season))
				}
				if sc.Episode != nil {
					params.Set("ep", strconv.Itoa(*sc.Episode))
				}
				return c.Search(ctx, newznab.Query{Mode: "tvsearch", Params: params, Categories: cats})
			}
		}
		return textSearch(ctx, c, cats, sc.text())
	})
}

// AlbumCriteria identifies an album.
type AlbumCriteria struct {
	Artist string
	Album  string
}

// SearchAlbum searches with t=music by artist and album where supported, as
// Lidarr does; plain text search otherwise.
func (s *IndexerService) SearchAlbum(ctx context.Context, purpose string, a AlbumCriteria) SearchResult {
	return s.run(ctx, purpose, newznab.MediaMusic, func(ctx context.Context, c *newznab.Client, caps newznab.Caps, capsOK bool, cats []int) ([]newznab.Release, error) {
		if capsOK && caps.Supports("music", "artist") && caps.Supports("music", "album") {
			return c.Search(ctx, newznab.Query{Mode: "music", Params: url.Values{
				"artist": {cleanQuery(a.Artist)}, "album": {cleanQuery(a.Album)},
			}, Categories: cats})
		}
		return textSearch(ctx, c, cats, cleanQuery(a.Artist+" "+a.Album))
	})
}

// SearchTrack searches for one track by artist, as text.
func (s *IndexerService) SearchTrack(ctx context.Context, purpose, artist, track string) SearchResult {
	return s.run(ctx, purpose, newznab.MediaMusic, func(ctx context.Context, c *newznab.Client, _ newznab.Caps, _ bool, cats []int) ([]newznab.Release, error) {
		return textSearch(ctx, c, cats, cleanQuery(artist+" "+track))
	})
}
