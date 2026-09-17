package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Import list implementations.
const (
	ListTMDBList     = "tmdb_list"
	ListTMDBPopular  = "tmdb_popular"
	ListTMDBTopRated = "tmdb_top_rated"
	ListTMDBTrending = "tmdb_trending"
	ListTMDBDiscover = "tmdb_discover"
	ListPlexRSS      = "plex_rss"
	ListTrakt        = "trakt_list"
)

// ImportList mirrors one import_lists row (migration 00027).
type ImportList struct {
	ID               int64
	Name             string
	Implementation   string
	Enabled          bool
	MediaType        string // movie or series
	Settings         map[string]string
	RootFolderID     sql.NullInt64
	QualityProfileID sql.NullInt64
	Monitored        bool
	AutoAdd          bool
	SearchOnAdd      bool
	LastSync         sql.NullTime
	LastResult       string
}

// ErrImportListNotFound means no row has that id.
var ErrImportListNotFound = errors.New("import list not found")

const importListColumns = `id, name, implementation, enabled, media_type, settings, root_folder_id, quality_profile_id, monitored, auto_add, search_on_add, last_sync, last_result`

func scanImportList(row interface{ Scan(...any) error }) (ImportList, error) {
	var l ImportList
	var settings string
	err := row.Scan(&l.ID, &l.Name, &l.Implementation, &l.Enabled, &l.MediaType, &settings, &l.RootFolderID, &l.QualityProfileID, &l.Monitored, &l.AutoAdd, &l.SearchOnAdd, &l.LastSync, &l.LastResult)
	if err != nil {
		return l, err
	}
	l.Settings = map[string]string{}
	_ = json.Unmarshal([]byte(settings), &l.Settings)
	return l, nil
}

// ListImportLists lists every list by name.
func ListImportLists(ctx context.Context, q Queryer) ([]ImportList, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+importListColumns+` FROM import_lists ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list import lists: %w", err)
	}
	defer rows.Close()
	var out []ImportList
	for rows.Next() {
		l, err := scanImportList(rows)
		if err != nil {
			return nil, fmt.Errorf("scan import list: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetImportList reads one list.
func GetImportList(ctx context.Context, q Queryer, id int64) (ImportList, error) {
	l, err := scanImportList(q.QueryRowContext(ctx, `SELECT `+importListColumns+` FROM import_lists WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ImportList{}, ErrImportListNotFound
	}
	if err != nil {
		return ImportList{}, fmt.Errorf("get import list %d: %w", id, err)
	}
	return l, nil
}

func importListSettings(l ImportList) (string, error) {
	if l.Settings == nil {
		return "{}", nil
	}
	b, err := json.Marshal(l.Settings)
	if err != nil {
		return "", fmt.Errorf("marshal import list settings: %w", err)
	}
	return string(b), nil
}

// CreateImportList adds a list.
func CreateImportList(ctx context.Context, q Queryer, l ImportList) (int64, error) {
	settings, err := importListSettings(l)
	if err != nil {
		return 0, err
	}
	res, err := q.ExecContext(ctx, `
		INSERT INTO import_lists (name, implementation, enabled, media_type, settings, root_folder_id, quality_profile_id, monitored, auto_add, search_on_add)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.Name, l.Implementation, l.Enabled, l.MediaType, settings, l.RootFolderID, l.QualityProfileID, l.Monitored, l.AutoAdd, l.SearchOnAdd)
	if err != nil {
		return 0, fmt.Errorf("create import list: %w", err)
	}
	return res.LastInsertId()
}

// UpdateImportList saves a list.
func UpdateImportList(ctx context.Context, q Queryer, l ImportList) error {
	settings, err := importListSettings(l)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `
		UPDATE import_lists SET name = ?, implementation = ?, enabled = ?, media_type = ?, settings = ?, root_folder_id = ?, quality_profile_id = ?,
			monitored = ?, auto_add = ?, search_on_add = ?
		WHERE id = ?`,
		l.Name, l.Implementation, l.Enabled, l.MediaType, settings, l.RootFolderID, l.QualityProfileID, l.Monitored, l.AutoAdd, l.SearchOnAdd, l.ID)
	if err != nil {
		return fmt.Errorf("update import list %d: %w", l.ID, err)
	}
	return nil
}

// RecordImportListSync notes when a list was synced and how it went.
func RecordImportListSync(ctx context.Context, q Queryer, id int64, result string) error {
	if _, err := q.ExecContext(ctx, `UPDATE import_lists SET last_sync = ?, last_result = ? WHERE id = ?`, time.Now().UTC(), result, id); err != nil {
		return fmt.Errorf("record import list %d sync: %w", id, err)
	}
	return nil
}

// DeleteImportList removes a list.
func DeleteImportList(ctx context.Context, q Queryer, id int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM import_lists WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete import list %d: %w", id, err)
	}
	return nil
}

// ImportListNameTaken reports whether another list has name.
func ImportListNameTaken(ctx context.Context, q Queryer, name string, exceptID int64) (bool, error) {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM import_lists WHERE name = ? AND id <> ?`, name, exceptID).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// Exclusion is a title lists must never add.
type Exclusion struct {
	ID        int64
	MediaType string
	TMDBID    int
	Title     string
}

// AddExclusion records one; a repeat is fine.
func AddExclusion(ctx context.Context, q Queryer, e Exclusion) error {
	if _, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO import_list_exclusions (media_type, tmdb_id, title) VALUES (?, ?, ?)`, e.MediaType, e.TMDBID, e.Title); err != nil {
		return fmt.Errorf("add exclusion: %w", err)
	}
	return nil
}

// ListExclusions lists every exclusion.
func ListExclusions(ctx context.Context, q Queryer) ([]Exclusion, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, media_type, tmdb_id, title FROM import_list_exclusions ORDER BY media_type, title`)
	if err != nil {
		return nil, fmt.Errorf("list exclusions: %w", err)
	}
	defer rows.Close()
	var out []Exclusion
	for rows.Next() {
		var e Exclusion
		if err := rows.Scan(&e.ID, &e.MediaType, &e.TMDBID, &e.Title); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteExclusion removes one.
func DeleteExclusion(ctx context.Context, q Queryer, id int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM import_list_exclusions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete exclusion %d: %w", id, err)
	}
	return nil
}

// TrackedTMDBIDs is every TMDB id the library already has for mediaType.
func TrackedTMDBIDs(ctx context.Context, q Queryer, mediaType string) (map[int]bool, error) {
	var query string
	switch mediaType {
	case "movie":
		query = `SELECT e.external_id FROM movies m JOIN external_ids e ON e.entity_type = 'movie' AND e.provider = 'tmdb' AND e.entity_id = m.movie_metadata_id`
	case "series":
		query = `SELECT e.external_id FROM series s JOIN external_ids e ON e.entity_type = 'series' AND e.provider = 'tmdb' AND e.entity_id = s.series_metadata_id`
	default:
		return nil, fmt.Errorf("tracked ids: unknown media type %q", mediaType)
	}
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			var id int
			fmt.Sscanf(s, "%d", &id)
			out[id] = true
		}
	}
	return out, rows.Err()
}
