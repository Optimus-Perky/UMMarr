-- +goose Up
-- Refines a series/album grab down to a single episode/track, for the
-- per-episode/per-track "Find release" buttons on the series/album detail
-- pages - previously a series grab could only be "whole series"
-- (season_number NULL) or "season pack" (season_number set), and an
-- album grab was always "whole album." episode_number needs no FK (it's
-- a display/bookkeeping refinement of season_number, same pattern);
-- track_id does, since a single-track grab must import against that
-- exact track, not a positional match across the whole release (see
-- ImportService.importTrack).
ALTER TABLE grabs ADD COLUMN episode_number INTEGER;
ALTER TABLE grabs ADD COLUMN track_id INTEGER REFERENCES tracks (id);

-- +goose Down
ALTER TABLE grabs DROP COLUMN track_id;
ALTER TABLE grabs DROP COLUMN episode_number;
