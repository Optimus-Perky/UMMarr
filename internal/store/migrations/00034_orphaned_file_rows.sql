-- +goose Up
-- The other half of 00016's problem: a file row that nothing points at.
--
-- Importing an episode attached a new episode_files row and repointed the
-- episode at it, leaving any previous row behind. Two imports of the same
-- release could also run at once (see DownloadService.beginCheck): both
-- found the episode still without a file and both attached one, so an
-- episode ended up with two or three file rows and pointed at only the
-- last. The rest counted towards the library's file totals and sizes for
-- good, with no way to reach them from the episode.
--
-- Clear the ones already left behind. AttachEpisodeFile now replaces an
-- episode's file rather than adding to it.
--
-- NOT IN rather than NOT EXISTS: neither pointer column is indexed, and
-- NOT IN lets SQLite build one ephemeral index instead of rescanning
-- episodes once per file row. movie_files is not affected - movies have no
-- pointer column, the link is movie_files.movie_id the other way round.

DELETE FROM episode_files
WHERE id NOT IN (SELECT episode_file_id FROM episodes WHERE episode_file_id IS NOT NULL);

DELETE FROM track_files
WHERE id NOT IN (SELECT track_file_id FROM tracks WHERE track_file_id IS NOT NULL);

-- +goose Down
-- Rows that nothing referenced cannot be reconstructed.
SELECT 1;
