-- +goose Up
-- Shared across all three media types, same as in Sonarr/Radarr/Lidarr today.
CREATE TABLE quality_profiles (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    cutoff      INTEGER,
    items       TEXT NOT NULL DEFAULT '[]', -- JSON: ordered allowed/upgrade-until quality list
    upgrade_allowed BOOLEAN NOT NULL DEFAULT 1
);

CREATE TABLE tags (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    label   TEXT NOT NULL UNIQUE
);

-- +goose Down
DROP TABLE tags;
DROP TABLE quality_profiles;
