-- +goose Up
-- Artwork that is already in the library. A hand-managed music folder
-- usually has its covers sitting right there - folder.jpg beside the
-- tracks, a Kodi-style set of discart/fanart/banner around it - and
-- UMMarr showed none of it, because it only ever knew about image URLs
-- the providers handed back (and MusicBrainz hands back none).
--
-- The path is relative to the album or artist folder, so moving the
-- library doesn't invalidate it; /music/albums/{id}/cover serves it.
ALTER TABLE albums  ADD COLUMN cover_path TEXT;
ALTER TABLE artists ADD COLUMN cover_path TEXT;

-- +goose Down
ALTER TABLE albums  DROP COLUMN cover_path;
ALTER TABLE artists DROP COLUMN cover_path;
