package store

import (
	"context"
	"fmt"
	"path/filepath"
)

// Posters chosen by hand, as in Plex: the choice wins over the providers'
// own images and survives a refresh.

// SetPosterOverride records the chosen poster, or clears it when url is empty.
func SetPosterOverride(ctx context.Context, q Queryer, entityType string, entityID int64, url string) error {
	if url == "" {
		_, err := q.ExecContext(ctx, `DELETE FROM poster_overrides WHERE entity_type = ? AND entity_id = ?`, entityType, entityID)
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO poster_overrides (entity_type, entity_id, url) VALUES (?, ?, ?)
		ON CONFLICT (entity_type, entity_id) DO UPDATE SET url = excluded.url`, entityType, entityID, url)
	if err != nil {
		return fmt.Errorf("set poster for %s %d: %w", entityType, entityID, err)
	}
	return nil
}

// PosterOverride reads one item's chosen poster, or "".
func PosterOverride(ctx context.Context, q Queryer, entityType string, entityID int64) string {
	var url string
	_ = q.QueryRowContext(ctx, `SELECT url FROM poster_overrides WHERE entity_type = ? AND entity_id = ?`, entityType, entityID).Scan(&url)
	return url
}

// posterOverrides reads every chosen poster of one kind, for list pages.
func posterOverrides(ctx context.Context, q Queryer, entityType string) map[int64]string {
	out := map[int64]string{}
	rows, err := q.QueryContext(ctx, `SELECT entity_id, url FROM poster_overrides WHERE entity_type = ?`, entityType)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var url string
		if rows.Scan(&id, &url) == nil {
			out[id] = url
		}
	}
	return out
}

// applyPosters puts any chosen posters onto a list of items.
func applyPosters(ctx context.Context, q Queryer, entityType string, n int, id func(int) int64, set func(int, string)) {
	if n == 0 {
		return
	}
	overrides := posterOverrides(ctx, q, entityType)
	if len(overrides) == 0 {
		return
	}
	for i := 0; i < n; i++ {
		if url, ok := overrides[id(i)]; ok {
			set(i, url)
		}
	}
}

// SetAlbumCover records which file in an album's folder is its cover, as
// a path relative to that folder. It reports whether anything changed, so
// a re-run can say what it actually did rather than counting everything
// every time.
func SetAlbumCover(ctx context.Context, q Queryer, albumID int64, relativePath string) (bool, error) {
	return setCover(ctx, q, "albums", albumID, relativePath)
}

// SetArtistCover is SetAlbumCover for an artist folder's own image.
func SetArtistCover(ctx context.Context, q Queryer, artistID int64, relativePath string) (bool, error) {
	return setCover(ctx, q, "artists", artistID, relativePath)
}

func setCover(ctx context.Context, q Queryer, table string, id int64, relativePath string) (bool, error) {
	var current string
	_ = q.QueryRowContext(ctx, `SELECT COALESCE(cover_path, '') FROM `+table+` WHERE id = ?`, id).Scan(&current)
	if current == relativePath {
		return false, nil
	}
	if _, err := q.ExecContext(ctx, `UPDATE `+table+` SET cover_path = NULLIF(?, '') WHERE id = ?`, relativePath, id); err != nil {
		return false, fmt.Errorf("set cover of %s %d: %w", table, id, err)
	}
	return true, nil
}

// CoverFile is the absolute path of an album or artist's recorded cover,
// or "" when it has none. The path is kept relative to the folder, so a
// library that moves keeps working.
func CoverFile(ctx context.Context, q Queryer, kind string, id int64) (string, error) {
	var table string
	switch kind {
	case "album":
		table = "albums"
	case "artist":
		table = "artists"
	default:
		return "", fmt.Errorf("unknown artwork kind %q", kind)
	}
	var folder, cover string
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(path, ''), COALESCE(cover_path, '') FROM `+table+` WHERE id = ?`, id).Scan(&folder, &cover); err != nil {
		return "", err
	}
	if folder != "" && cover != "" {
		return filepath.Join(folder, cover), nil
	}
	// Nothing in the folder: a cover fetched from a provider, kept in
	// UMMarr's own directory, stands in.
	if kind == "album" {
		var cached string
		_ = q.QueryRowContext(ctx, `SELECT COALESCE(cover_cache, '') FROM albums WHERE id = ?`, id).Scan(&cached)
		if cached != "" {
			return cached, nil
		}
	}
	return "", nil
}

// SetCachedCover records a cover fetched from a provider: an absolute path
// in UMMarr's own folder, and which provider it came from.
func SetCachedCover(ctx context.Context, q Queryer, albumID int64, file, source string) error {
	if _, err := q.ExecContext(ctx, `UPDATE albums SET cover_cache = NULLIF(?, ''), cover_source = NULLIF(?, '') WHERE id = ?`, file, source, albumID); err != nil {
		return fmt.Errorf("record cached cover for album %d: %w", albumID, err)
	}
	return nil
}

// AlbumsWithoutCover lists albums that have no artwork at all - neither a
// file in their folder nor one fetched before - with what to search for.
func AlbumsWithoutCover(ctx context.Context, q Queryer) ([]AlbumCoverCandidate, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT al.id, am.name, al.title, COALESCE(CAST(strftime('%Y', al.release_date) AS INTEGER), 0)
		FROM albums al JOIN artist_metadata am ON am.id = al.artist_metadata_id
		WHERE al.cover_path IS NULL AND al.cover_cache IS NULL
		  AND EXISTS (SELECT 1 FROM album_releases r JOIN tracks t ON t.album_release_id = r.id
		              WHERE r.album_id = al.id AND t.track_file_id IS NOT NULL)
		ORDER BY am.sort_name, al.title`)
	if err != nil {
		return nil, fmt.Errorf("list albums without cover: %w", err)
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

// AlbumCoverCandidate is an album with no artwork, and what to look for.
type AlbumCoverCandidate struct {
	AlbumID int64
	Artist  string
	Album   string
	Year    int
}
