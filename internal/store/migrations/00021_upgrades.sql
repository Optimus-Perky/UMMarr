-- +goose Up
-- Quality profile upgrades, as in Radarr: upgrade_allowed (already a
-- column) and the quality to upgrade until ('' = the best allowed quality).
-- Existing profiles start with upgrades off, so nothing re-downloads until
-- upgrades are turned on per profile; new profiles follow Radarr and allow them.
ALTER TABLE quality_profiles ADD COLUMN cutoff_quality TEXT NOT NULL DEFAULT '';
UPDATE quality_profiles SET upgrade_allowed = 0;

-- +goose Down
ALTER TABLE quality_profiles DROP COLUMN cutoff_quality;
