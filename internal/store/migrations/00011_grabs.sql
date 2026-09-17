-- +goose Up
-- Records one indexer release sent to the download client: the join point
-- a future "importers" pass uses to match a finished download back to the
-- library item it's for. Nothing here writes to
-- movie_files/episode_files/track_files - that import step is a separate,
-- later pass.
CREATE TABLE grabs (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    movie_id            INTEGER REFERENCES movies (id),
    series_id           INTEGER REFERENCES series (id),
    season_number       INTEGER, -- set for a season-pack grab; NULL + series_id set means "whole series"
    album_id            INTEGER REFERENCES albums (id),
    release_title       TEXT NOT NULL,
    indexer             TEXT NOT NULL,
    protocol            TEXT NOT NULL DEFAULT 'torrent',
    size                INTEGER,
    download_client     TEXT NOT NULL DEFAULT 'deluge',
    download_client_id  TEXT,
    status              TEXT NOT NULL DEFAULT 'grabbed', -- grabbed|downloading|completed|failed|removed
    status_message      TEXT,
    added               TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated             TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK ((movie_id IS NOT NULL) + (series_id IS NOT NULL) + (album_id IS NOT NULL) = 1)
);
CREATE INDEX idx_grabs_movie_id ON grabs (movie_id);
CREATE INDEX idx_grabs_series_id ON grabs (series_id);
CREATE INDEX idx_grabs_album_id ON grabs (album_id);
CREATE INDEX idx_grabs_download_client_id ON grabs (download_client_id);

-- +goose Down
DROP TABLE grabs;
