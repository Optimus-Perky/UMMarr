-- +goose Up
-- Adds the last two per-media-type file naming templates that the
-- "importers" pass needs - movie_file_format and episode_file_format.
-- track_file_format already exists (seeded by migration 00001) but was
-- unused until this same pass; no schema change needed for music.
ALTER TABLE naming_config ADD COLUMN movie_file_format TEXT;
ALTER TABLE naming_config ADD COLUMN episode_file_format TEXT;

UPDATE naming_config SET movie_file_format = '{Movie Title} ({Release Year})'
WHERE media_type = 'movie';
UPDATE naming_config SET episode_file_format = '{Series Title} - S{season:00}E{episode:00} - {Episode Title}'
WHERE media_type = 'series';

-- +goose Down
ALTER TABLE naming_config DROP COLUMN movie_file_format;
ALTER TABLE naming_config DROP COLUMN episode_file_format;
