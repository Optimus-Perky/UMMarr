-- +goose Up
-- What pressing an album actually is. MusicBrainz says the country, date
-- and track count; Discogs knows the rest - the label, the catalogue
-- number, and the descriptions that tell a remaster from an original or a
-- 180g reissue from the first run.
--
-- Kept on the album rather than the release row because it describes the
-- copy the library holds, and survives switching which MusicBrainz release
-- the album tracks.
ALTER TABLE albums ADD COLUMN edition_label      TEXT;
ALTER TABLE albums ADD COLUMN edition_catalogue  TEXT;
ALTER TABLE albums ADD COLUMN edition_format     TEXT;
ALTER TABLE albums ADD COLUMN edition_country    TEXT;
ALTER TABLE albums ADD COLUMN edition_year       INTEGER;
ALTER TABLE albums ADD COLUMN discogs_release_id INTEGER;

-- +goose Down
ALTER TABLE albums DROP COLUMN edition_label;
ALTER TABLE albums DROP COLUMN edition_catalogue;
ALTER TABLE albums DROP COLUMN edition_format;
ALTER TABLE albums DROP COLUMN edition_country;
ALTER TABLE albums DROP COLUMN edition_year;
ALTER TABLE albums DROP COLUMN discogs_release_id;
