-- +goose Up
-- Media analysis: Analyze Audio Files covers music (Analyze Video Files,
-- from migration 00017, covers movies and episodes), and Use Plex Media
-- Info reads what Plex has already analyzed. Plex is off by default.
ALTER TABLE media_settings ADD COLUMN analyze_audio_files BOOLEAN NOT NULL DEFAULT 1;
ALTER TABLE media_settings ADD COLUMN plex_media_info BOOLEAN NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE media_settings DROP COLUMN plex_media_info;
ALTER TABLE media_settings DROP COLUMN analyze_audio_files;
