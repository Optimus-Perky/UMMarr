package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/discogs"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Discogs matches loosely - searching for one album returns anything with
// similar words - so taking the first hit would hang the wrong cover on an
// album. These pin that a hit has to actually be this album, that the
// right year wins among several, and that the image lands in UMMarr's own
// folder rather than in the music library.

func discogsTestServer(t *testing.T, results []discogs.SearchResult) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/database/search"):
			_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
		case strings.HasPrefix(r.URL.Path, "/releases/"):
			_ = json.NewEncoder(w).Encode(discogs.Release{ID: 1, Images: []discogs.Image{{Type: "primary", URI: "http://" + r.Host + "/image.jpg"}}})
		case r.URL.Path == "/image.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpeg-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchMissingCovers(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_, albumID, _ := importedAlbum(t, db) // Daft Punk - Homework, with files

	srv := discogsTestServer(t, []discogs.SearchResult{
		{ID: 9, Title: "Someone Else - Other Record", CoverImage: "http://example.invalid/wrong.jpg"},
		{ID: 1, Title: "Daft Punk - Homework", Year: "1997", CoverImage: ""},
	})
	client, err := discogs.New(discogs.Options{UserAgent: "UMMarr/test", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	fetcher := &CoverFetcher{DB: db, Discogs: client, Dir: dir}

	report, err := fetcher.FetchMissingCovers(ctx, 0)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if report.Looked != 1 || report.Fetched != 1 || report.Failed != 0 {
		t.Fatalf("report = %+v", report)
	}

	// The file is in UMMarr's folder, and the album points at it.
	file, err := store.CoverFile(ctx, db, "album", albumID)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(file) != dir {
		t.Errorf("cover saved to %q, want it under %q - never in the music library", file, dir)
	}
	if body, err := os.ReadFile(file); err != nil || string(body) != "jpeg-bytes" {
		t.Errorf("cover file: %q %v", body, err)
	}

	// Run again: the album now has artwork, so it isn't looked up twice.
	report, err = fetcher.FetchMissingCovers(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if report.Looked != 0 {
		t.Errorf("want nothing left to look up, got %+v", report)
	}
}

func TestFetchMissingCovers_WrongAlbumIsNoMatch(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	importedAlbum(t, db)

	// Everything Discogs returns is a different record.
	srv := discogsTestServer(t, []discogs.SearchResult{
		{ID: 9, Title: "Daft Punk - Discovery", Year: "2001", CoverImage: "http://example.invalid/discovery.jpg"},
	})
	client, _ := discogs.New(discogs.Options{UserAgent: "UMMarr/test", BaseURL: srv.URL})
	fetcher := &CoverFetcher{DB: db, Discogs: client, Dir: t.TempDir()}

	report, err := fetcher.FetchMissingCovers(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if report.Fetched != 0 || report.NoMatch != 1 {
		t.Fatalf("want the wrong album refused, got %+v", report)
	}
}

func TestBestEdition_PrefersTheRightYear(t *testing.T) {
	candidate := store.AlbumEditionCandidate{Artist: "Portishead", Album: "Dummy", Year: 1994}
	results := []discogs.SearchResult{
		{ID: 1, Title: "Portishead - Dummy", Year: "2014"},
		{ID: 2, Title: "Portishead - Dummy", Year: "1994"},
	}
	best, ok := bestEdition(results, candidate)
	if !ok || best.ID != 2 {
		t.Fatalf("want the 1994 pressing, got %+v (%v)", best, ok)
	}
	if _, ok := bestEdition(nil, candidate); ok {
		t.Error("want no match from no results")
	}
	// A hit that is a different record entirely is no match, however
	// loosely Discogs thinks it fits the search.
	if _, ok := bestEdition([]discogs.SearchResult{{ID: 3, Title: "Portishead - Third"}}, candidate); ok {
		t.Error("want a different album refused")
	}
}

// Edition detail is what Discogs adds over MusicBrainz: the label,
// catalogue number and the descriptions that tell a remaster from an
// original. The same strict matching applies - a wrong album must not
// stamp its pressing onto this one.
func TestFetchEditions(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_, albumID, _ := importedAlbum(t, db)
	// MusicBrainz already settled what the copy on disk is: a UK CD from
	// 1997. Discogs should be asked about that, not about the album in
	// general.
	if _, err := db.Exec(`UPDATE album_releases SET country = '["GB"]', media = '[{"number":1,"format":"CD"}]', release_date = '1997-01-20' WHERE album_id = ?`, albumID); err != nil {
		t.Fatal(err)
	}

	var queries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/database/search"):
			queries = append(queries, r.URL.Query())
			// What Discogs actually hands back: every pressing ever made,
			// in an order of its own choosing, the real one third.
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []discogs.SearchResult{
				{ID: 7, Title: "Daft Punk - Homework", Year: "2013", Country: "Russia", Format: []string{"CD", "Album", "Unofficial Release"}},
				{ID: 8, Title: "Daft Punk - Homework", Year: "1997", Country: "Australia", Format: []string{"Cassette", "Album"}},
				{ID: 42, Title: "Daft Punk - Homework", Year: "1997", Country: "UK", Format: []string{"CD", "Album"}},
				{ID: 9, Title: "Daft Punk - Homework Remixes", Year: "1997", Country: "UK", Format: []string{"CD"}},
			}})
		case r.URL.Path == "/releases/42":
			_ = json.NewEncoder(w).Encode(discogs.Release{
				ID: 42, Title: "Homework", Year: 1997, Country: "UK",
				Formats: []discogs.Format{{Name: "Vinyl", Quantity: "2", Descriptions: []string{"LP", "Album", "Remastered"}}},
				Labels:  []discogs.Label{{Name: "Virgin", CatalogueNo: "V 2821"}},
				Images:  []discogs.Image{{Type: "primary", URI: "http://" + r.Host + "/image.jpg"}},
			})
		case r.URL.Path == "/image.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("cover"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	client, err := discogs.New(discogs.Options{UserAgent: "UMMarr/test", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &CoverFetcher{DB: db, Discogs: client, Dir: t.TempDir()}

	report, err := fetcher.FetchEditions(ctx, 0, false)
	if err != nil {
		t.Fatalf("fetch editions: %v", err)
	}
	if report.Filled != 1 || report.NoMatch != 0 || report.Failed != 0 {
		t.Fatalf("report = %+v", report)
	}

	// The search itself was narrowed, so the bootlegs mostly never came
	// back in the first place.
	if len(queries) != 1 {
		t.Fatalf("want one search, got %d", len(queries))
	}
	if got := queries[0]; got.Get("country") != "GB" || got.Get("format") != "CD" {
		t.Errorf("search was not narrowed: %v", got)
	}

	edition, err := store.GetAlbumEdition(ctx, db, albumID)
	if err != nil {
		t.Fatal(err)
	}
	// Release 42, not the Russian bootleg, the Australian cassette or the
	// remix album that merely starts with the same words.
	if edition.DiscogsReleaseID != 42 {
		t.Fatalf("chose release %d", edition.DiscogsReleaseID)
	}
	if edition.Label != "Virgin" || edition.Catalogue != "V 2821" || edition.Country != "UK" || edition.Year != 1997 {
		t.Errorf("edition = %+v", edition)
	}
	// A double album says so, and the descriptions come through in order.
	if edition.Format != "2xVinyl, LP, Album, Remastered" {
		t.Errorf("format = %q", edition.Format)
	}
	if got := edition.Summary(); got != "2xVinyl, LP, Album, Remastered · Virgin V 2821 · UK · 1997" {
		t.Errorf("summary = %q", got)
	}
	if edition.DiscogsURL() != "https://www.discogs.com/release/42" {
		t.Errorf("link = %q", edition.DiscogsURL())
	}

	// The release was fetched anyway, so its cover is kept rather than
	// costing a second lookup later.
	if file, _ := store.CoverFile(ctx, db, "album", albumID); file == "" {
		t.Error("want the cover kept from the release fetched for its edition")
	}

	// A second run has nothing to do.
	if report, err := fetcher.FetchEditions(ctx, 0, false); err != nil || report.Looked != 0 {
		t.Errorf("want nothing left, got %+v (%v)", report, err)
	}
}

// TestFetchEditionsWidensSearch: a library copy MusicBrainz describes as a
// GB CD may have no GB CD on Discogs at all. Rather than give up, the
// search drops one constraint at a time.
func TestFetchEditionsWidensSearch(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_, albumID, _ := importedAlbum(t, db)
	if _, err := db.Exec(`UPDATE album_releases SET country = '["GB"]', media = '[{"number":1,"format":"CD"}]' WHERE album_id = ?`, albumID); err != nil {
		t.Fatal(err)
	}

	var tried []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/database/search"):
			q := r.URL.Query()
			tried = append(tried, q.Get("country")+"/"+q.Get("format"))
			var results []discogs.SearchResult
			if q.Get("country") == "" && q.Get("format") == "CD" {
				results = []discogs.SearchResult{{ID: 5, Title: "Daft Punk - Homework", Country: "US", Format: []string{"CD", "Album"}}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
		case r.URL.Path == "/releases/5":
			_ = json.NewEncoder(w).Encode(discogs.Release{ID: 5, Title: "Homework", Country: "US",
				Formats: []discogs.Format{{Name: "CD", Descriptions: []string{"Album"}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	client, err := discogs.New(discogs.Options{UserAgent: "UMMarr/test", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &CoverFetcher{DB: db, Discogs: client, Dir: t.TempDir()}
	if report, err := fetcher.FetchEditions(ctx, 0, false); err != nil || report.Filled != 1 {
		t.Fatalf("report = %+v (%v)", report, err)
	}
	want := []string{"GB/CD", "GB/", "/CD"}
	if strings.Join(tried, " ") != strings.Join(want, " ") {
		t.Errorf("searches tried %v, want %v", tried, want)
	}
	if edition, _ := store.GetAlbumEdition(ctx, db, albumID); edition.DiscogsReleaseID != 5 {
		t.Errorf("chose release %d", edition.DiscogsReleaseID)
	}
}

func TestRankEdition(t *testing.T) {
	uk := store.AlbumEditionCandidate{Country: "GB", Format: "CD", ReleaseYear: 1997, Year: 1997}
	hit := func(country string, year string, formats ...string) discogs.SearchResult {
		return discogs.SearchResult{Country: country, Year: year, Format: formats}
	}
	right := rankEdition(hit("GB", "1997", "CD", "Album"), uk)
	for name, worse := range map[string]discogs.SearchResult{
		"a bootleg":         hit("Russia", "2013", "CD", "Album", "Unofficial Release"),
		"the wrong medium":  hit("GB", "1997", "Cassette", "Album"),
		"the wrong country": hit("Brazil", "1997", "CD", "Album"),
		"a later reissue":   hit("GB", "2009", "CD", "Album"),
		"a promo":           hit("GB", "1997", "CD", "Album", "Promo"),
	} {
		if score := rankEdition(worse, uk); score >= right {
			t.Errorf("%s scored %d, not below the right pressing's %d", name, score, right)
		}
	}
	// With nothing known about the copy on disk, home still beats away.
	blank := store.AlbumEditionCandidate{}
	if rankEdition(hit("UK", ""), blank) <= rankEdition(hit("Japan", ""), blank) {
		t.Error("want GB preferred when MusicBrainz says nothing")
	}
	if rankEdition(hit("US", ""), blank) <= rankEdition(hit("Japan", ""), blank) {
		t.Error("want US preferred over the rest when there is no GB pressing")
	}
	// Even the worst match is still this album, so it beats no answer.
	if rankEdition(hit("Russia", "2013", "CD", "Unofficial Release"), uk) < 1 {
		t.Error("want every real hit to score at least 1")
	}
}

// A refresh is for when the picking itself has changed: the editions
// already recorded were chosen by the old rules, so they are looked up
// again rather than left alone.
func TestFetchEditionsRefresh(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_, albumID, _ := importedAlbum(t, db)
	if err := store.SetAlbumEdition(ctx, db, albumID, store.AlbumEdition{
		Label: "Wrong", Country: "Russia", DiscogsReleaseID: 7,
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/database/search"):
			_ = json.NewEncoder(w).Encode(map[string]any{"results": []discogs.SearchResult{
				{ID: 42, Title: "Daft Punk - Homework", Country: "UK", Format: []string{"CD", "Album"}},
			}})
		case r.URL.Path == "/releases/42":
			_ = json.NewEncoder(w).Encode(discogs.Release{ID: 42, Title: "Homework", Country: "UK",
				Formats: []discogs.Format{{Name: "CD", Descriptions: []string{"Album"}}},
				Labels:  []discogs.Label{{Name: "Virgin", CatalogueNo: "CDV 2821"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	client, err := discogs.New(discogs.Options{UserAgent: "UMMarr/test", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &CoverFetcher{DB: db, Discogs: client, Dir: t.TempDir()}

	// Without it, an album that already has an edition is left alone.
	if report, err := fetcher.FetchEditions(ctx, 0, false); err != nil || report.Looked != 0 {
		t.Fatalf("want nothing looked at, got %+v (%v)", report, err)
	}
	if report, err := fetcher.FetchEditions(ctx, 0, true); err != nil || report.Filled != 1 {
		t.Fatalf("refresh = %+v (%v)", report, err)
	}
	edition, err := store.GetAlbumEdition(ctx, db, albumID)
	if err != nil {
		t.Fatal(err)
	}
	if edition.DiscogsReleaseID != 42 || edition.Label != "Virgin" || edition.Country != "UK" {
		t.Errorf("edition = %+v", edition)
	}
}
