-- +goose Up
-- episodes.episode_file_id and tracks.track_file_id are forward pointers
-- at their *_files row, declared without ON DELETE SET NULL. Deleting a
-- file row leaves the pointer dangling, and the detail pages read "has a
-- file" from the pointer being non-NULL while the join that supplies its
-- size/quality finds nothing - an episode renders as Downloaded with a
-- blank size and no release group, and its season count overstates what
-- is actually on disk.
--
-- SQLite cannot add a foreign key action to an existing column, so the
-- alternative would be rebuilding both tables and copying live library
-- data through them. Triggers give the same guarantee for a fraction of
-- the risk, and unlike foreign key actions they fire regardless of the
-- connection's foreign_keys pragma.

-- +goose StatementBegin
CREATE TRIGGER episode_files_clear_episode_pointer
AFTER DELETE ON episode_files
FOR EACH ROW
BEGIN
    UPDATE episodes SET episode_file_id = NULL WHERE episode_file_id = OLD.id;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER track_files_clear_track_pointer
AFTER DELETE ON track_files
FOR EACH ROW
BEGIN
    UPDATE tracks SET track_file_id = NULL WHERE track_file_id = OLD.id;
END;
-- +goose StatementEnd

-- Clear any pointer already left dangling before the triggers existed.
UPDATE episodes SET episode_file_id = NULL
WHERE episode_file_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM episode_files f WHERE f.id = episodes.episode_file_id);

UPDATE tracks SET track_file_id = NULL
WHERE track_file_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM track_files f WHERE f.id = tracks.track_file_id);

-- +goose Down
DROP TRIGGER IF EXISTS track_files_clear_track_pointer;
DROP TRIGGER IF EXISTS episode_files_clear_episode_pointer;
