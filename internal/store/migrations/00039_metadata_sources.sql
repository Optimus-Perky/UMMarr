-- +goose Up
-- Settings -> Metadata -> Sources: the order metadata providers are asked
-- for each media type. The first provider with a value for a field wins it;
-- for TV the first also decides which episodes each season has. Seeded with
-- the order the merge used before this was adjustable.
CREATE TABLE metadata_sources (
    media_type     TEXT PRIMARY KEY CHECK (media_type IN ('movie', 'series', 'music')),
    provider_order TEXT NOT NULL -- JSON array of provider keys
);
INSERT INTO metadata_sources (media_type, provider_order) VALUES
    ('movie', '["tmdb","omdb"]'),
    ('series', '["tmdb","tvmaze"]'),
    ('music', '["musicbrainz"]');

-- +goose Down
DROP TABLE metadata_sources;
