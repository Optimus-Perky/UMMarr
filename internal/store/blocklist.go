package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// BlocklistEntry is a release that failed and shouldn't be grabbed again
// for the same library item - see migration 00038.
type BlocklistEntry struct {
	ID            int64
	MovieID       sql.NullInt64
	SeriesID      sql.NullInt64
	SeasonNumber  sql.NullInt64
	EpisodeNumber sql.NullInt64
	AlbumID       sql.NullInt64
	SourceTitle   string
	Quality       string
	Protocol      string
	Indexer       string
	IndexerID     sql.NullInt64
	InfoHash      sql.NullString
	Size          sql.NullInt64
	Published     sql.NullTime
	Message       string
	Added         time.Time

	// ItemTitle names the movie, series or album; filled by ListBlocklist.
	ItemTitle string
}

const blocklistColumns = `b.id, b.movie_id, b.series_id, b.season_number, b.episode_number, b.album_id,
	b.source_title, b.quality, b.protocol, b.indexer, b.indexer_id, b.info_hash, b.size, b.published, b.message, b.added`

func scanBlocklist(row interface{ Scan(...any) error }, extra ...any) (BlocklistEntry, error) {
	var b BlocklistEntry
	dest := []any{&b.ID, &b.MovieID, &b.SeriesID, &b.SeasonNumber, &b.EpisodeNumber, &b.AlbumID,
		&b.SourceTitle, &b.Quality, &b.Protocol, &b.Indexer, &b.IndexerID, &b.InfoHash, &b.Size, &b.Published, &b.Message, &b.Added}
	err := row.Scan(append(dest, extra...)...)
	return b, err
}

// AddBlocklist records a failed release. A release already blocklisted for
// the same item isn't added twice.
func AddBlocklist(ctx context.Context, q Queryer, b BlocklistEntry) (int64, error) {
	var existing int64
	err := q.QueryRowContext(ctx, `
		SELECT id FROM blocklist
		WHERE source_title = ? AND indexer = ?
		  AND movie_id IS ? AND series_id IS ? AND album_id IS ?
		  AND season_number IS ? AND episode_number IS ?`,
		b.SourceTitle, b.Indexer, b.MovieID, b.SeriesID, b.AlbumID, b.SeasonNumber, b.EpisodeNumber).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("check blocklist: %w", err)
	}
	var hash sql.NullString
	if b.InfoHash.Valid && b.InfoHash.String != "" {
		hash = sql.NullString{String: strings.ToLower(b.InfoHash.String), Valid: true}
	}
	res, err := q.ExecContext(ctx, `
		INSERT INTO blocklist (movie_id, series_id, season_number, episode_number, album_id,
			source_title, quality, protocol, indexer, indexer_id, info_hash, size, published, message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.MovieID, b.SeriesID, b.SeasonNumber, b.EpisodeNumber, b.AlbumID,
		b.SourceTitle, b.Quality, b.Protocol, b.Indexer, b.IndexerID, hash, b.Size, b.Published, b.Message)
	if err != nil {
		return 0, fmt.Errorf("add blocklist: %w", err)
	}
	return res.LastInsertId()
}

// BlocklistForGrab builds the entry a failed grab leaves behind.
func BlocklistForGrab(g Grab, quality, message string) BlocklistEntry {
	b := BlocklistEntry{
		MovieID: g.MovieID, SeriesID: g.SeriesID, SeasonNumber: g.SeasonNumber, EpisodeNumber: g.EpisodeNumber, AlbumID: g.AlbumID,
		SourceTitle: g.ReleaseTitle, Quality: quality, Protocol: g.Protocol, Indexer: g.Indexer, IndexerID: g.IndexerID,
		InfoHash: g.InfoHash, Size: g.Size, Published: g.Published, Message: message,
	}
	if !b.InfoHash.Valid && g.Protocol == "torrent" && g.DownloadClientID.Valid && isInfoHash(g.DownloadClientID.String) {
		// Deluge and qBittorrent know a torrent by its infohash.
		b.InfoHash = sql.NullString{String: g.DownloadClientID.String, Valid: true}
	}
	return b
}

func isInfoHash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range strings.ToLower(s) {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// ListAllBlocklist reads every entry, for the decision engine.
func ListAllBlocklist(ctx context.Context, q Queryer) ([]BlocklistEntry, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+blocklistColumns+` FROM blocklist b`)
	if err != nil {
		return nil, fmt.Errorf("list blocklist: %w", err)
	}
	defer rows.Close()
	var out []BlocklistEntry
	for rows.Next() {
		b, err := scanBlocklist(rows)
		if err != nil {
			return nil, fmt.Errorf("scan blocklist: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListBlocklist reads a page of entries, newest first, with the title of
// the item each belongs to, and the total count.
func ListBlocklist(ctx context.Context, q Queryer, limit, offset int) ([]BlocklistEntry, int, error) {
	var total int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocklist`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count blocklist: %w", err)
	}
	rows, err := q.QueryContext(ctx, `
		SELECT `+blocklistColumns+`,
		       COALESCE(mm.title, sm.title, am.name || ' - ' || al.title, '')
		FROM blocklist b
		LEFT JOIN movies m ON m.id = b.movie_id
		LEFT JOIN movie_metadata mm ON mm.id = m.movie_metadata_id
		LEFT JOIN series s ON s.id = b.series_id
		LEFT JOIN series_metadata sm ON sm.id = s.series_metadata_id
		LEFT JOIN albums al ON al.id = b.album_id
		LEFT JOIN artist_metadata am ON am.id = al.artist_metadata_id
		ORDER BY b.added DESC, b.id DESC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list blocklist: %w", err)
	}
	defer rows.Close()
	var out []BlocklistEntry
	for rows.Next() {
		var title string
		b, err := scanBlocklist(rows, &title)
		if err != nil {
			return nil, 0, fmt.Errorf("scan blocklist: %w", err)
		}
		b.ItemTitle = title
		out = append(out, b)
	}
	return out, total, rows.Err()
}

// DeleteBlocklist removes entries by id, so their releases can be grabbed again.
func DeleteBlocklist(ctx context.Context, q Queryer, ids ...int64) error {
	for _, id := range ids {
		if _, err := q.ExecContext(ctx, `DELETE FROM blocklist WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete blocklist %d: %w", id, err)
		}
	}
	return nil
}

// ClearBlocklist removes every entry.
func ClearBlocklist(ctx context.Context, q Queryer) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM blocklist`); err != nil {
		return fmt.Errorf("clear blocklist: %w", err)
	}
	return nil
}

// HashBlocklisted reports whether a torrent with this infohash is on the
// blocklist for the item g is for - the check behind an indexer's "Reject
// Blocklisted Torrent Hashes While Grabbing".
func HashBlocklisted(ctx context.Context, q Queryer, g Grab, hash string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM blocklist
		WHERE info_hash = ? AND (movie_id IS ? AND series_id IS ? AND album_id IS ?)`,
		strings.ToLower(hash), g.MovieID, g.SeriesID, g.AlbumID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check blocklisted hash: %w", err)
	}
	return n > 0, nil
}

// DownloadHandling is Settings -> Download Clients -> Failed Download Handling.
type DownloadHandling struct {
	RedownloadFailed bool
}

// GetDownloadHandling reads the settings row seeded by migration 00038.
func GetDownloadHandling(ctx context.Context, q Queryer) (DownloadHandling, error) {
	var d DownloadHandling
	if err := q.QueryRowContext(ctx, `SELECT redownload_failed FROM download_handling WHERE id = 1`).Scan(&d.RedownloadFailed); err != nil {
		return DownloadHandling{}, fmt.Errorf("get download handling: %w", err)
	}
	return d, nil
}

// UpdateDownloadHandling saves the settings.
func UpdateDownloadHandling(ctx context.Context, q Queryer, d DownloadHandling) error {
	if _, err := q.ExecContext(ctx, `UPDATE download_handling SET redownload_failed = ? WHERE id = 1`, d.RedownloadFailed); err != nil {
		return fmt.Errorf("update download handling: %w", err)
	}
	return nil
}
