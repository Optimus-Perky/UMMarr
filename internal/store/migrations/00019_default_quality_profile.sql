-- +goose Up
-- The profile the Add forms preselect. The first profile created becomes
-- the default; Settings can move it.
ALTER TABLE quality_profiles ADD COLUMN is_default BOOLEAN NOT NULL DEFAULT 0;
UPDATE quality_profiles SET is_default = 1 WHERE id = (SELECT MIN(id) FROM quality_profiles);

-- +goose Down
ALTER TABLE quality_profiles DROP COLUMN is_default;
