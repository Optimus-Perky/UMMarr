-- +goose Up
-- Same metadata/instance split as movies. Deliberate deviation from Sonarr
-- here: seasons is a REAL table, not embedded JSON on series, because
-- multi-source import needs to merge season lists from TVDB/TMDB/TVMaze
-- independently (they can disagree on season counts/specials), and
-- per-season monitored/path state is easier to query and migrate against
-- as rows than inside a JSON blob.
CREATE TABLE series_metadata (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    title           TEXT NOT NULL,
    sort_title      TEXT NOT NULL,
    clean_title     TEXT NOT NULL,
    status          TEXT,
    overview        TEXT,
    network         TEXT,
    air_time        TEXT,
    series_type     TEXT NOT NULL DEFAULT 'standard',
    certification   TEXT,
    year            INTEGER,
    first_aired     DATE,
    last_aired      DATE,
    runtime         INTEGER,
    original_language TEXT,
    ratings         TEXT NOT NULL DEFAULT '{}',  -- JSON
    genres          TEXT NOT NULL DEFAULT '[]',  -- JSON
    images          TEXT NOT NULL DEFAULT '[]',  -- JSON
    last_info_sync  TIMESTAMP
);
CREATE INDEX idx_series_metadata_clean_title ON series_metadata (clean_title);

CREATE TABLE series (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    series_metadata_id  INTEGER NOT NULL UNIQUE REFERENCES series_metadata (id),
    monitored           BOOLEAN NOT NULL DEFAULT 1,
    monitor_new_items   TEXT NOT NULL DEFAULT 'all',
    season_folder       BOOLEAN NOT NULL DEFAULT 1,
    quality_profile_id  INTEGER REFERENCES quality_profiles (id),
    root_folder_id      INTEGER REFERENCES root_folders (id),
    path                TEXT,
    path_template        TEXT,
    added               TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    tags                TEXT NOT NULL DEFAULT '[]' -- JSON
);

CREATE TABLE seasons (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id               INTEGER NOT NULL REFERENCES series (id),
    season_number           INTEGER NOT NULL,
    monitored               BOOLEAN NOT NULL DEFAULT 1,
    season_folder_override  TEXT,
    path_override           TEXT,
    UNIQUE (series_id, season_number)
);

CREATE TABLE episodes (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id           INTEGER NOT NULL REFERENCES series (id),
    season_id           INTEGER NOT NULL REFERENCES seasons (id),
    episode_file_id     INTEGER REFERENCES episode_files (id), -- forward reference; valid in SQLite, table defined below
    season_number        INTEGER NOT NULL,
    episode_number      INTEGER NOT NULL,
    title               TEXT,
    air_date            DATE,
    overview            TEXT,
    monitored           BOOLEAN NOT NULL DEFAULT 1,
    absolute_episode_number INTEGER,
    runtime             INTEGER,
    ratings             TEXT NOT NULL DEFAULT '{}', -- JSON
    UNIQUE (series_id, season_number, episode_number)
);
CREATE INDEX idx_episodes_series_id ON episodes (series_id);
CREATE INDEX idx_episodes_season_id ON episodes (season_id);

CREATE TABLE episode_files (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    episode_id    INTEGER NOT NULL REFERENCES episodes (id),
    relative_path TEXT NOT NULL,
    size          INTEGER,
    quality       TEXT NOT NULL DEFAULT '{}', -- JSON
    media_info    TEXT NOT NULL DEFAULT '{}', -- JSON
    date_added    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_episode_files_episode_id ON episode_files (episode_id);

-- +goose Down
DROP TABLE episode_files;
DROP TABLE episodes;
DROP TABLE seasons;
DROP TABLE series;
DROP TABLE series_metadata;
