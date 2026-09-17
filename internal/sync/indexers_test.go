package sync

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	gosync "sync"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func testFeed(items ...string) string {
	return `<?xml version="1.0"?><rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>` +
		strings.Join(items, "") + `</channel></rss>`
}

func testItem(guid, title string) string {
	return fmt.Sprintf(`<item><title>%s</title><guid>%s</guid><link>http://example.invalid/%s</link><torznab:attr name="seeders" value="5"/></item>`, title, guid, guid)
}

// fakeIndexer is a Torznab endpoint: caps (404 when empty), a feed per t=
// mode (falling back to feed), or a fixed error status.
type fakeIndexer struct {
	caps   string
	feed   string
	byMode map[string]string
	status int

	mu      gosync.Mutex
	queries []url.Values
}

func (f *fakeIndexer) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f.mu.Lock()
		f.queries = append(f.queries, q)
		f.mu.Unlock()
		if q.Get("t") == "caps" {
			if f.caps == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write([]byte(f.caps))
			return
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		if body, ok := f.byMode[q.Get("t")]; ok {
			w.Write([]byte(body))
			return
		}
		w.Write([]byte(f.feed))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// searches are the non-caps requests the indexer received.
func (f *fakeIndexer) searches() []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []url.Values
	for _, q := range f.queries {
		if q.Get("t") != "caps" {
			out = append(out, q)
		}
	}
	return out
}

func addTestIndexer(t *testing.T, db *sql.DB, name, baseURL string, change func(*store.Indexer)) int64 {
	t.Helper()
	ix := store.Indexer{
		Name: name, Implementation: "Torznab", Priority: 25, BaseURL: baseURL, APIPath: "/api", APIKey: "key",
		EnableRSS: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true,
		Categories: []int{2000, 5000, 3000}, MinimumSeeders: 1,
	}
	if change != nil {
		change(&ix)
	}
	id, err := store.CreateIndexer(context.Background(), db, ix)
	if err != nil {
		t.Fatalf("create indexer: %v", err)
	}
	return id
}

const movieIDCaps = `<caps><searching><search available="yes" supportedParams="q"/><movie-search available="yes" supportedParams="q,imdbid,tmdbid"/><tv-search available="yes" supportedParams="q,season,ep,tvdbid"/></searching></caps>`

func TestSearchMovie_AsksEveryIndexerAndKeepsGoingWhenOneFails(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	good := &fakeIndexer{caps: movieIDCaps, feed: testFeed(testItem("g1", "Inception 2010 1080p BluRay"))}
	broken := &fakeIndexer{status: http.StatusInternalServerError}
	goodID := addTestIndexer(t, db, "Good", good.serve(t), nil)
	brokenID := addTestIndexer(t, db, "Broken", broken.serve(t), nil)
	svc := &IndexerService{DB: db}

	result := svc.SearchMovie(ctx, PurposeInteractive, MovieCriteria{Title: "Inception", Year: 2010, TMDbID: 27205, IMDbID: "tt1375666"})
	if result.Searched != 2 || len(result.Releases) != 1 || len(result.Errors) != 1 {
		t.Fatalf("want 2 indexers asked, 1 release and 1 error, got %+v", result)
	}
	r := result.Releases[0]
	if r.Indexer != "Good" || r.IndexerID != goodID || r.Protocol != "torrent" || r.IndexerPriority != 25 {
		t.Errorf("want the release tagged with its indexer, got %+v", r)
	}
	if e := result.Errors[0]; e.Indexer != "Broken" || e.IndexerID != brokenID || !strings.Contains(e.Message, "500") {
		t.Errorf("want the broken indexer's error reported, got %+v", e)
	}
	searches := good.searches()
	if len(searches) != 1 || searches[0].Get("t") != "movie" || searches[0].Get("tmdbid") != "27205" || searches[0].Get("imdbid") != "" {
		t.Errorf("want one t=movie search by tmdb id, got %v", searches)
	}
	if _, ok := svc.CachedRelease(goodID, "g1"); !ok {
		t.Errorf("want the release remembered for a grab")
	}

	ix, _ := store.GetIndexer(ctx, db, brokenID)
	if ix.Failures != 1 || ix.DisabledUntil.Valid == false {
		t.Fatalf("want the broken indexer's failure recorded with a back-off, got %+v", ix)
	}
	again := svc.SearchMovie(ctx, PurposeInteractive, MovieCriteria{Title: "Inception", Year: 2010})
	if again.Searched != 1 {
		t.Fatalf("want the resting indexer skipped on the next search, got %d asked", again.Searched)
	}
}

func TestSearchMovie_TextSearchWhenNoIDSearch(t *testing.T) {
	db := openTestDB(t)
	idx := &fakeIndexer{
		caps: `<caps><searching><search available="yes" supportedParams="q"/><movie-search available="yes" supportedParams="q"/></searching></caps>`,
		feed: testFeed(),
	}
	addTestIndexer(t, db, "Text", idx.serve(t), nil)

	(&IndexerService{DB: db}).SearchMovie(context.Background(), PurposeInteractive, MovieCriteria{Title: "Spider-Man: No Way Home", Year: 2021, TMDbID: 634649})
	searches := idx.searches()
	if len(searches) != 1 || searches[0].Get("t") != "search" || searches[0].Get("q") != "Spider Man No Way Home 2021" {
		t.Fatalf("want a text search for the clean title and year, got %v", searches)
	}
	if searches[0].Get("cat") != "2000" {
		t.Fatalf("want only the indexer's movie categories, got %q", searches[0].Get("cat"))
	}
}

func TestSearchMovie_IDSearchWithNothingFallsBackToText(t *testing.T) {
	db := openTestDB(t)
	idx := &fakeIndexer{
		caps:   movieIDCaps,
		byMode: map[string]string{"movie": testFeed(), "search": testFeed(testItem("t1", "Inception 2010"))},
	}
	addTestIndexer(t, db, "Both", idx.serve(t), nil)

	result := (&IndexerService{DB: db}).SearchMovie(context.Background(), PurposeInteractive, MovieCriteria{Title: "Inception", Year: 2010, TMDbID: 27205})
	if len(result.Releases) != 1 || len(idx.searches()) != 2 {
		t.Fatalf("want the id search then the text search, got %d releases from %v", len(result.Releases), idx.searches())
	}
}

func TestSearchSeries_TVSearchWithSeasonAndEpisode(t *testing.T) {
	db := openTestDB(t)
	idx := &fakeIndexer{caps: movieIDCaps, feed: testFeed()}
	addTestIndexer(t, db, "TV", idx.serve(t), nil)
	season, episode := 1, 3

	(&IndexerService{DB: db}).SearchSeries(context.Background(), PurposeInteractive, SeriesCriteria{Title: "Breaking Bad", TVDBID: 81189, Season: &season, Episode: &episode})
	q := idx.searches()[0]
	if q.Get("t") != "tvsearch" || q.Get("tvdbid") != "81189" || q.Get("season") != "1" || q.Get("ep") != "3" || q.Get("cat") != "5000" {
		t.Fatalf("want t=tvsearch by tvdb id with season and episode in TV categories, got %v", q)
	}
}

func TestSearchSeries_TextWhenCapsUnavailable(t *testing.T) {
	db := openTestDB(t)
	idx := &fakeIndexer{feed: testFeed()}
	addTestIndexer(t, db, "NoCaps", idx.serve(t), nil)
	season, episode := 1, 3

	(&IndexerService{DB: db}).SearchSeries(context.Background(), PurposeInteractive, SeriesCriteria{Title: "Breaking Bad", Season: &season, Episode: &episode})
	q := idx.searches()[0]
	if q.Get("t") != "search" || q.Get("q") != "Breaking Bad S01E03" {
		t.Fatalf("want a text search for the episode, got %v", q)
	}
}

func TestSearch_OnlyAsksSuitableIndexers(t *testing.T) {
	db := openTestDB(t)
	shared := &fakeIndexer{feed: testFeed(testItem("s1", "Breaking Bad S01E01"))}
	sharedURL := shared.serve(t)
	other := &fakeIndexer{feed: testFeed(testItem("o1", "Breaking Bad S01E01"))}
	otherURL := other.serve(t)

	addTestIndexer(t, db, "Interactive off", otherURL, func(ix *store.Indexer) { ix.EnableInteractiveSearch = false })
	addTestIndexer(t, db, "Movies only", otherURL+"/movies", func(ix *store.Indexer) { ix.Categories = []int{2000} })
	addTestIndexer(t, db, "Converted", sharedURL, func(ix *store.Indexer) { ix.Converted = true })
	addTestIndexer(t, db, "Synced", sharedURL+"/", func(ix *store.Indexer) { ix.Synced = true })

	result := (&IndexerService{DB: db}).SearchSeries(context.Background(), PurposeInteractive, SeriesCriteria{Title: "Breaking Bad"})
	if result.Searched != 1 || len(result.Releases) != 1 || result.Releases[0].Indexer != "Synced" {
		t.Fatalf("want only the synced entry for the shared endpoint asked, got %+v", result)
	}
	if len(other.searches()) != 0 {
		t.Fatalf("want the disabled and movie-only indexers left alone, got %v", other.searches())
	}
}

func TestIndexerTest(t *testing.T) {
	db := openTestDB(t)
	svc := &IndexerService{DB: db}
	ctx := context.Background()

	empty := &fakeIndexer{feed: testFeed()}
	ix := store.Indexer{Implementation: "Torznab", BaseURL: empty.serve(t), APIPath: "/api", Categories: []int{2000}}
	if err := svc.Test(ctx, ix); err == nil || err.Error() != NoResultsMessage {
		t.Errorf("want Radarr's no results message, got %v", err)
	}
	full := &fakeIndexer{feed: testFeed(testItem("a", "Something"))}
	ix.BaseURL = full.serve(t)
	if err := svc.Test(ctx, ix); err != nil {
		t.Errorf("want a working indexer to pass, got %v", err)
	}
	ix.Categories = nil
	if err := svc.Test(ctx, ix); err == nil || !strings.Contains(err.Error(), "Categories") {
		t.Errorf("want categories required, got %v", err)
	}
}

func TestIndexerService_Configured(t *testing.T) {
	db := openTestDB(t)
	var nilService *IndexerService
	if nilService.Configured(context.Background()) {
		t.Fatalf("want a nil service not configured")
	}
	svc := &IndexerService{DB: db}
	addTestIndexer(t, db, "RSS only", "http://example.invalid", func(ix *store.Indexer) { ix.EnableInteractiveSearch = false })
	if svc.Configured(context.Background()) {
		t.Fatalf("want no Find release without an interactive indexer")
	}
	addTestIndexer(t, db, "Interactive", "http://example.invalid/2", nil)
	if !svc.Configured(context.Background()) {
		t.Fatalf("want configured once an indexer allows interactive search")
	}
}
