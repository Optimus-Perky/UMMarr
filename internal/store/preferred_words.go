package store

import (
	"context"
	"errors"
	"fmt"
)

// PreferredWord is one Sonarr-style "Preferred Words" entry: a term whose
// presence in a release's title adds Score (positive or negative) to that
// release's ranking. See internal/decision.
type PreferredWord struct {
	ID    int64
	Term  string
	Score int
}

var ErrPreferredWordNotFound = errors.New("preferred word not found")

// ListPreferredWords returns every configured preferred word, alphabetical
// by term.
func ListPreferredWords(ctx context.Context, q Queryer) ([]PreferredWord, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, term, score FROM preferred_words ORDER BY term COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list preferred words: %w", err)
	}
	defer rows.Close()
	var words []PreferredWord
	for rows.Next() {
		var w PreferredWord
		if err := rows.Scan(&w.ID, &w.Term, &w.Score); err != nil {
			return nil, fmt.Errorf("scan preferred word: %w", err)
		}
		words = append(words, w)
	}
	return words, rows.Err()
}

// CreatePreferredWord adds a new preferred word.
func CreatePreferredWord(ctx context.Context, q Queryer, term string, score int) (int64, error) {
	res, err := q.ExecContext(ctx, `INSERT INTO preferred_words (term, score) VALUES (?, ?)`, term, score)
	if err != nil {
		return 0, fmt.Errorf("create preferred word: %w", err)
	}
	return res.LastInsertId()
}

// UpdatePreferredWord changes an existing preferred word's term and score.
func UpdatePreferredWord(ctx context.Context, q Queryer, id int64, term string, score int) error {
	res, err := q.ExecContext(ctx, `UPDATE preferred_words SET term = ?, score = ? WHERE id = ?`, term, score, id)
	if err != nil {
		return fmt.Errorf("update preferred word %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update preferred word %d: %w", id, err)
	}
	if n == 0 {
		return ErrPreferredWordNotFound
	}
	return nil
}

// DeletePreferredWord removes a preferred word.
func DeletePreferredWord(ctx context.Context, q Queryer, id int64) error {
	res, err := q.ExecContext(ctx, `DELETE FROM preferred_words WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete preferred word %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete preferred word %d: %w", id, err)
	}
	if n == 0 {
		return ErrPreferredWordNotFound
	}
	return nil
}
