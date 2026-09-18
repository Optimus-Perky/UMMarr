package musicbrainz

import "fmt"

// Artist matches a MusicBrainz artist entity.
type Artist struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	SortName       string `json:"sort-name"`
	Disambiguation string `json:"disambiguation"`
	Type           string `json:"type"`
	// LifeSpan.Ended is how MusicBrainz says a band has split up or a
	// person has died - the music answer to a series being Ended, which
	// the library's Active/Ended filter reads. A lookup returns it; a
	// search result usually doesn't.
	LifeSpan struct {
		Begin string `json:"begin"`
		End   string `json:"end"`
		Ended bool   `json:"ended"`
	} `json:"life-span"`
}

// Status is the artist's life-span as one word, in the vocabulary the
// library uses: "ended" when MusicBrainz has ended them, otherwise
// "active". Empty when this came from a search result, which carries no
// life-span at all - an unknown status must not read as active.
func (a Artist) Status() string {
	switch {
	case a.LifeSpan.Ended || a.LifeSpan.End != "":
		return "ended"
	case a.LifeSpan.Begin != "":
		return "active"
	}
	return ""
}

// ArtistSearchResponse is the body of GET /artist?query=.
type ArtistSearchResponse struct {
	Artists []Artist `json:"artists"`
}

// SeriesTarget is the "series" sub-object of a relation whose
// target-type is "series".
type SeriesTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Relation is one entry in a release-group's "relations" array (present
// when the request includes inc=series-rels). AttributeValues carries the
// series position under the "number" key for release_group-series
// relations - see SeriesInfo below.
type Relation struct {
	TargetType      string            `json:"target-type"`
	Type            string            `json:"type"`
	Series          *SeriesTarget     `json:"series,omitempty"`
	AttributeValues map[string]string `json:"attribute-values,omitempty"`
}

// ReleaseRef is one entry in ReleaseGroup.Releases - a brief pointer to
// one of the release-group's specific pressings/editions, populated
// because GetReleaseGroup requests inc=releases. Lets a caller pick a
// representative release (e.g. prefer Status == "Official") without a
// separate search call before fetching its full track listing via
// Client.GetRelease.
type ReleaseRef struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	Date           string `json:"date"`
	Country        string `json:"country"`
	Disambiguation string `json:"disambiguation"`
	// Media is only filled in when the request asks for inc=media, which
	// ListReleases does: its track count is how an edition is told apart
	// from another - 13 tracks for a UK release, 16 for a Japanese one.
	Media []struct {
		Format     string `json:"format"`
		TrackCount int    `json:"track-count"`
	} `json:"media,omitempty"`
}

// TrackCount totals the tracks across all of the release's media.
func (r ReleaseRef) TrackCount() int {
	n := 0
	for _, m := range r.Media {
		n += m.TrackCount
	}
	return n
}

// Format names the media, e.g. "CD" or "2xCD".
func (r ReleaseRef) Format() string {
	if len(r.Media) == 0 {
		return ""
	}
	if len(r.Media) == 1 {
		return r.Media[0].Format
	}
	return fmt.Sprintf("%dx%s", len(r.Media), r.Media[0].Format)
}

// ReleaseGroup matches a MusicBrainz release-group entity (UMMarr's
// "Album"). Relations and Releases are only populated when the request
// includes inc=series-rels / inc=releases respectively.
type ReleaseGroup struct {
	ID               string       `json:"id"`
	Title            string       `json:"title"`
	Disambiguation   string       `json:"disambiguation"`
	PrimaryType      string       `json:"primary-type"`
	SecondaryTypes   []string     `json:"secondary-types"`
	FirstReleaseDate string       `json:"first-release-date"`
	Relations        []Relation   `json:"relations,omitempty"`
	Releases         []ReleaseRef `json:"releases,omitempty"`
}

// ReleaseGroupBrowseResponse is the body of GET /release-group?artist=.
type ReleaseGroupBrowseResponse struct {
	ReleaseGroups []ReleaseGroup `json:"release-groups"`
	Count         int            `json:"release-group-count"`
}

// ArtistCredit is one entry in a track/release's "artist-credit" array -
// each entry names one contributing artist, which is how Various Artists
// compilations expose per-track artists rather than one album-level artist.
type ArtistCredit struct {
	Name   string `json:"name"`
	Artist Artist `json:"artist"`
}

// Track matches a MusicBrainz recording as it appears within a medium's
// track listing.
type Track struct {
	ID           string         `json:"id"`
	Number       string         `json:"number"`
	Title        string         `json:"title"`
	Length       int            `json:"length"` // milliseconds
	ArtistCredit []ArtistCredit `json:"artist-credit,omitempty"`
}

// Medium matches one disc/medium within a release (e.g. "CD 1").
type Medium struct {
	Format     string  `json:"format"`
	Position   int     `json:"position"`
	TrackCount int     `json:"track-count"`
	Tracks     []Track `json:"tracks"`
}

// LabelInfo is one entry in Release.LabelInfo - populated when GetRelease
// requests inc=labels.
type LabelInfo struct {
	Label struct {
		Name string `json:"name"`
	} `json:"label"`
}

// Release matches a MusicBrainz release (a specific pressing/edition of a
// release-group) - UMMarr's "AlbumRelease".
type Release struct {
	ID             string         `json:"id"`
	Title          string         `json:"title"`
	Status         string         `json:"status"`
	Disambiguation string         `json:"disambiguation"`
	Date           string         `json:"date"`
	Country        string         `json:"country"`
	LabelInfo      []LabelInfo    `json:"label-info,omitempty"`
	Media          []Medium       `json:"media"`
	ArtistCredit   []ArtistCredit `json:"artist-credit"`
}
