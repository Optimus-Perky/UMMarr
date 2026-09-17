package store

import (
	"context"
	"fmt"
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
