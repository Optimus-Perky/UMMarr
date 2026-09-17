package store

import (
	"encoding/json"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
)

// marshalJSON encodes v as JSON, normalizing a nil slice/map (which
// json.Marshal renders as "null") to empty, matching the schema's
// NOT NULL DEFAULT '[]'/'{}' columns.
func marshalJSON(v any, empty string) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	if string(b) == "null" {
		return empty, nil
	}
	return string(b), nil
}

// dateOrNull formats t as "YYYY-MM-DD" for a DATE column, or returns nil
// (SQL NULL) when t is nil - used for the several optional release/air
// date columns across movies/series/albums.
func dateOrNull(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.Format("2006-01-02")
}

// unmarshalStringSlice decodes a JSON string-array column (images/genres).
// These columns are always app-written valid JSON, but a detail page
// rendering a poster/genre list is better served by an empty list than a
// hard error over a malformed/legacy row, so a decode failure returns nil
// rather than an error.
func unmarshalStringSlice(raw string) []string {
	var v []string
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil
	}
	return v
}

// unmarshalRatings decodes a JSON {provider: score} ratings column - see
// unmarshalStringSlice for why decode failure returns nil, not an error.
func unmarshalRatings(raw string) map[string]float64 {
	var v map[string]float64
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil
	}
	return v
}

// unmarshalFileQuality decodes a movie_files/episode_files/track_files
// quality JSON column into a releaseparse.FileQuality - see
// unmarshalStringSlice for why decode failure returns the zero value
// rather than an error (a row written before this feature existed, or
// any row that was never touched by an import path that populates
// quality, is simply "{}" - a legitimately empty/unknown quality, not a
// malformed one).
func unmarshalFileQuality(raw string) releaseparse.FileQuality {
	var v releaseparse.FileQuality
	_ = json.Unmarshal([]byte(raw), &v)
	return v
}

// firstOrEmpty returns s[0], or "" if s is empty - used to pick a poster
// URL from an images array (the first image TMDB/TVMaze/MusicBrainz
// return is always the primary poster/cover).
func firstOrEmpty(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
