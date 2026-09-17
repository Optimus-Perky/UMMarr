-- +goose Up
-- Singleton settings row (id=1 enforced by the CHECK) for everything this
-- pass makes editable via the Settings UI instead of only env vars/.env:
-- indexer + download-client connection info, and the chosen admin
-- username/password (bcrypt hash, never the raw password). Empty string
-- in any column means "not set via the UI - fall back to the env var
-- bootstrap", not NULL, to keep every read a plain non-nullable scan.
CREATE TABLE app_settings (
    id                 INTEGER PRIMARY KEY CHECK (id = 1),
    prowlarr_base_url  TEXT NOT NULL DEFAULT '',
    prowlarr_api_key   TEXT NOT NULL DEFAULT '',
    deluge_base_url    TEXT NOT NULL DEFAULT '',
    deluge_password    TEXT NOT NULL DEFAULT '',
    auth_username      TEXT NOT NULL DEFAULT '',
    auth_password_hash TEXT NOT NULL DEFAULT ''
);
INSERT INTO app_settings (id) VALUES (1);

-- +goose Down
DROP TABLE app_settings;
