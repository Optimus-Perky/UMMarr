package store

import (
	"context"
	"database/sql"
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

// AlbumsWithoutEdition lists albums with files but no edition detail yet.
func AlbumsWithoutEdition(ctx context.Context, q Queryer) ([]AlbumCoverCandidate, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT al.id, am.name, al.title, COALESCE(CAST(strftime('%Y', al.release_date) AS INTEGER), 0)
		FROM albums al JOIN artist_metadata am ON am.id = al.artist_metadata_id
		WHERE al.discogs_release_id IS NULL
		  AND EXISTS (SELECT 1 FROM album_releases r JOIN tracks t ON t.album_release_id = r.id
		              WHERE r.album_id = al.id AND t.track_file_id IS NOT NULL)
		ORDER BY am.sort_name, al.title`)
	if err != nil {
		return nil, fmt.Errorf("list albums without edition: %w", err)
	}
	defer rows.Close()
	var out []AlbumCoverCandidate
	for rows.Next() {
		var c AlbumCoverCandidate
		if err := rows.Scan(&c.AlbumID, &c.Artist, &c.Album, &c.Year); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
