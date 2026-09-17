-- +goose Up
-- What happened to the library, as Sonarr's History page shows it: every
-- grab, import, upgrade, rename, delete, add and failure, with the item it
-- concerned. Items are referenced without foreign keys so history outlives
-- them.
CREATE TABLE history (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    event       TEXT NOT NULL,      -- grabbed, imported, upgraded, renamed, deleted, added, failed, import_failed, needs_extraction, matched, health
    media_type  TEXT NOT NULL DEFAULT '',  -- movie, series, music or ''
    movie_id    INTEGER,
    series_id   INTEGER,
    album_id    INTEGER,
    title       TEXT NOT NULL DEFAULT '',  -- the item, e.g. "Inception (2010)" or "Breaking Bad S01E01"
    detail      TEXT NOT NULL DEFAULT '',  -- release, file or reason
    source      TEXT NOT NULL DEFAULT '',  -- interactive, automatic, rss, scan, webhook...
    quality     TEXT NOT NULL DEFAULT '',
    added       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_history_added ON history (added DESC);
CREATE INDEX idx_history_movie ON history (movie_id);
CREATE INDEX idx_history_series ON history (series_id);
CREATE INDEX idx_history_album ON history (album_id);

-- +goose Down
DROP TABLE history;
