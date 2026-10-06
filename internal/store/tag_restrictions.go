package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Tag restrictions, as in Radarr, Sonarr and Lidarr: an indexer, download
// client or notification with tags is only used for library items that
// share at least one of them. One with no tags is used for everything.

// TagsAllow reports whether something restricted to restrict may be used
// for an item tagged have.
func TagsAllow[T, U ~int | ~int64](restrict []T, have []U) bool {
	if len(restrict) == 0 {
		return true
	}
	for _, r := range restrict {
		for _, h := range have {
			if int64(r) == int64(h) {
				return true
			}
		}
	}
	return false
}

// parseTagIDs decodes a stored JSON id array; anything unreadable is no tags.
func parseTagIDs(stored string) []int64 {
	var ids []int64
	_ = json.Unmarshal([]byte(stored), &ids)
	return ids
}

func tagIDsJSON(ids []int64) string {
	if ids == nil {
		ids = []int64{}
	}
	data, _ := json.Marshal(ids)
	return string(data)
}

// ItemTags returns the tags of the library item a grab or an event is
// about: the movie's, the series', or for an album its artist's.
func ItemTags(ctx context.Context, q Queryer, movieID, seriesID, albumID sql.NullInt64) ([]int64, error) {
	var query string
	var id int64
	switch {
	case movieID.Valid:
		query, id = `SELECT tags FROM movies WHERE id = ?`, movieID.Int64
	case seriesID.Valid:
		query, id = `SELECT tags FROM series WHERE id = ?`, seriesID.Int64
	case albumID.Valid:
		query, id = `SELECT ar.tags FROM albums al JOIN artists ar ON ar.artist_metadata_id = al.artist_metadata_id WHERE al.id = ?`, albumID.Int64
	default:
		return nil, nil
	}
	var stored string
	err := q.QueryRowContext(ctx, query, id).Scan(&stored)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("item tags: %w", err)
	}
	return parseTagIDs(stored), nil
}

// TagIDsFromText turns a comma-separated list of labels typed into a form
// into tag ids, creating tags that don't exist yet.
func TagIDsFromText(ctx context.Context, q Queryer, text string) ([]int64, error) {
	return EnsureTags(ctx, q, strings.Split(text, ","))
}

// TagText is the comma-separated labels of ids, for a form's tags box.
func TagText[T ~int | ~int64](ctx context.Context, q Queryer, ids []T) string {
	asInt64 := make([]int64, len(ids))
	for i, id := range ids {
		asInt64[i] = int64(id)
	}
	labels, _ := TagLabels(ctx, q, tagIDsJSON(asInt64))
	return strings.Join(labels, ", ")
}

// TagLabelsByID is every tag's label by id.
func TagLabelsByID(ctx context.Context, q Queryer) (map[int64]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, label FROM tags`)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var label string
		if err := rows.Scan(&id, &label); err != nil {
			return nil, err
		}
		out[id] = label
	}
	return out, rows.Err()
}
