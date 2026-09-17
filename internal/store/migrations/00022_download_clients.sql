-- +goose Up
-- Download clients, as in Sonarr's Settings > Download Clients: any number,
-- torrent or usenet, tried by priority. The Deluge connection saved under the
-- old single-client settings becomes the first row.
CREATE TABLE download_clients (
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
    added          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO download_clients (name, implementation, base_url, password)
    SELECT 'Deluge', 'deluge', deluge_base_url, deluge_password FROM app_settings WHERE id = 1 AND deluge_base_url != '';

-- Which client row a grab went to (no FK: removing a client keeps history).
ALTER TABLE grabs ADD COLUMN download_client_ref INTEGER;

-- +goose Down
ALTER TABLE grabs DROP COLUMN download_client_ref;
DROP TABLE download_clients;
