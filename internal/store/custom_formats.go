package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/customformat"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
)

// ErrCustomFormatNotFound means no custom format has that id.
var ErrCustomFormatNotFound = errors.New("custom format not found")

// ListCustomFormats lists every custom format by name.
func ListCustomFormats(ctx context.Context, q Queryer) ([]customformat.Format, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name, include_when_renaming, conditions FROM custom_formats ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list custom formats: %w", err)
	}
	defer rows.Close()
	var out []customformat.Format
	for rows.Next() {
		f, err := scanCustomFormat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func scanCustomFormat(row interface{ Scan(...any) error }) (customformat.Format, error) {
	var f customformat.Format
	var raw string
	if err := row.Scan(&f.ID, &f.Name, &f.IncludeWhenRenaming, &raw); err != nil {
		return f, err
	}
	_ = json.Unmarshal([]byte(raw), &f.Conditions)
	return f, nil
}

// GetCustomFormat reads one custom format.
func GetCustomFormat(ctx context.Context, q Queryer, id int64) (customformat.Format, error) {
	f, err := scanCustomFormat(q.QueryRowContext(ctx, `SELECT id, name, include_when_renaming, conditions FROM custom_formats WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrCustomFormatNotFound
	}
	return f, err
}

// SaveCustomFormat adds f (ID 0) or updates it, and returns its id.
func SaveCustomFormat(ctx context.Context, q Queryer, f customformat.Format) (int64, error) {
	raw, err := json.Marshal(f.Conditions)
	if err != nil {
		return 0, err
	}
	if f.ID == 0 {
		res, err := q.ExecContext(ctx, `INSERT INTO custom_formats (name, include_when_renaming, conditions) VALUES (?, ?, ?)`, f.Name, f.IncludeWhenRenaming, string(raw))
		if err != nil {
			return 0, fmt.Errorf("add custom format: %w", err)
		}
		return res.LastInsertId()
	}
	if _, err := q.ExecContext(ctx, `UPDATE custom_formats SET name = ?, include_when_renaming = ?, conditions = ? WHERE id = ?`, f.Name, f.IncludeWhenRenaming, string(raw), f.ID); err != nil {
		return 0, fmt.Errorf("update custom format %d: %w", f.ID, err)
	}
	return f.ID, nil
}

// CustomFormatNameTaken reports whether another format already has name.
func CustomFormatNameTaken(ctx context.Context, q Queryer, name string, exceptID int64) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM custom_formats WHERE name = ? COLLATE NOCASE AND id <> ?`, name, exceptID).Scan(&n)
	return n > 0, err
}

// DeleteCustomFormat removes a format and its scores.
func DeleteCustomFormat(ctx context.Context, q Queryer, id int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM custom_formats WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete custom format %d: %w", id, err)
	}
	return nil
}

// ProfileFormatScores is a quality profile's custom format scoring.
type ProfileFormatScores struct {
	Scores      map[int64]int // by custom format id
	MinScore    int
	CutoffScore int
}

// GetProfileFormatScores reads a profile's format scores and thresholds.
func GetProfileFormatScores(ctx context.Context, q Queryer, profileID int64) (ProfileFormatScores, error) {
	s := ProfileFormatScores{Scores: map[int64]int{}}
	if err := q.QueryRowContext(ctx, `SELECT min_format_score, cutoff_format_score FROM quality_profiles WHERE id = ?`, profileID).Scan(&s.MinScore, &s.CutoffScore); err != nil {
		return s, fmt.Errorf("quality profile %d format scores: %w", profileID, err)
	}
	rows, err := q.QueryContext(ctx, `SELECT custom_format_id, score FROM quality_profile_format_scores WHERE quality_profile_id = ?`, profileID)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var score int
		if err := rows.Scan(&id, &score); err != nil {
			return s, err
		}
		s.Scores[id] = score
	}
	return s, rows.Err()
}

// SaveProfileFormatScores replaces a profile's format scores and thresholds.
func SaveProfileFormatScores(ctx context.Context, db *sql.DB, profileID int64, s ProfileFormatScores) error {
	return WithTx(ctx, db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE quality_profiles SET min_format_score = ?, cutoff_format_score = ? WHERE id = ?`, s.MinScore, s.CutoffScore, profileID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM quality_profile_format_scores WHERE quality_profile_id = ?`, profileID); err != nil {
			return err
		}
		for id, score := range s.Scores {
			if score == 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO quality_profile_format_scores (quality_profile_id, custom_format_id, score) VALUES (?, ?, ?)`, profileID, id, score); err != nil {
				return err
			}
		}
		return nil
	})
}

// CustomFormatNames is the {Custom Formats} naming token: the names of the
// formats a release matches that are set to be included when renaming,
// separated by spaces, as in Radarr. An empty string when none match.
func CustomFormatNames(ctx context.Context, q Queryer, releaseName string) string {
	formats, err := ListCustomFormats(ctx, q)
	if err != nil || len(formats) == 0 {
		return ""
	}
	release := customformat.ReleaseFrom(newznab.Release{Title: releaseName})
	var names []string
	for _, f := range customformat.Matching(formats, release) {
		if f.IncludeWhenRenaming {
			names = append(names, f.Name)
		}
	}
	return strings.Join(names, " ")
}
