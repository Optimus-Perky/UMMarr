-- +goose Up
-- Settings > General > Host, as in Sonarr: URL base for reverse proxies,
-- SSL, and an outbound proxy. Applied when UMMarr starts.
ALTER TABLE app_settings ADD COLUMN url_base      TEXT NOT NULL DEFAULT '';
ALTER TABLE app_settings ADD COLUMN ssl_enabled   BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE app_settings ADD COLUMN ssl_port      INTEGER NOT NULL DEFAULT 9898;
ALTER TABLE app_settings ADD COLUMN ssl_cert_path TEXT NOT NULL DEFAULT '';
ALTER TABLE app_settings ADD COLUMN ssl_key_path  TEXT NOT NULL DEFAULT '';
ALTER TABLE app_settings ADD COLUMN proxy_enabled BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE app_settings ADD COLUMN proxy_url     TEXT NOT NULL DEFAULT '';
ALTER TABLE app_settings ADD COLUMN proxy_bypass  TEXT NOT NULL DEFAULT 'localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16';

-- +goose Down
ALTER TABLE app_settings DROP COLUMN proxy_bypass;
ALTER TABLE app_settings DROP COLUMN proxy_url;
ALTER TABLE app_settings DROP COLUMN proxy_enabled;
ALTER TABLE app_settings DROP COLUMN ssl_key_path;
ALTER TABLE app_settings DROP COLUMN ssl_cert_path;
ALTER TABLE app_settings DROP COLUMN ssl_port;
ALTER TABLE app_settings DROP COLUMN ssl_enabled;
ALTER TABLE app_settings DROP COLUMN url_base;
