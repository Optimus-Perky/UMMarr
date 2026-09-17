-- +goose Up
-- Season 0 is the specials: one-offs, recaps, behind-the-scenes pieces and
-- webisodes. They usually carry no air date and often were never released
-- as a file at all, so monitoring them means searching every pass for
-- episodes that will never be found.
--
-- On this library, 2026-09-16: 475 monitored-and-missing episodes sat in
-- season 0 - Supernatural alone held 106, Top Gear 103, The Walking Dead
-- 87 - against 140 across every real season put together.
--
-- UpsertSeason now creates specials unmonitored; this switches off the
-- ones already recorded. Only the season flag is touched, which is what
-- decides whether an episode is wanted (an episode counts as monitored
-- only when its season is too), so turning a season back on restores
-- whatever was set per episode.

UPDATE seasons SET monitored = 0 WHERE season_number = 0 AND monitored = 1;

-- +goose Down
-- Which specials were monitored before is not recorded; turning them all
-- back on would be a different state, not the previous one.
SELECT 1;
