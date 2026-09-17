package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// FindEntityIDByExternalID looks up the entity_id already recorded for
// (entityType, provider, externalID), keyed on the unique constraint from
// migration 00002. This is what every Upsert<Entity> function uses to
// decide insert-vs-update - the sync package never has to make that call
// itself.
func FindEntityIDByExternalID(ctx context.Context, q Queryer, entityType, provider, externalID string) (id int64, found bool, err error) {
	row := q.QueryRowContext(ctx, `
		SELECT entity_id FROM external_ids
		WHERE entity_type = ? AND provider = ? AND external_id = ?
	`, entityType, provider, externalID)
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("find entity by external id (%s, %s, %s): %w", entityType, provider, externalID, err)
	}
	return id, true, nil
}

// GetExternalID is FindEntityIDByExternalID's reverse: looks up the
// external id already recorded for (entityType, entityID, provider) -
// what Refresh methods use to re-fetch from a provider using an entity's
// existing anchor id.
func GetExternalID(ctx context.Context, q Queryer, entityType string, entityID int64, provider string) (externalID string, found bool, err error) {
	row := q.QueryRowContext(ctx, `
		SELECT external_id FROM external_ids
		WHERE entity_type = ? AND entity_id = ? AND provider = ?
	`, entityType, entityID, provider)
	if err := row.Scan(&externalID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get external id (%s, %d, %s): %w", entityType, entityID, provider, err)
	}
	return externalID, true, nil
}
