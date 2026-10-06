package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
)

// QualityDefinition is Settings -> Quality's size limits for one video
// quality, in megabytes per minute of runtime (Radarr's and Sonarr's unit).
// MaxSize 0 means no limit, PreferredSize 0 no preference.
type QualityDefinition struct {
	Quality       string
	MinSize       float64
	PreferredSize float64
	MaxSize       float64
}

// ListQualityDefinitions returns one definition per video quality, in the
// catalog's order (worst to best). A quality with no saved row - one added
// to the catalog after the database was created - comes back with no
// limits.
func ListQualityDefinitions(ctx context.Context, q Queryer) ([]QualityDefinition, error) {
	rows, err := q.QueryContext(ctx, `SELECT quality, min_size, preferred_size, max_size FROM quality_definitions`)
	if err != nil {
		return nil, fmt.Errorf("list quality definitions: %w", err)
	}
	defer rows.Close()
	saved := map[string]QualityDefinition{}
	for rows.Next() {
		var d QualityDefinition
		if err := rows.Scan(&d.Quality, &d.MinSize, &d.PreferredSize, &d.MaxSize); err != nil {
			return nil, fmt.Errorf("scan quality definition: %w", err)
		}
		saved[d.Quality] = d
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]QualityDefinition, 0, len(releaseparse.AllQualities))
	for _, quality := range releaseparse.AllQualities {
		d, ok := saved[quality]
		if !ok {
			d = QualityDefinition{Quality: quality}
		}
		out = append(out, d)
	}
	return out, nil
}

// QualityDefinitionsByQuality is ListQualityDefinitions keyed by quality.
func QualityDefinitionsByQuality(ctx context.Context, q Queryer) (map[string]QualityDefinition, error) {
	list, err := ListQualityDefinitions(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make(map[string]QualityDefinition, len(list))
	for _, d := range list {
		out[d.Quality] = d
	}
	return out, nil
}

// Validate checks a definition makes sense on its own.
func (d QualityDefinition) Validate() error {
	switch {
	case d.MinSize < 0 || d.PreferredSize < 0 || d.MaxSize < 0:
		return fmt.Errorf("%s: sizes can't be negative", d.Quality)
	case d.MaxSize > 0 && d.MinSize > d.MaxSize:
		return fmt.Errorf("%s: the minimum is bigger than the maximum", d.Quality)
	case d.PreferredSize > 0 && d.PreferredSize < d.MinSize:
		return fmt.Errorf("%s: the preferred size is below the minimum", d.Quality)
	case d.PreferredSize > 0 && d.MaxSize > 0 && d.PreferredSize > d.MaxSize:
		return fmt.Errorf("%s: the preferred size is above the maximum", d.Quality)
	}
	return nil
}

// SaveQualityDefinitions replaces the definitions given, all or none.
func SaveQualityDefinitions(ctx context.Context, db *sql.DB, defs []QualityDefinition) error {
	for _, d := range defs {
		if err := d.Validate(); err != nil {
			return err
		}
	}
	return WithTx(ctx, db, func(tx *sql.Tx) error {
		for _, d := range defs {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO quality_definitions (quality, min_size, preferred_size, max_size) VALUES (?, ?, ?, ?)
				ON CONFLICT (quality) DO UPDATE SET min_size = excluded.min_size,
					preferred_size = excluded.preferred_size, max_size = excluded.max_size`,
				d.Quality, d.MinSize, d.PreferredSize, d.MaxSize); err != nil {
				return fmt.Errorf("save quality definition %s: %w", d.Quality, err)
			}
		}
		return nil
	})
}
