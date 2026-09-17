-- +goose Up
-- Mirrors Radarr's Movie/MovieMetadata split (introduced there in migration
-- 207): immutable shared metadata decoupled from the per-instance tracked
-- row, so un-added/listed movies can share a metadata row. External IDs
-- (TMDB/IMDb/OMDb) live in external_ids, not as columns here.
CREATE TABLE movie_metadata (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    title               TEXT NOT NULL,
    sort_title          TEXT NOT NULL,
    clean_title         TEXT NOT NULL,
    original_title      TEXT,
    status              TEXT,
    year                INTEGER,
    runtime             INTEGER,
    in_cinemas          DATE,
    physical_release    DATE,
    digital_release     DATE,
    certification       TEXT,
    overview            TEXT,
    studio              TEXT,
    collection_title    TEXT,
    ratings             TEXT NOT NULL DEFAULT '{}',  -- JSON
    genres              TEXT NOT NULL DEFAULT '[]',  -- JSON
    images              TEXT NOT NULL DEFAULT '[]',  -- JSON
    last_info_sync      TIMESTAMP
);
CREATE INDEX idx_movie_metadata_clean_title ON movie_metadata (clean_title);

CREATE TABLE movies (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    movie_metadata_id   INTEGER NOT NULL UNIQUE REFERENCES movie_metadata (id),
    monitored           BOOLEAN NOT NULL DEFAULT 1,
    minimum_availability TEXT NOT NULL DEFAULT 'released',
    quality_profile_id  INTEGER REFERENCES quality_profiles (id),
    root_folder_id      INTEGER REFERENCES root_folders (id),
    path                TEXT,
    path_template        TEXT,
    added               TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    tags                TEXT NOT NULL DEFAULT '[]' -- JSON int array, FK-by-value into tags.id
);

CREATE TABLE movie_files (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    movie_id     INTEGER NOT NULL REFERENCES movies (id),
    relative_path TEXT NOT NULL,
    size         INTEGER,
    quality      TEXT NOT NULL DEFAULT '{}', -- JSON
    media_info   TEXT NOT NULL DEFAULT '{}', -- JSON
    date_added   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_movie_files_movie_id ON movie_files (movie_id);

-- +goose Down
DROP TABLE movie_files;
DROP TABLE movies;
DROP TABLE movie_metadata;
