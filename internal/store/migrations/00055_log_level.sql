-- +goose Up
-- Settings -> General -> Logging: standard, verbose or diagnostic (see
-- internal/logging).
ALTER TABLE app_settings ADD COLUMN log_level TEXT NOT NULL DEFAULT 'standard';

-- +goose Down
ALTER TABLE app_settings DROP COLUMN log_level;
