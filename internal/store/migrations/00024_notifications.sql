-- +goose Up
-- Notifications, as in Sonarr's Settings > Connect: where to tell about
-- events, and which events. Each implementation keeps its own fields in
-- settings (JSON) - webhook URL, SMTP details, Plex token and so on.
CREATE TABLE notifications (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT NOT NULL UNIQUE,
    implementation TEXT NOT NULL CHECK (implementation IN ('discord', 'webhook', 'email', 'plex', 'pushover', 'telegram')),
    enabled        BOOLEAN NOT NULL DEFAULT 1,
    settings       TEXT NOT NULL DEFAULT '{}',
    on_grab        BOOLEAN NOT NULL DEFAULT 1,
    on_import      BOOLEAN NOT NULL DEFAULT 1,
    on_upgrade     BOOLEAN NOT NULL DEFAULT 1,
    on_rename      BOOLEAN NOT NULL DEFAULT 0,
    on_delete      BOOLEAN NOT NULL DEFAULT 0,
    on_added       BOOLEAN NOT NULL DEFAULT 0,
    on_failed      BOOLEAN NOT NULL DEFAULT 1,
    on_health      BOOLEAN NOT NULL DEFAULT 1,
    added          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE notifications;
