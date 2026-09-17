package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Why a scan couldn't take a folder.
const (
	UnmatchedNoMatch   = "no-match"  // the provider had no confident match
	UnmatchedDuplicate = "duplicate" // already in the library under another folder
	UnmatchedNoFiles   = "no-files"  // no video or audio files in it
	UnmatchedNoAlbums  = "no-albums" // artist folder with loose tracks, no album folders
)

// UnmatchedReasons is how each reason reads on the scan report.
var UnmatchedReasons = map[string]string{
	UnmatchedNoMatch:   "No match",
	UnmatchedDuplicate: "Already in the library",
	UnmatchedNoFiles:   "No media files",
	UnmatchedNoAlbums:  "No album folders",
}

// UnmatchedFolder is one folder a library scan couldn't take, for the scan
// report.
type UnmatchedFolder struct {
	ID           int64
	RootFolderID int64
	Kind         string // movie, series or music
	Path         string // absolute folder path
	Name         string // folder name, or "Artist / Album"
	Reason       string
	Detail       string
	FirstSeen    time.Time
	LastSeen     time.Time
	Ignored      bool
	ResolvedAt   sql.NullTime
}

// ReasonLabel is how the report names this row's reason.
func (f UnmatchedFolder) ReasonLabel() string {
	if label, ok := UnmatchedReasons[f.Reason]; ok {
		return label
	}
	return f.Reason
}

// RecordUnmatchedFolder notes a folder a scan couldn't take. Seeing the same
// folder again refreshes it rather than adding a second row, and un-resolves
// it - it clearly still isn't in the library.
func RecordUnmatchedFolder(ctx context.Context, q Queryer, f UnmatchedFolder) error {
	if f.Path == "" || f.Kind == "" {
		return errors.New("unmatched folder needs a path and a kind")
	}
	if f.Reason == "" {
		f.Reason = UnmatchedNoMatch
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO unmatched_folders (root_folder_id, kind, path, name, reason, detail)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (path) DO UPDATE SET
			root_folder_id = excluded.root_folder_id, kind = excluded.kind, name = excluded.name,
			reason = excluded.reason, detail = excluded.detail, last_seen = CURRENT_TIMESTAMP, resolved_at = NULL
	`, f.RootFolderID, f.Kind, f.Path, f.Name, f.Reason, f.Detail)
	if err != nil {
		return fmt.Errorf("record unmatched folder %s: %w", f.Path, err)
	}
	return nil
}

// ListUnmatchedFolders lists what the scan report shows: every folder still
// waiting, newest scan first within each kind. Ignored rows come only when
// asked for.
func ListUnmatchedFolders(ctx context.Context, q Queryer, includeIgnored bool) ([]UnmatchedFolder, error) {
	query := `SELECT id, root_folder_id, kind, path, name, reason, detail, first_seen, last_seen, ignored, resolved_at
		FROM unmatched_folders WHERE resolved_at IS NULL`
	if !includeIgnored {
		query += ` AND ignored = 0`
	}
	query += ` ORDER BY kind, name COLLATE NOCASE`
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list unmatched folders: %w", err)
	}
	defer rows.Close()
	var out []UnmatchedFolder
	for rows.Next() {
		var f UnmatchedFolder
		if err := rows.Scan(&f.ID, &f.RootFolderID, &f.Kind, &f.Path, &f.Name, &f.Reason, &f.Detail, &f.FirstSeen, &f.LastSeen, &f.Ignored, &f.ResolvedAt); err != nil {
			return nil, fmt.Errorf("scan unmatched folder: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetUnmatchedFolder reads one row.
func GetUnmatchedFolder(ctx context.Context, q Queryer, id int64) (UnmatchedFolder, bool, error) {
	var f UnmatchedFolder
	err := q.QueryRowContext(ctx, `SELECT id, root_folder_id, kind, path, name, reason, detail, first_seen, last_seen, ignored, resolved_at
		FROM unmatched_folders WHERE id = ?`, id).
		Scan(&f.ID, &f.RootFolderID, &f.Kind, &f.Path, &f.Name, &f.Reason, &f.Detail, &f.FirstSeen, &f.LastSeen, &f.Ignored, &f.ResolvedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return f, false, nil
	}
	if err != nil {
		return f, false, fmt.Errorf("get unmatched folder %d: %w", id, err)
	}
	return f, true, nil
}

// IgnoreUnmatchedFolder hides a folder from the report, or shows it again.
func IgnoreUnmatchedFolder(ctx context.Context, q Queryer, id int64, ignored bool) error {
	if _, err := q.ExecContext(ctx, `UPDATE unmatched_folders SET ignored = ? WHERE id = ?`, ignored, id); err != nil {
		return fmt.Errorf("ignore unmatched folder %d: %w", id, err)
	}
	return nil
}

// ResolveUnmatchedFolder marks a folder as dealt with - matched by hand, or
// found in the library on a later scan.
func ResolveUnmatchedFolder(ctx context.Context, q Queryer, path string) error {
	if _, err := q.ExecContext(ctx, `UPDATE unmatched_folders SET resolved_at = CURRENT_TIMESTAMP WHERE path = ? AND resolved_at IS NULL`, path); err != nil {
		return fmt.Errorf("resolve unmatched folder %s: %w", path, err)
	}
	return nil
}

// CountUnmatchedFolders counts what the report would show, for the badge on
// Settings → Library scan.
func CountUnmatchedFolders(ctx context.Context, q Queryer) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM unmatched_folders WHERE resolved_at IS NULL AND ignored = 0`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count unmatched folders: %w", err)
	}
	return n, nil
}
