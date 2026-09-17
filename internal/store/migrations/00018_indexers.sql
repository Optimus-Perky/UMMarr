-- +goose Up
-- Indexers, as in Radarr's Settings -> Indexers: any number of Torznab or
-- Newznab indexers, added in UMMarr, synced by Prowlarr through UMMarr's
-- Radarr-compatible API (synced = 1), or converted from the single Prowlarr
-- connection UMMarr used before this migration (converted = 1).
CREATE TABLE indexers (
    id                        INTEGER PRIMARY KEY AUTOINCREMENT,
    name                      TEXT NOT NULL,
    implementation            TEXT NOT NULL CHECK (implementation IN ('Torznab', 'Newznab')),
    enable_rss                BOOLEAN NOT NULL DEFAULT 1,
    enable_automatic_search   BOOLEAN NOT NULL DEFAULT 1,
    enable_interactive_search BOOLEAN NOT NULL DEFAULT 1,
    priority                  INTEGER NOT NULL DEFAULT 25 CHECK (priority BETWEEN 1 AND 50),
    base_url                  TEXT NOT NULL,
    api_path                  TEXT NOT NULL DEFAULT '/api',
    api_key                   TEXT NOT NULL DEFAULT '',
    categories                TEXT NOT NULL DEFAULT '[]', -- JSON list of Newznab category ids
    anime_categories          TEXT NOT NULL DEFAULT '[]',
    additional_parameters     TEXT NOT NULL DEFAULT '',
    minimum_seeders           INTEGER NOT NULL DEFAULT 1,
    seed_ratio                REAL,
    seed_time                 INTEGER,  -- minutes
    season_pack_seed_time     INTEGER,
    discography_seed_time     INTEGER,
    required_flags            TEXT NOT NULL DEFAULT '[]', -- JSON list of Radarr IndexerFlags values
    reject_blocklisted        BOOLEAN NOT NULL DEFAULT 0,
    tags                      TEXT NOT NULL DEFAULT '[]',
    download_client_id        INTEGER NOT NULL DEFAULT 0,
    synced                    BOOLEAN NOT NULL DEFAULT 0,
    converted                 BOOLEAN NOT NULL DEFAULT 0,
    extra_fields              TEXT NOT NULL DEFAULT '[]', -- API fields UMMarr doesn't use, returned as sent
    last_error                TEXT NOT NULL DEFAULT '',
    failures                  INTEGER NOT NULL DEFAULT 0,
    disabled_until            TIMESTAMP,
    added                     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Radarr's Settings -> Indexers -> Options. RSS sync is off (0) until it's
-- set, so upgrading doesn't start grabbing anything on its own.
CREATE TABLE indexer_settings (
    id                          INTEGER PRIMARY KEY CHECK (id = 1),
    minimum_age                 INTEGER NOT NULL DEFAULT 0,  -- minutes
    retention                   INTEGER NOT NULL DEFAULT 0,  -- days
    maximum_size                INTEGER NOT NULL DEFAULT 0,  -- MB
    prefer_indexer_flags        BOOLEAN NOT NULL DEFAULT 0,
    availability_delay          INTEGER NOT NULL DEFAULT 0,  -- days
    rss_sync_interval           INTEGER NOT NULL DEFAULT 0,  -- minutes, 0 = off
    whitelisted_hardcoded_subs  TEXT NOT NULL DEFAULT '',
    allow_hardcoded_subs        BOOLEAN NOT NULL DEFAULT 0,
    prowlarr_converted          BOOLEAN NOT NULL DEFAULT 0,
    last_rss_sync               TIMESTAMP,
    last_rss_result             TEXT NOT NULL DEFAULT ''
);
INSERT INTO indexer_settings (id) VALUES (1);

-- UMMarr's own API key, for Prowlarr's app sync. Generated at startup.
ALTER TABLE app_settings ADD COLUMN api_key TEXT NOT NULL DEFAULT '';

-- Which indexer a grab came from (no FK: removing an indexer keeps its
-- history) and what grabbed it.
ALTER TABLE grabs ADD COLUMN indexer_id INTEGER;
ALTER TABLE grabs ADD COLUMN grabbed_by TEXT NOT NULL DEFAULT 'interactive';

-- +goose Down
ALTER TABLE grabs DROP COLUMN grabbed_by;
ALTER TABLE grabs DROP COLUMN indexer_id;
ALTER TABLE app_settings DROP COLUMN api_key;
DROP TABLE indexer_settings;
DROP TABLE indexers;
