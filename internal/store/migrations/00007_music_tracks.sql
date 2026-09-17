-- +goose Up
-- Tracks carry their OWN artist_metadata_id, independent of the album's.
-- This is the load-bearing mechanism carried forward unchanged from
-- Lidarr: it's what already makes a Various Artists compilation resolve
-- correctly per-track (album artist = Various Artists, but each track
-- points at its real performing artist) without any special-case code.
CREATE TABLE tracks (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    album_release_id    INTEGER NOT NULL REFERENCES album_releases (id),
    artist_metadata_id  INTEGER NOT NULL REFERENCES artist_metadata (id),
    track_file_id       INTEGER REFERENCES track_files (id), -- forward reference, table defined below
    track_number        TEXT NOT NULL, -- string: MusicBrainz numbers can be non-numeric (e.g. "A1")
    absolute_track_number INTEGER,
    medium_number       INTEGER NOT NULL DEFAULT 1,
    title               TEXT NOT NULL,
    duration_ms         INTEGER,
    explicit            BOOLEAN NOT NULL DEFAULT 0,
    ratings             TEXT NOT NULL DEFAULT '{}' -- JSON
);
CREATE INDEX idx_tracks_album_release_id ON tracks (album_release_id);
CREATE INDEX idx_tracks_artist_metadata_id ON tracks (artist_metadata_id);

CREATE TABLE track_files (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    relative_path TEXT NOT NULL,
    size          INTEGER,
    quality       TEXT NOT NULL DEFAULT '{}', -- JSON
    media_info    TEXT NOT NULL DEFAULT '{}', -- JSON
    date_added    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE track_files;
DROP TABLE tracks;
