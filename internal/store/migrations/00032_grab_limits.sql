-- +goose Up
-- Prowlarr-style per-indexer Grab Limit: how many releases UMMarr may grab
-- from an indexer between the tracker's daily resets (0 = no limit).
ALTER TABLE indexers ADD COLUMN grab_limit INTEGER NOT NULL DEFAULT 0;
ALTER TABLE indexers ADD COLUMN grab_limit_reset_hour INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE indexers DROP COLUMN grab_limit_reset_hour;
ALTER TABLE indexers DROP COLUMN grab_limit;
