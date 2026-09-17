-- +goose Up
-- A poster chosen by hand, as in Plex: it wins over whatever the metadata
-- providers offer and survives a refresh. entity_type is the library item's
-- kind, not a metadata row, so re-matching an item keeps the choice.
CREATE TABLE poster_overrides (
    entity_type TEXT NOT NULL CHECK (entity_type IN ('movie', 'series', 'artist', 'album')),
    entity_id   INTEGER NOT NULL,
    url         TEXT NOT NULL,
    PRIMARY KEY (entity_type, entity_id)
);

-- +goose Down
DROP TABLE poster_overrides;
