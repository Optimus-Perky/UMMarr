-- +goose Up
-- Metadata providers, as in Sonarr: Kodi (XBMC) / Emby, Jellyfin and Plex
-- can each be on, each writing its own file names. Season posters are kept
-- so season artwork can be written too.
ALTER TABLE metadata_settings ADD COLUMN jellyfin_enabled BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE metadata_settings ADD COLUMN plex_enabled BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE seasons ADD COLUMN poster TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE seasons DROP COLUMN poster;
ALTER TABLE metadata_settings DROP COLUMN plex_enabled;
ALTER TABLE metadata_settings DROP COLUMN jellyfin_enabled;
