-- +goose Up
-- Field-level provenance for the multi-source metadata merge: which
-- provider actually supplied a given field, so the merge engine
-- (internal/metadata) doesn't have to re-derive that decision on every
-- read, and so a future per-user override ("prefer TVDB overview but TMDB
-- images") has something to act on. The merge logic itself lives in Go,
-- not SQL - this table only records the outcome of a merge, not how it was
-- computed.
CREATE TABLE metadata_field_provenance (
    entity_type TEXT NOT NULL CHECK (entity_type IN
        ('movie', 'series', 'season', 'episode', 'artist', 'album', 'release', 'track')),
    entity_id   INTEGER NOT NULL,
    field_name  TEXT NOT NULL,
    provider    TEXT NOT NULL CHECK (provider IN
        ('tmdb', 'imdb', 'omdb', 'tvdb', 'tvmaze', 'musicbrainz', 'discogs', 'theaudiodb')),
    fetched_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (entity_type, entity_id, field_name)
);

-- +goose Down
DROP TABLE metadata_field_provenance;
