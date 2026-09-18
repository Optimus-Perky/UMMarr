-- +goose Up
-- Artwork fetched from a provider, kept in UMMarr's own folder rather than
-- written into the music library: the library's folders are the user's,
-- and a hand-managed one is exactly where an unexpected cover.jpg would be
-- unwelcome. cover_path (migration 48) still wins - a file the user already
-- has beats anything downloaded.
ALTER TABLE albums ADD COLUMN cover_cache TEXT;
ALTER TABLE albums ADD COLUMN cover_source TEXT;

-- +goose Down
ALTER TABLE albums DROP COLUMN cover_cache;
ALTER TABLE albums DROP COLUMN cover_source;
