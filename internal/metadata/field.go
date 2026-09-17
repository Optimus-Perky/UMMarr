// Package metadata holds the provider-agnostic result of merging metadata
// fetched from multiple external providers (TMDB, TVMaze, MusicBrainz,
// OMDb, ...) into a single record per movie/series/artist, plus a record
// of which provider supplied each field. Provider-specific HTTP clients
// live in metadata/providers/<name>; the actual merge logic lives in
// metadata/merge.
package metadata

// Field wraps a merged value together with which provider supplied it.
// Provider is empty when no provider set this field. Provider must be one
// of the values accepted by the external_ids/metadata_field_provenance
// CHECK constraints (see internal/store/migrations/00002, 00009).
type Field[T any] struct {
	Value    T
	Provider string
}

// Provenance records that Provider supplied FieldName for the given
// entity, matching one row of metadata_field_provenance.
type Provenance struct {
	EntityType string
	FieldName  string
	Provider   string
}

// ExternalID records one (provider, id) pair for an entity, matching one
// row of external_ids.
type ExternalID struct {
	Provider   string
	ExternalID string
}

// MergeReport carries non-fatal observations from a merge that don't fit
// into the merged record itself - e.g. providers disagreeing on how many
// episodes a season has. Nothing here is persisted by this layer; it's
// returned for a caller (or a later UI pass) to surface if useful.
type MergeReport struct {
	Conflicts []string
}
