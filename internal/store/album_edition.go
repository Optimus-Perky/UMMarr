package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// What pressing an album is: the label, catalogue number and format
// descriptions that tell one edition from another. MusicBrainz has the
// country and date; this is the detail Discogs adds.

// AlbumEdition describes the copy the library holds.
type AlbumEdition struct {
	Label     string
	Catalogue string
	// Format is the medium and its descriptions joined, e.g.
	// "CD, Album, Remastered" or "2xVinyl, LP, 180g".
	Format           string
	Country          string
	Year             int
	DiscogsReleaseID int64
}

// Empty reports whether nothing is known about the edition.
func (e AlbumEdition) Empty() bool { return e == AlbumEdition{} }

// Summary is the edition on one line, for the album page.
func (e AlbumEdition) Summary() string {
	var parts []string
	if e.Format != "" {
		parts = append(parts, e.Format)
	}
	if e.Label != "" {
		label := e.Label
		if e.Catalogue != "" {
			label += " " + e.Catalogue
		}
		parts = append(parts, label)
	} else if e.Catalogue != "" {
		parts = append(parts, e.Catalogue)
	}
	if e.Country != "" {
		parts = append(parts, e.Country)
	}
	if e.Year > 0 {
		parts = append(parts, strconv.Itoa(e.Year))
	}
	return strings.Join(parts, " · ")
}

// DiscogsURL links to the release this came from, or "".
func (e AlbumEdition) DiscogsURL() string {
	if e.DiscogsReleaseID == 0 {
		return ""
	}
	return "https://www.discogs.com/release/" + strconv.FormatInt(e.DiscogsReleaseID, 10)
}

// SetAlbumEdition records what pressing an album is.
func SetAlbumEdition(ctx context.Context, q Queryer, albumID int64, e AlbumEdition) error {
	_, err := q.ExecContext(ctx, `
		UPDATE albums SET edition_label = NULLIF(?, ''), edition_catalogue = NULLIF(?, ''),
		                  edition_format = NULLIF(?, ''), edition_country = NULLIF(?, ''),
		                  edition_year = NULLIF(?, 0), discogs_release_id = NULLIF(?, 0)
		WHERE id = ?`,
		e.Label, e.Catalogue, e.Format, e.Country, e.Year, e.DiscogsReleaseID, albumID)
	if err != nil {
		return fmt.Errorf("set edition of album %d: %w", albumID, err)
	}
	return nil
}

// GetAlbumEdition reads it back.
func GetAlbumEdition(ctx context.Context, q Queryer, albumID int64) (AlbumEdition, error) {
	var e AlbumEdition
	var label, catalogue, format, country sql.NullString
	var year, discogsID sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT edition_label, edition_catalogue, edition_format, edition_country, edition_year, discogs_release_id
		FROM albums WHERE id = ?`, albumID).Scan(&label, &catalogue, &format, &country, &year, &discogsID)
	if err != nil {
		return e, fmt.Errorf("get edition of album %d: %w", albumID, err)
	}
	e.Label, e.Catalogue = label.String, catalogue.String
	e.Format, e.Country = format.String, country.String
	e.Year, e.DiscogsReleaseID = int(year.Int64), discogsID.Int64
	return e, nil
}

// AlbumEditionCandidate is an album to look up, together with what
// MusicBrainz already settled about the copy on disk. Discogs holds
// hundreds of pressings of a popular album and ranks them by nothing in
// particular, so without this a search happily returns a Russian bootleg
// cassette for a UK CD.
type AlbumEditionCandidate struct {
	AlbumID int64
	Artist  string
	Album   string
	// Year is the album's own first-release year.
	Year int
	// Country is the chosen release's country, e.g. "GB". Empty when
	// MusicBrainz doesn't say or the release is worldwide.
	Country string
	// Format is its medium in Discogs' vocabulary: CD, Vinyl, Cassette.
	Format string
	// TrackCount and ReleaseYear come from the chosen release too.
	TrackCount  int
	ReleaseYear int
}

// AlbumsWithoutEditionDetail lists albums with files but no edition yet.
func AlbumsWithoutEditionDetail(ctx context.Context, q Queryer) ([]AlbumEditionCandidate, error) {
	return albumLookupCandidates(ctx, q, "al.discogs_release_id IS NULL")
}

// albumLookupCandidates lists albums missing something Discogs can supply,
// each with its monitored release's country, format and track count. Only
// albums with files are listed: an album nobody has is not worth a lookup.
func albumLookupCandidates(ctx context.Context, q Queryer, missing string) ([]AlbumEditionCandidate, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT al.id, am.name, al.title,
		       COALESCE(CAST(strftime('%Y', al.release_date) AS INTEGER), 0),
		       COALESCE(r.country, ''), COALESCE(r.media, ''),
		       COALESCE(r.track_count, 0),
		       COALESCE(CAST(strftime('%Y', r.release_date) AS INTEGER), 0)
		FROM albums al
		JOIN artist_metadata am ON am.id = al.artist_metadata_id
		LEFT JOIN album_releases r ON r.id = (
		    SELECT r2.id FROM album_releases r2
		    WHERE r2.album_id = al.id
		    ORDER BY r2.monitored DESC, r2.id LIMIT 1)
		WHERE `+missing+`
		  AND EXISTS (SELECT 1 FROM album_releases r3 JOIN tracks t ON t.album_release_id = r3.id
		              WHERE r3.album_id = al.id AND t.track_file_id IS NOT NULL)
		ORDER BY am.sort_name, al.title`)
	if err != nil {
		return nil, fmt.Errorf("list albums to look up: %w", err)
	}
	defer rows.Close()
	var out []AlbumEditionCandidate
	for rows.Next() {
		var c AlbumEditionCandidate
		var countryJSON, mediaJSON string
		if err := rows.Scan(&c.AlbumID, &c.Artist, &c.Album, &c.Year, &countryJSON, &mediaJSON, &c.TrackCount, &c.ReleaseYear); err != nil {
			return nil, err
		}
		c.Country = firstJSONString(countryJSON)
		c.Format = discogsFormat(firstMediumFormat(mediaJSON))
		out = append(out, c)
	}
	return out, rows.Err()
}

// firstJSONString reads the first entry of a JSON string array column.
func firstJSONString(raw string) string {
	var list []string
	if json.Unmarshal([]byte(raw), &list) != nil || len(list) == 0 {
		return ""
	}
	return list[0]
}

// firstMediumFormat reads the format of the first medium, e.g. "12\" Vinyl".
func firstMediumFormat(raw string) string {
	var media []struct {
		Format string `json:"format"`
	}
	if json.Unmarshal([]byte(raw), &media) != nil {
		return ""
	}
	for _, m := range media {
		if m.Format != "" {
			return m.Format
		}
	}
	return ""
}

// discogsFormat translates MusicBrainz's medium names into the words
// Discogs searches by. MusicBrainz is specific where Discogs is broad -
// "12\" Vinyl", "Hybrid SACD" and "Digital Media" all narrow to one
// Discogs format - and anything unrecognised is left out rather than
// guessed, since a wrong filter finds nothing at all.
func discogsFormat(medium string) string {
	m := strings.ToLower(medium)
	switch {
	case strings.Contains(m, "vinyl"), strings.Contains(m, "lp"):
		return "Vinyl"
	case strings.Contains(m, "cassette"):
		return "Cassette"
	case strings.Contains(m, "digital"), strings.Contains(m, "file"):
		return "File"
	case strings.Contains(m, "sacd"):
		return "SACD"
	case strings.Contains(m, "dvd"):
		return "DVD"
	case strings.Contains(m, "cd"):
		return "CD"
	}
	return ""
}
