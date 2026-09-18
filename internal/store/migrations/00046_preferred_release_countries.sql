-- +goose Up
-- Which country's pressing of an album to prefer when MusicBrainz lists
-- several. A release group is the album; its releases are the individual
-- issues, and they differ - a UK CD with 13 tracks against a Japanese one
-- with 16. Sync used to take whichever release MusicBrainz happened to
-- list first as Official, which is arbitrary.
--
-- Comma separated and in order, so "GB,US" means a British pressing, then
-- an American one, then anything. Empty means no country preference.
ALTER TABLE media_settings ADD COLUMN preferred_release_countries TEXT NOT NULL DEFAULT 'GB,US';

-- +goose Down
ALTER TABLE media_settings DROP COLUMN preferred_release_countries;
