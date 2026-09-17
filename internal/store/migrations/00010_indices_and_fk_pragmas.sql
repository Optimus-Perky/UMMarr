-- +goose Up
-- SQLite has foreign_keys enforcement off by default per-connection; the
-- app must set `PRAGMA foreign_keys = ON` on every new connection (this
-- can't be persisted in the schema itself, see internal/store's connection
-- setup) - this migration only adds the indices that make FK lookups and
-- the common list/filter queries fast.
CREATE INDEX idx_movies_root_folder_id ON movies (root_folder_id);
CREATE INDEX idx_series_root_folder_id ON series (root_folder_id);
CREATE INDEX idx_artists_root_folder_id ON artists (root_folder_id);
CREATE INDEX idx_movie_metadata_year ON movie_metadata (year);
CREATE INDEX idx_series_metadata_year ON series_metadata (year);
CREATE INDEX idx_compilation_series_albums_series_id ON compilation_series_albums (compilation_series_id);

-- +goose Down
DROP INDEX idx_compilation_series_albums_series_id;
DROP INDEX idx_series_metadata_year;
DROP INDEX idx_movie_metadata_year;
DROP INDEX idx_artists_root_folder_id;
DROP INDEX idx_series_root_folder_id;
DROP INDEX idx_movies_root_folder_id;
