-- +goose Up
-- New concept, doesn't exist in Lidarr today: groups Various Artists albums
-- into a named franchise (e.g. "Now That's What I Call Music",
-- "Ministry of Sound: The Annual") so they can live under a shared
-- subfolder instead of flat under "Various Artists/". MusicBrainz has a
-- native "Series" entity for exactly this (linking release-groups
-- together) - preferred source when available, with a manual fallback for
-- compilations MusicBrainz hasn't modeled as a series.
--
-- The album<->series relationship lives in the join table below, not as a
-- column on albums, so an album has zero-or-one series membership with no
-- schema churn either way, and sequence_number captures e.g. "Now 45"
-- ordering independent of title text.
CREATE TABLE compilation_series (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    name                  TEXT NOT NULL,
    sort_name             TEXT NOT NULL,
    musicbrainz_series_id TEXT UNIQUE,
    source                TEXT NOT NULL CHECK (source IN ('musicbrainz', 'manual')),
    overview              TEXT
);

CREATE TABLE compilation_series_albums (
    compilation_series_id INTEGER NOT NULL REFERENCES compilation_series (id),
    album_id              INTEGER NOT NULL UNIQUE REFERENCES albums (id),
    sequence_number        INTEGER,
    PRIMARY KEY (compilation_series_id, album_id)
);

-- +goose Down
DROP TABLE compilation_series_albums;
DROP TABLE compilation_series;
