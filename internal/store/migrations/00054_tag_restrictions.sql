-- +goose Up
-- Tags restrict download clients and notifications the way they already
-- could indexers (indexers.tags): one with tags is only used for movies,
-- series and artists sharing at least one of them; one without is used for
-- everything. JSON int arrays into tags.id, as on movies and series.
ALTER TABLE download_clients ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';
ALTER TABLE notifications ADD COLUMN tags TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE notifications DROP COLUMN tags;
ALTER TABLE download_clients DROP COLUMN tags;
