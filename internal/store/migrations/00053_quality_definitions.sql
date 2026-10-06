-- +goose Up
-- Radarr's and Sonarr's Quality Definitions: how big a release of each
-- video quality may be, in megabytes per minute of runtime, so a 300MB
-- "Bluray-1080p" movie (a fake, or a sample) or a 90GB one is turned away
-- by size rather than grabbed. 0 means no limit (max) or no preference.
--
-- The minimums are deliberately low - below any real encode, so they catch
-- fakes and samples without turning away a small x265 release - and there
-- are no maximums until someone sets one: a size limit that rejected what
-- UMMarr grabbed yesterday would be a surprise, not a feature.
CREATE TABLE quality_definitions (
    quality        TEXT PRIMARY KEY,
    min_size       REAL NOT NULL DEFAULT 0,
    preferred_size REAL NOT NULL DEFAULT 0,
    max_size       REAL NOT NULL DEFAULT 0
);
INSERT INTO quality_definitions (quality, min_size) VALUES
    ('Unknown', 0),
    ('SDTV', 2), ('DVD', 2),
    ('WEBRip-480p', 2), ('WEBDL-480p', 2),
    ('HDTV-720p', 3), ('WEBRip-720p', 3), ('WEBDL-720p', 3), ('Bluray-720p', 3),
    ('HDTV-1080p', 4), ('WEBRip-1080p', 4), ('WEBDL-1080p', 4), ('Bluray-1080p', 4), ('Remux-1080p', 15),
    ('HDTV-2160p', 10), ('WEBRip-2160p', 10), ('WEBDL-2160p', 10), ('Bluray-2160p', 10), ('Remux-2160p', 30);

-- +goose Down
DROP TABLE quality_definitions;
