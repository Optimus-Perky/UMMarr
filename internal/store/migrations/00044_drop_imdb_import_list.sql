-- +goose Up
-- IMDb import lists are removed: IMDb refuses its own export to anything
-- without a login (403, or a 202 bot challenge with browser headers), and
-- Radarr only manages it by proxying through api.radarr.video. Trakt, the
-- Plex watchlist RSS and TMDB lists all still sync on their own.
DELETE FROM import_lists WHERE implementation = 'imdb_list';

-- +goose Down
-- Nothing to restore: the lists themselves are gone.
SELECT 1;
