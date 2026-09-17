-- +goose Up
-- Failed download handling and the blocklist, as in Sonarr and Radarr.
--
-- A release whose download fails - the client reports an error, or someone
-- marks it failed from the queue - goes on the blocklist, and the decision
-- engine rejects it from then on, so the next search picks something else.
--
-- Rows belong to the library item the release was grabbed for and go with
-- it. A torrent is matched by infohash when both sides know one, otherwise
-- by title and indexer; usenet by title, publish date and size (Sonarr's
-- BlocklistService).
CREATE TABLE blocklist (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    movie_id       INTEGER REFERENCES movies (id) ON DELETE CASCADE,
    series_id      INTEGER REFERENCES series (id) ON DELETE CASCADE,
    season_number  INTEGER,
    episode_number INTEGER,
    album_id       INTEGER REFERENCES albums (id) ON DELETE CASCADE,
    source_title   TEXT NOT NULL,
    quality        TEXT NOT NULL DEFAULT '',
    protocol       TEXT NOT NULL,
    indexer        TEXT NOT NULL DEFAULT '',
    indexer_id     INTEGER,
    info_hash      TEXT,
    size           INTEGER,
    published      TIMESTAMP,
    message        TEXT NOT NULL DEFAULT '',
    added          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK ((movie_id IS NOT NULL) + (series_id IS NOT NULL) + (album_id IS NOT NULL) = 1)
);
CREATE INDEX idx_blocklist_movie_id ON blocklist (movie_id);
CREATE INDEX idx_blocklist_series_id ON blocklist (series_id);
CREATE INDEX idx_blocklist_album_id ON blocklist (album_id);
CREATE INDEX idx_blocklist_info_hash ON blocklist (info_hash);

-- What a later blocklist entry needs to know about the release, captured
-- when it's grabbed: the indexer's publish date, and the torrent's infohash.
ALTER TABLE grabs ADD COLUMN published TIMESTAMP;
ALTER TABLE grabs ADD COLUMN info_hash TEXT;

-- Sonarr's "Remove Failed", per client. Off, so nothing is deleted from a
-- client until it's switched on.
ALTER TABLE download_clients ADD COLUMN remove_failed BOOLEAN NOT NULL DEFAULT 0;

-- Sonarr's "Redownload": search for another release when one fails. Off
-- until switched on, so a failure keeps doing what it did before.
CREATE TABLE download_handling (
    id                INTEGER PRIMARY KEY CHECK (id = 1),
    redownload_failed BOOLEAN NOT NULL DEFAULT 0
);
INSERT INTO download_handling (id) VALUES (1);

-- +goose Down
DROP TABLE download_handling;
ALTER TABLE download_clients DROP COLUMN remove_failed;
ALTER TABLE grabs DROP COLUMN info_hash;
ALTER TABLE grabs DROP COLUMN published;
DROP INDEX idx_blocklist_info_hash;
DROP INDEX idx_blocklist_album_id;
DROP INDEX idx_blocklist_series_id;
DROP INDEX idx_blocklist_movie_id;
DROP TABLE blocklist;
