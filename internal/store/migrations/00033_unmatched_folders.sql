-- +goose Up
-- Every folder a library scan couldn't match, kept so the scan report
-- outlives the run that found it and can be matched by hand later.
CREATE TABLE unmatched_folders (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    root_folder_id INTEGER NOT NULL REFERENCES root_folders (id),
    kind           TEXT NOT NULL CHECK (kind IN ('movie', 'series', 'music')),
    path           TEXT NOT NULL UNIQUE,
    name           TEXT NOT NULL,
    reason         TEXT NOT NULL DEFAULT 'no-match',
    detail         TEXT NOT NULL DEFAULT '',
    first_seen     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ignored        BOOLEAN NOT NULL DEFAULT 0,
    resolved_at    TIMESTAMP
);
CREATE INDEX idx_unmatched_folders_kind ON unmatched_folders (kind);

-- +goose Down
DROP TABLE unmatched_folders;
