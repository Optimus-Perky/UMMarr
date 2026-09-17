package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Event kinds recorded in history.
const (
	EventGrabbed         = "grabbed"
	EventImported        = "imported"
	EventUpgraded        = "upgraded"
	EventRenamed         = "renamed"
	EventDeleted         = "deleted"
	EventAdded           = "added"
	EventFailed          = "failed"
	EventImportFailed    = "import_failed"
	EventNeedsExtraction = "needs_extraction"
	EventMatched         = "matched"
	EventHealth          = "health"
)

// Event is one history row.
type Event struct {
	ID        int64
	Event     string
	MediaType string
	MovieID   sql.NullInt64
	SeriesID  sql.NullInt64
	AlbumID   sql.NullInt64
	Title     string
	Detail    string
	Source    string
	Quality   string
	Added     time.Time
}

// RecordEvent appends to history.
func RecordEvent(ctx context.Context, q Queryer, e Event) (int64, error) {
	res, err := q.ExecContext(ctx, `
		INSERT INTO history (event, media_type, movie_id, series_id, album_id, title, detail, source, quality)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Event, e.MediaType, e.MovieID, e.SeriesID, e.AlbumID, e.Title, e.Detail, e.Source, e.Quality)
	if err != nil {
		return 0, fmt.Errorf("record %s event: %w", e.Event, err)
	}
	return res.LastInsertId()
}

// HistoryFilter narrows ListHistory; zero values mean everything.
type HistoryFilter struct {
	Event     string
	MediaType string
	MovieID   int64
	SeriesID  int64
	AlbumID   int64
	Search    string
	Limit     int
	Offset    int
}

// ListHistory lists events newest first, and how many match in all.
func ListHistory(ctx context.Context, q Queryer, f HistoryFilter) ([]Event, int, error) {
	var where []string
	var args []any
	add := func(cond string, v any) { where = append(where, cond); args = append(args, v) }
	if f.Event != "" {
		add("event = ?", f.Event)
	}
	if f.MediaType != "" {
		add("media_type = ?", f.MediaType)
	}
	if f.MovieID > 0 {
		add("movie_id = ?", f.MovieID)
	}
	if f.SeriesID > 0 {
		add("series_id = ?", f.SeriesID)
	}
	if f.AlbumID > 0 {
		add("album_id = ?", f.AlbumID)
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		where = append(where, "(title LIKE ? OR detail LIKE ?)")
		args = append(args, "%"+s+"%", "%"+s+"%")
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM history`+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count history: %w", err)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := q.QueryContext(ctx, `SELECT id, event, media_type, movie_id, series_id, album_id, title, detail, source, quality, added
		FROM history`+clause+` ORDER BY added DESC, id DESC LIMIT ? OFFSET ?`, append(args, limit, f.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list history: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Event, &e.MediaType, &e.MovieID, &e.SeriesID, &e.AlbumID, &e.Title, &e.Detail, &e.Source, &e.Quality, &e.Added); err != nil {
			return nil, 0, fmt.Errorf("scan history: %w", err)
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}
