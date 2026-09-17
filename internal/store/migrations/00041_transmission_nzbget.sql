-- +goose Up
-- Transmission and NZBGet join the download clients. SQLite can't change a
-- CHECK constraint, so the table is rebuilt with the wider list (the same
-- columns, including remove_failed from migration 38).
CREATE TABLE download_clients_new (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT NOT NULL UNIQUE,
    implementation TEXT NOT NULL CHECK (implementation IN ('deluge', 'qbittorrent', 'transmission', 'sabnzbd', 'nzbget')),
    enabled        BOOLEAN NOT NULL DEFAULT 1,
    priority       INTEGER NOT NULL DEFAULT 1,
    base_url       TEXT NOT NULL DEFAULT '',
    username       TEXT NOT NULL DEFAULT '',
    password       TEXT NOT NULL DEFAULT '',
    api_key        TEXT NOT NULL DEFAULT '',
    category       TEXT NOT NULL DEFAULT '',
    remote_path    TEXT NOT NULL DEFAULT '',
    local_path     TEXT NOT NULL DEFAULT '',
    added          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    remove_failed  BOOLEAN NOT NULL DEFAULT 0
);
INSERT INTO download_clients_new (id, name, implementation, enabled, priority, base_url, username, password, api_key, category, remote_path, local_path, added, remove_failed)
    SELECT id, name, implementation, enabled, priority, base_url, username, password, api_key, category, remote_path, local_path, added, remove_failed FROM download_clients;
DROP TABLE download_clients;
ALTER TABLE download_clients_new RENAME TO download_clients;

-- +goose Down
CREATE TABLE download_clients_old (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT NOT NULL UNIQUE,
    implementation TEXT NOT NULL CHECK (implementation IN ('deluge', 'qbittorrent', 'sabnzbd')),
    enabled        BOOLEAN NOT NULL DEFAULT 1,
    priority       INTEGER NOT NULL DEFAULT 1,
    base_url       TEXT NOT NULL DEFAULT '',
    username       TEXT NOT NULL DEFAULT '',
    password       TEXT NOT NULL DEFAULT '',
    api_key        TEXT NOT NULL DEFAULT '',
    category       TEXT NOT NULL DEFAULT '',
    remote_path    TEXT NOT NULL DEFAULT '',
    local_path     TEXT NOT NULL DEFAULT '',
    added          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    remove_failed  BOOLEAN NOT NULL DEFAULT 0
);
INSERT INTO download_clients_old SELECT * FROM download_clients WHERE implementation IN ('deluge', 'qbittorrent', 'sabnzbd');
DROP TABLE download_clients;
ALTER TABLE download_clients_old RENAME TO download_clients;
