package newznab

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Protocols a release can be downloaded with.
const (
	ProtocolTorrent = "torrent"
	ProtocolUsenet  = "usenet"
)

// Flags are Radarr's indexer flags (NzbDrone.Core.Parser.Model.IndexerFlags),
// with the same values, so they round-trip through the Radarr-compatible API.
type Flags int

const (
	FlagFreeleech    Flags = 1
	FlagHalfleech    Flags = 2
	FlagDoubleUpload Flags = 4
	FlagInternal     Flags = 32
	FlagScene        Flags = 128
	FlagFreeleech75  Flags = 256
	FlagFreeleech25  Flags = 512
	FlagNuked        Flags = 2048
)

// FlagNames lists the flags an indexer can require, as Radarr names them.
var FlagNames = []struct {
	Flag Flags
	Name string
}{
	{FlagFreeleech, "Freeleech"}, {FlagHalfleech, "Halfleech"}, {FlagDoubleUpload, "Double Upload"},
	{FlagInternal, "Internal"}, {FlagScene, "Scene"}, {FlagFreeleech75, "Freeleech 75%"},
	{FlagFreeleech25, "Freeleech 25%"}, {FlagNuked, "Nuked"},
}

// Release is one item from an indexer's feed.
type Release struct {
	GUID        string
	Title       string
	DownloadURL string
	MagnetURL   string
	InfoURL     string
	InfoHash    string
	Protocol    string
	Size        int64
	// Seeders and Peers are -1 when the indexer didn't say.
	Seeders     int
	Peers       int
	PublishDate time.Time
	Categories  []int
	IMDbID      int
	TMDbID      int
	TVDBID      int
	Flags       Flags

	// Set by whoever queried the indexer, not parsed from the feed.
	IndexerID       int64
	Indexer         string
	IndexerPriority int
}

// Leechers is Peers minus Seeders, or -1 when either is unknown.
func (r Release) Leechers() int {
	if r.Seeders < 0 || r.Peers < 0 || r.Peers < r.Seeders {
		return -1
	}
	return r.Peers - r.Seeders
}

// APIError is an <error code="..." description="..."/> response.
type APIError struct {
	Code        int
	Description string
}

func (e *APIError) Error() string {
	switch {
	case e.Code >= 100 && e.Code <= 199:
		return fmt.Sprintf("invalid API key (%s)", e.Description)
	case strings.EqualFold(e.Description, "Request limit reached"):
		return "API request limit reached"
	default:
		return fmt.Sprintf("indexer error %d: %s", e.Code, e.Description)
	}
}

type feedAttr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type feedItem struct {
	Title    string `xml:"title"`
	GUID     string `xml:"guid"`
	Link     string `xml:"link"`
	Comments string `xml:"comments"`
	PubDate  string `xml:"pubDate"`
	Size     string `xml:"size"`
	// Attrs match torznab:attr and newznab:attr alike (no namespace given).
	Attrs     []feedAttr `xml:"attr"`
	Enclosure struct {
		URL    string `xml:"url,attr"`
		Length string `xml:"length,attr"`
		Type   string `xml:"type,attr"`
	} `xml:"enclosure"`
}

type feedDoc struct {
	XMLName     xml.Name
	Code        string     `xml:"code,attr"`
	Description string     `xml:"description,attr"`
	Items       []feedItem `xml:"channel>item"`
}

// checkError returns the APIError in body if it is an <error> document.
func checkError(body []byte) error {
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")))
	var doc struct {
		XMLName     xml.Name
		Code        string `xml:"code,attr"`
		Description string `xml:"description,attr"`
	}
	if err := xml.Unmarshal(trimmed, &doc); err != nil || doc.XMLName.Local != "error" {
		return nil
	}
	code, _ := strconv.Atoi(doc.Code)
	return &APIError{Code: code, Description: doc.Description}
}

// ParseFeed reads a Torznab or Newznab RSS feed. protocol is the indexer's
// (Torznab indexers are torrent, Newznab usenet).
func ParseFeed(body []byte, protocol string) ([]Release, error) {
	if err := checkError(body); err != nil {
		return nil, err
	}
	var doc feedDoc
	dec := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))))
	dec.Strict = false
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("read indexer feed: %w", err)
	}
	if doc.XMLName.Local != "rss" {
		return nil, fmt.Errorf("read indexer feed: expected an RSS feed, got <%s>", doc.XMLName.Local)
	}
	releases := make([]Release, 0, len(doc.Items))
	for _, item := range doc.Items {
		releases = append(releases, item.release(protocol))
	}
	return releases, nil
}

func (it feedItem) attr(name string) string {
	for _, a := range it.Attrs {
		if strings.EqualFold(a.Name, name) {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

func (it feedItem) attrs(name string) []string {
	var out []string
	for _, a := range it.Attrs {
		if strings.EqualFold(a.Name, name) {
			out = append(out, strings.TrimSpace(a.Value))
		}
	}
	return out
}

func atoiOr(s string, fallback int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int(f)
	}
	return fallback
}

func (it feedItem) release(protocol string) Release {
	r := Release{
		GUID:     strings.TrimSpace(it.GUID),
		Title:    strings.TrimSpace(it.Title),
		Protocol: protocol,
		InfoHash: it.attr("infohash"),
		Seeders:  -1,
		Peers:    -1,
	}
	link := strings.TrimSpace(it.Link)
	if link == "" {
		link = strings.TrimSpace(it.Enclosure.URL)
	}
	if strings.HasPrefix(link, "magnet:") {
		r.MagnetURL = link
	} else {
		r.DownloadURL = link
	}
	if magnet := it.attr("magneturl"); magnet != "" {
		r.MagnetURL = magnet
	}
	if r.GUID == "" {
		r.GUID = link
	}
	r.InfoURL = strings.TrimSuffix(strings.TrimSpace(it.Comments), "#comments")

	for _, s := range []string{it.attr("size"), strings.TrimSpace(it.Size), it.Enclosure.Length} {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 {
			r.Size = n
			break
		}
	}
	if s := it.attr("seeders"); s != "" {
		r.Seeders = atoiOr(s, -1)
	}
	if p := it.attr("peers"); p != "" {
		r.Peers = atoiOr(p, -1)
	} else if l := it.attr("leechers"); l != "" && r.Seeders >= 0 {
		if n := atoiOr(l, -1); n >= 0 {
			r.Peers = r.Seeders + n
		}
	}
	r.PublishDate = parsePubDate(it.PubDate)
	for _, c := range it.attrs("category") {
		if n := atoiOr(c, 0); n > 0 {
			r.Categories = append(r.Categories, n)
		}
	}
	imdb := it.attr("imdb")
	if imdb == "" {
		imdb = it.attr("imdbid")
	}
	r.IMDbID = atoiOr(strings.TrimPrefix(strings.ToLower(imdb), "tt"), 0)
	r.TMDbID = atoiOr(it.attr("tmdbid"), 0)
	r.TVDBID = atoiOr(it.attr("tvdbid"), 0)
	r.Flags = it.flags()
	return r
}

// flags follows Radarr's TorznabRssParser.GetFlags.
func (it feedItem) flags() Flags {
	var f Flags
	switch it.attr("downloadvolumefactor") {
	case "0", "0.0":
		f |= FlagFreeleech
	case "0.5":
		f |= FlagHalfleech
	case "0.25":
		f |= FlagFreeleech75
	case "0.75":
		f |= FlagFreeleech25
	}
	if v := it.attr("uploadvolumefactor"); v == "2" || v == "2.0" {
		f |= FlagDoubleUpload
	}
	for _, tag := range it.attrs("tag") {
		switch strings.ToLower(tag) {
		case "internal":
			f |= FlagInternal
		case "scene":
			f |= FlagScene
		}
	}
	return f
}

var pubDateLayouts = []string{
	time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST",
	"2 Jan 2006 15:04:05 -0700", time.RFC3339,
}

func parsePubDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range pubDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Caps is what an indexer's t=caps says it supports.
type Caps struct {
	// Each mode's supported parameters; nil when the mode isn't available.
	Search, TVSearch, MovieSearch, MusicSearch []string
	Categories                                 []Category
}

// Supports reports whether mode ("search", "tvsearch", "movie", "music")
// is available with param.
func (c Caps) Supports(mode, param string) bool {
	var params []string
	switch mode {
	case "search":
		params = c.Search
	case "tvsearch":
		params = c.TVSearch
	case "movie":
		params = c.MovieSearch
	case "music":
		params = c.MusicSearch
	}
	for _, p := range params {
		if p == param {
			return true
		}
	}
	return false
}

type capsMode struct {
	Available       string `xml:"available,attr"`
	SupportedParams string `xml:"supportedParams,attr"`
}

type capsDoc struct {
	XMLName   xml.Name
	Searching struct {
		Search      *capsMode `xml:"search"`
		TVSearch    *capsMode `xml:"tv-search"`
		MovieSearch *capsMode `xml:"movie-search"`
		AudioSearch *capsMode `xml:"audio-search"`
		MusicSearch *capsMode `xml:"music-search"`
	} `xml:"searching"`
	Categories []struct {
		ID      int    `xml:"id,attr"`
		Name    string `xml:"name,attr"`
		Subcats []struct {
			ID   int    `xml:"id,attr"`
			Name string `xml:"name,attr"`
		} `xml:"subcat"`
	} `xml:"categories>category"`
}

func (m *capsMode) params() []string {
	if m == nil || !strings.EqualFold(m.Available, "yes") {
		return nil
	}
	// Radarr treats a mode with no supportedParams as supporting q.
	if strings.TrimSpace(m.SupportedParams) == "" {
		return []string{"q"}
	}
	var out []string
	for _, p := range strings.Split(m.SupportedParams, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseCaps reads a t=caps response.
func ParseCaps(body []byte) (Caps, error) {
	if err := checkError(body); err != nil {
		return Caps{}, err
	}
	var doc capsDoc
	if err := xml.Unmarshal(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")), &doc); err != nil {
		return Caps{}, fmt.Errorf("read indexer capabilities: %w", err)
	}
	if doc.XMLName.Local != "caps" {
		return Caps{}, fmt.Errorf("read indexer capabilities: expected <caps>, got <%s>", doc.XMLName.Local)
	}
	caps := Caps{
		Search:      doc.Searching.Search.params(),
		TVSearch:    doc.Searching.TVSearch.params(),
		MovieSearch: doc.Searching.MovieSearch.params(),
		MusicSearch: doc.Searching.MusicSearch.params(),
	}
	if caps.MusicSearch == nil {
		caps.MusicSearch = doc.Searching.AudioSearch.params()
	}
	for _, c := range doc.Categories {
		caps.Categories = append(caps.Categories, Category{ID: c.ID, Name: c.Name})
		for _, s := range c.Subcats {
			caps.Categories = append(caps.Categories, Category{ID: s.ID, Name: c.Name + "/" + s.Name})
		}
	}
	return caps, nil
}
