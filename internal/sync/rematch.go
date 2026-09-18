package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Re-matching an album from its files' tags.
//
// Matching by tags only applies to files as they are scanned or imported,
// so anything attached before that keeps whatever positional matching gave
// it - which is exactly where the wrong answers are. This re-reads an
// album's files and reports what their tags say they should be attached
// to, so a library that was matched by position can be corrected without
// re-importing anything.
//
// Nothing moves on disk. Only which track a file hangs off changes.

// RematchChange is one file that its tags say belongs to a different track.
type RematchChange struct {
	FileID     int64
	Path       string
	FromTrack  int64
	FromTitle  string
	ToTrack    int64
	ToTitle    string
	ToNumber   string
	How        string // MatchMBID or MatchNumbers
	Occupied   bool   // the wanted track already holds another file
	OccupiedBy string
}

// RematchReport is what a re-match would do.
type RematchReport struct {
	Changes []RematchChange
	// Confirmed are files already on the track their tags name.
	Confirmed int
	// Unreadable are files that say nothing UMMarr can act on; they keep
	// the track they have, because a guess is what we are getting away
	// from.
	Unreadable []string
}

// RematchPreview works out where an album's files say they belong.
func (s *ImportService) RematchPreview(ctx context.Context, albumID int64) (RematchReport, error) {
	var report RematchReport
	folder, err := store.AlbumFolderPath(ctx, s.DB, albumID)
	if err != nil {
		return report, err
	}
	files, err := store.ListTrackFileDetails(ctx, s.DB, albumID)
	if err != nil {
		return report, err
	}
	_, tracks, err := store.FindImportRelease(ctx, s.DB, albumID)
	if errors.Is(err, store.ErrNoAlbumRelease) {
		// No release synced means no tracks to match against, which is
		// nothing to do rather than a failure to report.
		return report, nil
	}
	if err != nil {
		return report, err
	}
	index := newTrackIndex(tracks)
	titles := make(map[int64]store.TrackImportInfo, len(tracks))
	for _, t := range tracks {
		titles[t.ID] = t
	}
	// Which file sits on each track now, so a swap can be described rather
	// than silently overwriting.
	holder := make(map[int64]string, len(files))
	for _, f := range files {
		holder[f.TrackID] = f.RelativePath
	}

	for _, f := range files {
		tags := s.fileTags(ctx, filepath.Join(folder, f.RelativePath))
		track, how := index.lookup(tags)
		if how == "" {
			report.Unreadable = append(report.Unreadable, f.RelativePath)
			continue
		}
		if track.ID == f.TrackID {
			report.Confirmed++
			continue
		}
		change := RematchChange{
			FileID: f.ID, Path: f.RelativePath,
			FromTrack: f.TrackID, FromTitle: titles[f.TrackID].Title,
			ToTrack: track.ID, ToTitle: track.Title, ToNumber: track.Number, How: how,
		}
		if other, taken := holder[track.ID]; taken && other != f.RelativePath {
			change.Occupied, change.OccupiedBy = true, other
		}
		report.Changes = append(report.Changes, change)
	}
	return report, nil
}

// ApplyRematch moves the chosen files onto the tracks their tags name. It
// runs in one transaction, detaching every chosen file first, so two files
// swapping tracks works and no file is left attached to nothing if a later
// step fails.
func (s *ImportService) ApplyRematch(ctx context.Context, albumID int64, fileIDs []int64) (int, error) {
	report, err := s.RematchPreview(ctx, albumID)
	if err != nil {
		return 0, err
	}
	chosen := map[int64]bool{}
	for _, id := range fileIDs {
		chosen[id] = true
	}
	var moves []RematchChange
	for _, change := range report.Changes {
		if chosen[change.FileID] {
			moves = append(moves, change)
		}
	}
	if len(moves) == 0 {
		return 0, nil
	}

	err = store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		for _, move := range moves {
			if _, err := tx.ExecContext(ctx, `UPDATE tracks SET track_file_id = NULL WHERE track_file_id = ?`, move.FileID); err != nil {
				return fmt.Errorf("detach %s: %w", move.Path, err)
			}
		}
		for _, move := range moves {
			// A track still holding someone else's file is one this run
			// isn't moving, so leave both alone rather than dropping it.
			var held sql.NullInt64
			if err := tx.QueryRowContext(ctx, `SELECT track_file_id FROM tracks WHERE id = ?`, move.ToTrack).Scan(&held); err != nil {
				return fmt.Errorf("read track %d: %w", move.ToTrack, err)
			}
			if held.Valid {
				return fmt.Errorf("%s belongs to %q, which already has another file - re-match those together", move.Path, move.ToTitle)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE tracks SET track_file_id = ? WHERE id = ?`, move.FileID, move.ToTrack); err != nil {
				return fmt.Errorf("attach %s: %w", move.Path, err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	e := albumEvent(ctx, s.DB, albumID, store.EventRenamed)
	e.Detail = fmt.Sprintf("%d file(s) re-matched from their tags", len(moves))
	e.Source = "re-match from tags"
	s.Events.Record(ctx, e)
	return len(moves), nil
}
