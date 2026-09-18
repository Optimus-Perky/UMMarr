-- +goose Up
-- Discogs as a music metadata source, off until it is turned on. It is
-- listed after MusicBrainz because it supplements rather than replaces it:
-- MusicBrainz has no cover art at all, and Discogs describes pressings in
-- more detail.
INSERT INTO metadata_providers (media_type, implementation, name, enabled, position, api_key, extra)
SELECT 'music', 'discogs', 'Discogs', 0,
       COALESCE((SELECT MAX(position) + 1 FROM metadata_providers WHERE media_type = 'music'), 0),
       '', '{}'
WHERE NOT EXISTS (SELECT 1 FROM metadata_providers WHERE media_type = 'music' AND implementation = 'discogs');

-- +goose Down
DELETE FROM metadata_providers WHERE media_type = 'music' AND implementation = 'discogs';
