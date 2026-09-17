-- +goose Up
-- Mirrors Lidarr's shape: ArtistMetadata (canonical, MusicBrainz-identified)
-- decoupled from Artists (the per-instance tracked row). Albums here is the
-- MusicBrainz "release-group" equivalent; AlbumReleases is a specific
-- release/edition of it. Various Artists is NOT special-cased structurally
-- here either (matching Lidarr) - it's an ordinary artist_metadata row,
-- identified by its MusicBrainz external_id like any other artist. The new
-- compilation_series concept (migration 00008) is what actually models the
-- VA-album-series grouping this project adds on top.
CREATE TABLE artist_metadata (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    clean_name      TEXT NOT NULL,
    sort_name       TEXT NOT NULL,
    overview        TEXT,
    disambiguation  TEXT,
    artist_type     TEXT, -- Person/Group/Orchestra/Choir/Character/Other (MusicBrainz)
    status          TEXT,
    genres          TEXT NOT NULL DEFAULT '[]', -- JSON
    images          TEXT NOT NULL DEFAULT '[]', -- JSON
    ratings         TEXT NOT NULL DEFAULT '{}', -- JSON
    members         TEXT NOT NULL DEFAULT '[]', -- JSON
    last_info_sync  TIMESTAMP
);
CREATE INDEX idx_artist_metadata_clean_name ON artist_metadata (clean_name);

CREATE TABLE artists (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    artist_metadata_id  INTEGER NOT NULL UNIQUE REFERENCES artist_metadata (id),
    monitored           BOOLEAN NOT NULL DEFAULT 1,
    monitor_new_items   TEXT NOT NULL DEFAULT 'all',
    quality_profile_id  INTEGER REFERENCES quality_profiles (id),
    root_folder_id      INTEGER REFERENCES root_folders (id),
    path                TEXT,
    path_template        TEXT,
    added               TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    tags                TEXT NOT NULL DEFAULT '[]' -- JSON
);

CREATE TABLE albums (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    artist_metadata_id  INTEGER NOT NULL REFERENCES artist_metadata (id),
    title               TEXT NOT NULL,
    clean_title         TEXT NOT NULL,
    disambiguation      TEXT,
    overview            TEXT,
    release_date        DATE,
    album_type          TEXT NOT NULL DEFAULT 'Album'
        CHECK (album_type IN ('Album', 'EP', 'Single', 'Broadcast', 'Other')),
    secondary_types     TEXT NOT NULL DEFAULT '[]', -- JSON list, e.g. ["Compilation","Live"]
    genres              TEXT NOT NULL DEFAULT '[]', -- JSON
    images              TEXT NOT NULL DEFAULT '[]', -- JSON
    ratings             TEXT NOT NULL DEFAULT '{}', -- JSON
    monitored           BOOLEAN NOT NULL DEFAULT 1,
    path                TEXT,
    path_template        TEXT,
    added               TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_info_sync      TIMESTAMP
);
CREATE INDEX idx_albums_artist_metadata_id ON albums (artist_metadata_id);
CREATE INDEX idx_albums_clean_title ON albums (clean_title);

CREATE TABLE album_releases (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    album_id     INTEGER NOT NULL REFERENCES albums (id),
    title        TEXT NOT NULL,
    status       TEXT, -- Official/Promotion/Bootleg/Pseudo-Release (MusicBrainz)
    disambiguation TEXT,
    country      TEXT NOT NULL DEFAULT '[]', -- JSON
    label        TEXT NOT NULL DEFAULT '[]', -- JSON
    media        TEXT NOT NULL DEFAULT '[]', -- JSON embedded medium list: [{number, name, format}]
    track_count  INTEGER,
    release_date DATE,
    monitored    BOOLEAN NOT NULL DEFAULT 1
);
CREATE INDEX idx_album_releases_album_id ON album_releases (album_id);

-- +goose Down
DROP TABLE album_releases;
DROP TABLE albums;
DROP TABLE artists;
DROP TABLE artist_metadata;
