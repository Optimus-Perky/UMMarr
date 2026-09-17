-- +goose Up
-- Sonarr's Manage Episodes editor: languages and release type are stored
-- per file so they can be shown and changed there.
ALTER TABLE episode_files ADD COLUMN languages TEXT NOT NULL DEFAULT '';
ALTER TABLE episode_files ADD COLUMN release_type TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE episode_files DROP COLUMN release_type;
ALTER TABLE episode_files DROP COLUMN languages;
