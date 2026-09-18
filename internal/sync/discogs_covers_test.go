package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestBestDiscogsMatch_PrefersTheRightYear(t *testing.T) {
	candidate := store.AlbumCoverCandidate{Artist: "Portishead", Album: "Dummy", Year: 1994}
	results := []discogs.SearchResult{
		{ID: 1, Title: "Portishead - Dummy", Year: "2014"},
		{ID: 2, Title: "Portishead - Dummy", Year: "1994"},
	}
	best, ok := bestDiscogsMatch(results, candidate)
	if !ok || best.ID != 2 {
		t.Fatalf("want the 1994 pressing, got %+v (%v)", best, ok)
	}
	if _, ok := bestDiscogsMatch(nil, candidate); ok {
		t.Error("want no match from no results")
	}
}
