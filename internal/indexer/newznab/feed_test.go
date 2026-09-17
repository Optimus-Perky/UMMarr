package newznab_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
)

const torznabFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:torznab="http://torznab.com/schemas/2015/feed">
  <channel>
    <title>Example Tracker</title>
    <item>
      <title>The Matrix 1999 1080p BluRay x264-GROUP</title>
      <guid>https://tracker.example/details/42</guid>
      <link>https://prowlarr.example/7/download?apikey=secret&amp;link=abc</link>
      <comments>https://tracker.example/details/42#comments</comments>
      <pubDate>Sat, 14 Mar 2015 17:10:42 -0400</pubDate>
      <size>8123456789</size>
      <enclosure url="https://prowlarr.example/7/download?apikey=secret&amp;link=abc" length="8123456789" type="application/x-bittorrent"/>
      <torznab:attr name="category" value="2000"/>
      <torznab:attr name="category" value="2040"/>
      <torznab:attr name="seeders" value="12"/>
      <torznab:attr name="peers" value="15"/>
      <torznab:attr name="imdb" value="0133093"/>
      <torznab:attr name="tmdbid" value="603"/>
      <torznab:attr name="infohash" value="63e07ff523710ca268567dad344ce1e0e6b7e8a3"/>
      <torznab:attr name="downloadvolumefactor" value="0"/>
      <torznab:attr name="uploadvolumefactor" value="2"/>
      <torznab:attr name="tag" value="internal"/>
    </item>
    <item>
      <title>Show S01E02 720p HDTV</title>
      <guid>tracker-43</guid>
      <link>magnet:?xt=urn:btih:abcdef</link>
      <pubDate>Mon, 2 Mar 2015 09:00:00 +0000</pubDate>
      <torznab:attr name="size" value="1000"/>
      <torznab:attr name="seeders" value="3"/>
      <torznab:attr name="leechers" value="4"/>
      <torznab:attr name="tvdbid" value="273181"/>
      <torznab:attr name="category" value="5040"/>
    </item>
  </channel>
</rss>`

const newznabFeed = `<?xml version="1.0" encoding="utf-8"?>
<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
  <channel>
    <item>
      <title>White.Collar.S03E05.720p.HDTV.X264-DIMENSION</title>
      <guid isPermaLink="true">https://nzb.example/details/2496</guid>
      <link>https://nzb.example/getnzb/2496.nzb&amp;r=xxx</link>
      <pubDate>Mon, 27 Feb 2012 11:09:39 -0500</pubDate>
      <enclosure url="https://nzb.example/getnzb/2496.nzb&amp;r=xxx" length="1183105773" type="application/x-nzb"/>
      <newznab:attr name="category" value="5040"/>
    </item>
  </channel>
</rss>`

func TestParseFeed_Torznab(t *testing.T) {
	releases, err := newznab.ParseFeed([]byte(torznabFeed), newznab.ProtocolTorrent)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("want 2 releases, got %d", len(releases))
	}
	r := releases[0]
	checks := []struct {
		name      string
		got, want any
	}{
		{"title", r.Title, "The Matrix 1999 1080p BluRay x264-GROUP"},
		{"guid", r.GUID, "https://tracker.example/details/42"},
		{"download", r.DownloadURL, "https://prowlarr.example/7/download?apikey=secret&link=abc"},
		{"info", r.InfoURL, "https://tracker.example/details/42"},
		{"size", r.Size, int64(8123456789)},
		{"seeders", r.Seeders, 12},
		{"peers", r.Peers, 15},
		{"leechers", r.Leechers(), 3},
		{"imdb", r.IMDbID, 133093},
		{"tmdb", r.TMDbID, 603},
		{"infohash", r.InfoHash, "63e07ff523710ca268567dad344ce1e0e6b7e8a3"},
		{"flags", r.Flags, newznab.FlagFreeleech | newznab.FlagDoubleUpload | newznab.FlagInternal},
		{"categories", len(r.Categories), 2},
		{"protocol", r.Protocol, newznab.ProtocolTorrent},
		{"published", r.PublishDate.UTC(), time.Date(2015, 3, 14, 21, 10, 42, 0, time.UTC)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: want %v, got %v", c.name, c.want, c.got)
		}
	}

	m := releases[1]
	if m.MagnetURL != "magnet:?xt=urn:btih:abcdef" || m.DownloadURL != "" {
		t.Errorf("want a magnet link kept as the magnet, got magnet %q download %q", m.MagnetURL, m.DownloadURL)
	}
	if m.Size != 1000 || m.Seeders != 3 || m.Peers != 7 || m.TVDBID != 273181 {
		t.Errorf("want size 1000, 3 seeders, 7 peers (seeders + leechers), tvdb 273181, got %+v", m)
	}
}

func TestParseFeed_NewznabUsesEnclosureLength(t *testing.T) {
	releases, err := newznab.ParseFeed([]byte(newznabFeed), newznab.ProtocolUsenet)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(releases) != 1 || releases[0].Size != 1183105773 || releases[0].Seeders != -1 || releases[0].Protocol != newznab.ProtocolUsenet {
		t.Fatalf("want one usenet release sized from its enclosure with unknown seeders, got %+v", releases)
	}
}

func TestParseFeed_ErrorDocument(t *testing.T) {
	for _, tc := range []struct {
		body string
		want string
	}{
		{"\xef\xbb\xbf<?xml version=\"1.0\"?>\n<error code=\"100\" description=\"Incorrect user credentials\"/>", "invalid API key (Incorrect user credentials)"},
		{`<error code="500" description="Request limit reached"/>`, "API request limit reached"},
		{`<error code="201" description="Incorrect parameter"/>`, "indexer error 201: Incorrect parameter"},
	} {
		_, err := newznab.ParseFeed([]byte(tc.body), newznab.ProtocolTorrent)
		var apiErr *newznab.APIError
		if !errors.As(err, &apiErr) || err.Error() != tc.want {
			t.Errorf("want %q, got %v", tc.want, err)
		}
	}
}

func TestParseFeed_NotAFeed(t *testing.T) {
	if _, err := newznab.ParseFeed([]byte(`<html><body>login</body></html>`), newznab.ProtocolTorrent); err == nil {
		t.Fatalf("want an error for a page that isn't a feed")
	}
}

func TestParseCaps(t *testing.T) {
	caps, err := newznab.ParseCaps([]byte(`<caps>
  <searching>
    <search available="yes" supportedParams="q"/>
    <tv-search available="yes" supportedParams="q,season,ep,tvdbid"/>
    <movie-search available="yes" supportedParams="q,imdbid,tmdbid"/>
    <audio-search available="no"/>
    <music-search available="yes"/>
  </searching>
  <categories>
    <category id="2000" name="Movies"><subcat id="2040" name="HD"/></category>
  </categories>
</caps>`))
	if err != nil {
		t.Fatalf("parse caps: %v", err)
	}
	if !caps.Supports("tvsearch", "tvdbid") || !caps.Supports("movie", "tmdbid") || caps.Supports("movie", "tvdbid") {
		t.Errorf("want tvsearch tvdbid and movie tmdbid supported, movie tvdbid not, got %+v", caps)
	}
	if !caps.Supports("music", "q") {
		t.Errorf("want a mode with no supportedParams to support q, got %+v", caps.MusicSearch)
	}
	if len(caps.Categories) != 2 || caps.Categories[1].Name != "Movies/HD" {
		t.Errorf("want Movies and Movies/HD, got %+v", caps.Categories)
	}
}
