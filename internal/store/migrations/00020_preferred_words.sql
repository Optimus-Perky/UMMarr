-- +goose Up
-- Sonarr's classic "Preferred Words": a term found (case-insensitively) in
-- a release's title adds its score to that release's ranking - positive to
-- prefer, negative to penalize. Applies across every media type's search,
-- not per-series/profile: keyword weighting is a global list, deliberately
-- not scoped to any one profile.
CREATE TABLE preferred_words (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    term  TEXT NOT NULL,
    score INTEGER NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE preferred_words;
