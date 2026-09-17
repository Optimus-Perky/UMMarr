-- +goose Up
-- episodes.episode_file_id and tracks.track_file_id point at the row
-- holding a library item's file. Nothing indexed them, so
-- every lookup in that direction - "which item owns this file", the join
-- behind a detail page's size and quality, and the orphan sweep in 00034 -
-- scanned the whole table. Over 23,000 episodes that turned migration 34's
-- first draft into a multi-minute scan and forced it to be rewritten
-- around an ephemeral index SQLite had to build each time. movies are not
-- affected: they carry no pointer column, the link is movie_files.movie_id
-- the other way round, and that one is already indexed.
--
-- Partial indexes: the overwhelming majority of rows in a library being
-- filled have no file yet, and those NULLs are never what is looked up.

CREATE INDEX IF NOT EXISTS idx_episodes_episode_file_id
    ON episodes (episode_file_id) WHERE episode_file_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_tracks_track_file_id
    ON tracks (track_file_id) WHERE track_file_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_tracks_track_file_id;
DROP INDEX IF EXISTS idx_episodes_episode_file_id;
