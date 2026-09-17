-- +goose Up
-- Metadata providers become rows rather than a fixed list: each has its own
-- place in the order, its own key, and can be switched off, so another
-- provider is added without a schema change. Replaces metadata_sources
-- (migration 39), whose orders are carried over.
CREATE TABLE metadata_providers (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    media_type     TEXT NOT NULL CHECK (media_type IN ('movie', 'series', 'music')),
    implementation TEXT NOT NULL, -- tmdb, omdb, tvmaze, tvdb, musicbrainz
    name           TEXT NOT NULL,
    enabled        BOOLEAN NOT NULL DEFAULT 1,
    position       INTEGER NOT NULL DEFAULT 0,
    api_key        TEXT NOT NULL DEFAULT '',
    -- Anything else the provider needs, e.g. TheTVDB's subscriber PIN.
    extra          TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_metadata_providers_media ON metadata_providers (media_type, position);

-- Carry over each media type's saved order. json_each keeps the positions.
INSERT INTO metadata_providers (media_type, implementation, name, enabled, position)
SELECT s.media_type, j.value,
       CASE j.value WHEN 'tmdb' THEN 'TMDB' WHEN 'omdb' THEN 'OMDb' WHEN 'tvmaze' THEN 'TVmaze'
                    WHEN 'musicbrainz' THEN 'MusicBrainz' WHEN 'tvdb' THEN 'TheTVDB' ELSE j.value END,
       1, j.key
FROM metadata_sources s, json_each(s.provider_order) j;

-- TheTVDB joins TV, last and switched off: it needs a key, and a subscriber
-- PIN when the key is a subscriber one.
INSERT INTO metadata_providers (media_type, implementation, name, enabled, position)
VALUES ('series', 'tvdb', 'TheTVDB', 0, (SELECT COUNT(*) FROM metadata_providers WHERE media_type = 'series'));

DROP TABLE metadata_sources;

-- +goose Down
CREATE TABLE metadata_sources (
    media_type     TEXT PRIMARY KEY CHECK (media_type IN ('movie', 'series', 'music')),
    provider_order TEXT NOT NULL
);
INSERT INTO metadata_sources (media_type, provider_order)
SELECT media_type, '["' || group_concat(implementation, '","') || '"]'
FROM (SELECT media_type, implementation FROM metadata_providers WHERE implementation <> 'tvdb' ORDER BY media_type, position)
GROUP BY media_type;
DROP INDEX idx_metadata_providers_media;
DROP TABLE metadata_providers;
