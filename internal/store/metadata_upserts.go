package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Queryer is satisfied by both *sql.DB and *sql.Tx, so callers can wrap
// these upserts in a transaction alongside other writes - see WithTx.
type Queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// UpsertExternalID records that entity (entityType, entityID) has the
// given id from provider, keyed on the unique constraints from migration
// 00002. Safe to call repeatedly - re-running with the same values is a
// no-op, and importers/refreshes can call this idempotently.
func UpsertExternalID(ctx context.Context, q Queryer, entityType string, entityID int64, provider, externalID string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO external_ids (entity_type, entity_id, provider, external_id)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (entity_type, entity_id, provider)
		DO UPDATE SET external_id = excluded.external_id
	`, entityType, entityID, provider, externalID)
	if err != nil {
		return fmt.Errorf("upsert external_id (%s, %d, %s): %w", entityType, entityID, provider, err)
	}
	return nil
}

// UpsertFieldProvenance records that provider supplied fieldName for
// entity (entityType, entityID), keyed on the primary key from migration
// 00009.
func UpsertFieldProvenance(ctx context.Context, q Queryer, entityType string, entityID int64, fieldName, provider string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO metadata_field_provenance (entity_type, entity_id, field_name, provider, fetched_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT (entity_type, entity_id, field_name)
		DO UPDATE SET provider = excluded.provider, fetched_at = excluded.fetched_at
	`, entityType, entityID, fieldName, provider)
	if err != nil {
		return fmt.Errorf("upsert field_provenance (%s, %d, %s): %w", entityType, entityID, fieldName, err)
	}
	return nil
}

// upsertExternalIDs writes one external_ids row per non-empty entry in
// ids, keyed (provider -> external id) - the shape every merged
// *Metadata.ExternalIDs map already has.
func upsertExternalIDs(ctx context.Context, q Queryer, entityType string, entityID int64, ids map[string]string) error {
	for provider, externalID := range ids {
		if externalID == "" {
			continue
		}
		if err := UpsertExternalID(ctx, q, entityType, entityID, provider, externalID); err != nil {
			return err
		}
	}
	return nil
}

// upsertProvenance writes one metadata_field_provenance row per non-empty
// provider in fields (field name -> provider that supplied it) - the
// shape built by collecting each merged Field[T].Provider that's set.
func upsertProvenance(ctx context.Context, q Queryer, entityType string, entityID int64, fields map[string]string) error {
	for fieldName, provider := range fields {
		if provider == "" {
			continue
		}
		if err := UpsertFieldProvenance(ctx, q, entityType, entityID, fieldName, provider); err != nil {
			return err
		}
	}
	return nil
}
