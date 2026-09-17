-- +goose Up
-- Custom formats, as in Radarr and Sonarr v4: named sets of conditions a
-- release meets or doesn't, scored per quality profile. Conditions are a
-- JSON array of customformat.Condition.
CREATE TABLE custom_formats (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    name                   TEXT NOT NULL UNIQUE,
    include_when_renaming  BOOLEAN NOT NULL DEFAULT 0,
    conditions             TEXT NOT NULL DEFAULT '[]'
);

-- A profile's score for each format; a format with no row scores 0.
CREATE TABLE quality_profile_format_scores (
    quality_profile_id  INTEGER NOT NULL REFERENCES quality_profiles (id) ON DELETE CASCADE,
    custom_format_id    INTEGER NOT NULL REFERENCES custom_formats (id) ON DELETE CASCADE,
    score               INTEGER NOT NULL,
    PRIMARY KEY (quality_profile_id, custom_format_id)
);

-- Minimum Custom Format Score: releases scoring less are rejected. Upgrade
-- Until Custom Format Score: a file scoring this much isn't upgraded for its
-- formats. Both 0, so nothing changes until formats are scored.
ALTER TABLE quality_profiles ADD COLUMN min_format_score INTEGER NOT NULL DEFAULT 0;
ALTER TABLE quality_profiles ADD COLUMN cutoff_format_score INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE quality_profiles DROP COLUMN cutoff_format_score;
ALTER TABLE quality_profiles DROP COLUMN min_format_score;
DROP TABLE quality_profile_format_scores;
DROP TABLE custom_formats;
