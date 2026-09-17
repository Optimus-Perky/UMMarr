-- +goose Up
-- Episodes that only TVMaze ever listed, in seasons TMDB also described.
--
-- The season merge used to union episode numbers across providers, so an
-- episode any provider listed became a row. TVMaze splits a double-length
-- premiere into two entries, which shifts every later episode of that
-- season by one and leaves its last entry repeating the real finale under
-- the same title. Shrinking season 3 showed 11/12 for ever, with a second
-- "And That's Our Time" nobody has next to the real one, monitored and
-- searched for on every pass.
--
-- MergeSeasons now lets one provider decide which episodes a season has,
-- so no more of these appear. Nothing prunes episodes on refresh though,
-- so the ones already recorded have to be cleared here.
--
-- Deliberately narrow: only episodes with no file, whose title came from
-- TVMaze, in a season where TMDB named at least one episode. A season
-- only TVMaze knows about is left alone - TVMaze is still the source
-- there, and those episodes are all the library has.
--
-- The ids are collected up front. Clearing the provenance rows first
-- would delete the very rows that identify which episodes these are.

CREATE TEMP TABLE phantom_episodes AS
SELECT e.id FROM episodes e
JOIN metadata_field_provenance p ON p.entity_type = 'episode' AND p.entity_id = e.id
     AND p.field_name = 'title' AND p.provider = 'tvmaze'
WHERE e.episode_file_id IS NULL
  AND EXISTS (SELECT 1 FROM episodes o
              JOIN metadata_field_provenance q ON q.entity_type = 'episode' AND q.entity_id = o.id
                   AND q.field_name = 'title' AND q.provider = 'tmdb'
              WHERE o.series_id = e.series_id AND o.season_number = e.season_number);

DELETE FROM metadata_field_provenance
WHERE entity_type = 'episode' AND entity_id IN (SELECT id FROM phantom_episodes);

DELETE FROM external_ids
WHERE entity_type = 'episode' AND entity_id IN (SELECT id FROM phantom_episodes);

DELETE FROM episodes WHERE id IN (SELECT id FROM phantom_episodes);

DROP TABLE phantom_episodes;

-- +goose Down
-- Episodes that no provider will hand back cannot be reconstructed; a
-- metadata refresh re-adds any that a provider does still list.
SELECT 1;
