-- +goose Up
-- Generic multi-provider external ID table. Replaces the hardcoded
-- per-provider columns Sonarr uses on Series (TvdbId/TvMazeId/ImdbId/...)
-- with a shape that scales to any entity type gaining a new provider
-- without a schema change, and lets a single entity carry IDs from
-- multiple sources at once (needed to merge/fall back across providers).
CREATE TABLE external_ids (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL CHECK (entity_type IN
        ('movie', 'series', 'season', 'episode', 'artist', 'album', 'release', 'track')),
    entity_id   INTEGER NOT NULL,
    provider    TEXT NOT NULL CHECK (provider IN
        ('tmdb', 'imdb', 'omdb', 'tvdb', 'tvmaze', 'musicbrainz', 'discogs', 'theaudiodb')),
    external_id TEXT NOT NULL,
    UNIQUE (entity_type, provider, external_id),
    UNIQUE (entity_type, entity_id, provider)
);

CREATE INDEX idx_external_ids_entity ON external_ids (entity_type, entity_id);

-- +goose Down
DROP TABLE external_ids;
