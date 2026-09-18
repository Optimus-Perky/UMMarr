-- +goose Up
-- Named access keys, separate from the one API key Prowlarr's app sync
-- uses. The point is revocability: handing the main key to something else
-- means regenerating it later breaks Prowlarr until it is re-pasted there,
-- whereas one of these can be deleted on its own and nothing else notices.
--
-- Unlike the main key these also authenticate the web UI, so a tool can
-- fetch a page rather than only the API - which is what makes them usable
-- for checking that a change renders.
CREATE TABLE access_keys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL,
    key          TEXT NOT NULL UNIQUE,
    created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at TIMESTAMP
);

-- +goose Down
DROP TABLE access_keys;
