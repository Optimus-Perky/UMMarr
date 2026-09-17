-- +goose Up
-- Settings > Metadata, as in Radarr/Sonarr's Kodi (XBMC) / Emby provider:
-- what to write beside the files for media players to read.
CREATE TABLE metadata_settings (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    enabled         BOOLEAN NOT NULL DEFAULT 0,
    movie_nfo       BOOLEAN NOT NULL DEFAULT 1,
    movie_images    BOOLEAN NOT NULL DEFAULT 1,
    series_nfo      BOOLEAN NOT NULL DEFAULT 1,
    episode_nfo     BOOLEAN NOT NULL DEFAULT 1,
    series_images   BOOLEAN NOT NULL DEFAULT 1,
    album_nfo       BOOLEAN NOT NULL DEFAULT 1,
    album_images    BOOLEAN NOT NULL DEFAULT 1
);
INSERT INTO metadata_settings (id) VALUES (1);

-- +goose Down
DROP TABLE metadata_settings;
