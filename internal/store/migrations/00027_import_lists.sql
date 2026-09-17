-- +goose Up
-- Import lists, as in Radarr/Sonarr: outside lists whose titles are added to
-- the library on a schedule, and the titles the user has excluded from that.
CREATE TABLE import_lists (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    name               TEXT NOT NULL UNIQUE,
    implementation     TEXT NOT NULL,
    enabled            BOOLEAN NOT NULL DEFAULT 1,
    media_type         TEXT NOT NULL CHECK (media_type IN ('movie', 'series')),
    settings           TEXT NOT NULL DEFAULT '{}',
    root_folder_id     INTEGER REFERENCES root_folders (id),
    quality_profile_id INTEGER REFERENCES quality_profiles (id),
    monitored          BOOLEAN NOT NULL DEFAULT 1,
    auto_add           BOOLEAN NOT NULL DEFAULT 1,
    search_on_add      BOOLEAN NOT NULL DEFAULT 0,
    last_sync          TIMESTAMP,
    last_result        TEXT NOT NULL DEFAULT '',
    added              TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE import_list_exclusions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    media_type TEXT NOT NULL CHECK (media_type IN ('movie', 'series')),
    tmdb_id    INTEGER NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    UNIQUE (media_type, tmdb_id)
);

-- +goose Down
DROP TABLE import_list_exclusions;
DROP TABLE import_lists;
