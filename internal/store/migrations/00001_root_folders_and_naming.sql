-- +goose Up
CREATE TABLE root_folders (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    path        TEXT NOT NULL UNIQUE,
    media_type  TEXT NOT NULL CHECK (media_type IN ('movie', 'series', 'music')),
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE naming_config (
    media_type              TEXT PRIMARY KEY CHECK (media_type IN ('movie', 'series', 'music')),
    -- movie
    movie_folder_format     TEXT,
    -- series
    series_folder_format    TEXT,
    season_folder_format    TEXT,
    -- music
    artist_folder_format    TEXT,
    album_folder_format     TEXT,
    va_series_folder_format TEXT,
    track_file_format       TEXT
);

-- Seed defaults matching Radarr/Sonarr/Lidarr conventions, adapted for the
-- new Various-Artists-series path. See internal/pathbuilder for how these
-- tokens are resolved.
INSERT INTO naming_config (media_type, movie_folder_format) VALUES
    ('movie', '{Movie Title} ({Release Year})');
INSERT INTO naming_config (media_type, series_folder_format, season_folder_format) VALUES
    ('series', '{Series Title}', 'Season {season}');
INSERT INTO naming_config (media_type, artist_folder_format, album_folder_format, va_series_folder_format, track_file_format) VALUES
    ('music', '{Artist Name}', '{Album Title} ({Release Year})', 'Various Artists/{Series Name}', '{Artist Name} - {Album Title} - {track:00} - {Track Title}');

-- +goose Down
DROP TABLE naming_config;
DROP TABLE root_folders;
