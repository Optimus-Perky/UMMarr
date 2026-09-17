package newznab_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
)

func TestSearch_BuildsRadarrShapedRequest(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.Write([]byte(torznabFeed))
	}))
	defer srv.Close()

	c := newznab.New(newznab.Settings{
		Implementation: newznab.Torznab, BaseURL: srv.URL + "/7/", APIPath: "/api", APIKey: "k&y",
		AdditionalParameters: "&extra=1",
	}, newznab.Options{})
	releases, err := c.Search(context.Background(), newznab.Query{
		Mode: "movie", Params: url.Values{"tmdbid": {"603"}}, Categories: []int{2000, 2040, 2000},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("want the feed's 2 releases, got %d", len(releases))
	}
	if got.URL.Path != "/7/api" {
		t.Errorf("want path /7/api, got %q", got.URL.Path)
	}
	q := got.URL.Query()
	for key, want := range map[string]string{
		"t": "movie", "cat": "2000,2040", "extended": "1", "tmdbid": "603", "apikey": "k&y", "extra": "1", "limit": "100", "offset": "0",
	} {
		if q.Get(key) != want {
			t.Errorf("%s: want %q, got %q (query %s)", key, want, q.Get(key), got.URL.RawQuery)
		}
	}
}

func TestSearch_NoCategoriesAsksNothing(t *testing.T) {
	c := newznab.New(newznab.Settings{BaseURL: "http://example.invalid"}, newznab.Options{})
	releases, err := c.Search(context.Background(), newznab.Query{Mode: "search"})
	if err != nil || releases != nil {
		t.Fatalf("want nothing asked and nothing returned, got %v %v", releases, err)
	}
}

func TestSearch_HTTPErrorKeepsAPIKeyOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c := newznab.New(newznab.Settings{BaseURL: srv.URL, APIKey: "very-secret"}, newznab.Options{})
	_, err := c.Search(context.Background(), newznab.Query{Categories: []int{2000}})
	if err == nil || strings.Contains(err.Error(), "very-secret") {
		t.Fatalf("want an error without the API key in it, got %v", err)
	}

	unreachable := newznab.New(newznab.Settings{BaseURL: "http://127.0.0.1:1", APIKey: "very-secret"}, newznab.Options{})
	_, err = unreachable.Search(context.Background(), newznab.Query{Categories: []int{2000}})
	if err == nil || strings.Contains(err.Error(), "very-secret") || strings.Contains(err.Error(), "/api?") ||
		!strings.HasPrefix(err.Error(), "couldn't reach the indexer: ") {
		t.Fatalf("want a short connection error without the request URL or API key, got %v", err)
	}
}

func TestFetchRelease(t *testing.T) {
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "magnet:?xt=urn:btih:abcdef")
		w.WriteHeader(http.StatusMovedPermanently)
	}))
	defer redirect.Close()
	torrentBytes := []byte("d8:announce...e")
	file := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(torrentBytes)
	}))
	defer file.Close()

	c := newznab.New(newznab.Settings{Implementation: newznab.Torznab}, newznab.Options{})
	ctx := context.Background()

	fetched, err := c.FetchRelease(ctx, newznab.Release{Title: "x", DownloadURL: redirect.URL, Protocol: newznab.ProtocolTorrent})
	if err != nil || fetched.Kind != newznab.KindMagnet || fetched.MagnetURI != "magnet:?xt=urn:btih:abcdef" {
		t.Errorf("redirect to magnet: got %+v, %v", fetched, err)
	}
	fetched, err = c.FetchRelease(ctx, newznab.Release{Title: "Movie", DownloadURL: file.URL, Protocol: newznab.ProtocolTorrent})
	if err != nil || fetched.Kind != newznab.KindTorrentFile || string(fetched.Data) != string(torrentBytes) || fetched.FileName != "Movie.torrent" {
		t.Errorf("torrent file: got %+v, %v", fetched, err)
	}
	fetched, err = c.FetchRelease(ctx, newznab.Release{Title: "x", MagnetURL: "magnet:?xt=urn:btih:already", DownloadURL: "http://example.invalid", Protocol: newznab.ProtocolTorrent})
	if err != nil || fetched.MagnetURI != "magnet:?xt=urn:btih:already" {
		t.Errorf("magnet needs no request: got %+v, %v", fetched, err)
	}
}
