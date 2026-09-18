-- +goose Up
-- A track's own MusicBrainz id, so a Picard-tagged file can be matched to
-- the exact track it came from instead of by its position in the folder.
--
-- Not in external_ids: that table is unique on (entity_type, provider,
-- external_id), i.e. one id maps to one row for ever. That holds for a
-- movie or a series, but the same MusicBrainz track legitimately appears
-- under more than one album_releases row here - two editions of an album
-- list the same tracks - and storing it there made a second edition fail
-- to sync at all.
ALTER TABLE tracks ADD COLUMN musicbrainz_id TEXT;
CREATE INDEX idx_tracks_musicbrainz_id ON tracks (musicbrainz_id);

-- +goose Down
DROP INDEX idx_tracks_musicbrainz_id;
ALTER TABLE tracks DROP COLUMN musicbrainz_id;
