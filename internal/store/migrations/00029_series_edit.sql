-- +goose Up
-- Sonarr's Edit Series dialog: series type is stored now (renaming and
-- parsing by type comes later); monitor_new_items, season_folder and tags
-- already exist from migration 00005.
ALTER TABLE series ADD COLUMN series_type TEXT NOT NULL DEFAULT 'standard';

-- +goose Down
ALTER TABLE series DROP COLUMN series_type;
