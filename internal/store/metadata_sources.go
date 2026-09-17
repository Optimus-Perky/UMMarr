package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MetadataProvider is one row of Settings -> Metadata -> Metadata Sources:
// a provider for one media type, in a given order, with its own key.
type MetadataProvider struct {
	ID             int64
	MediaType      string
	Implementation string
	Name           string
	Enabled        bool
	Position       int
	APIKey         string
	// Extra holds anything else the provider needs, e.g. TheTVDB's
	// subscriber PIN under "pin".
	Extra map[string]string
}

// Pin is TheTVDB's subscriber PIN, when one is set.
func (p MetadataProvider) Pin() string { return p.Extra["pin"] }

// MetadataImplementation describes a provider UMMarr can talk to.
type MetadataImplementation struct {
	Key        string
	Name       string
	MediaTypes []string
	Supplies   string
	NeedsKey   bool
	// Fields are extra credentials beyond the key, e.g. TheTVDB's PIN.
	Fields []struct{ Key, Label, Help string }
	Help   string
}

// MetadataImplementations are the providers UMMarr knows how to talk to. A
// new one appears in the Add list as soon as it's added here.
var MetadataImplementations = []MetadataImplementation{
	{Key: "tmdb", Name: "TMDB", MediaTypes: []string{"movie", "series"}, NeedsKey: true,
		Supplies: "Titles, overviews, genres, runtimes, dates, status, studio or network, collections, posters and fanart, seasons and episodes, TMDB ratings",
		Help:     "A free v4 read access token from themoviedb.org. Leave blank to use UMMARR_TMDB_TOKEN from the environment."},
	{Key: "omdb", Name: "OMDb", MediaTypes: []string{"movie"}, NeedsKey: true,
		Supplies: "Anything TMDB leaves empty, plus IMDb, Rotten Tomatoes and Metacritic ratings",
		Help:     "A free key from omdbapi.com. Leave blank to use UMMARR_OMDB_API_KEY from the environment."},
	{Key: "tvmaze", Name: "TVmaze", MediaTypes: []string{"series"},
		Supplies: "Episode names, summaries and air dates, often for upcoming episodes TMDB hasn't named yet",
		Help:     "No key needed."},
	{Key: "tvdb", Name: "TheTVDB", MediaTypes: []string{"series"}, NeedsKey: true,
		Supplies: "Series details, seasons and episodes, including names TMDB and TVmaze don't have yet",
		Fields: []struct{ Key, Label, Help string }{{Key: "pin", Label: "Subscriber PIN",
			Help: "Only for a subscriber key (the one on thetvdb.com under Dashboard → Subscription). Leave blank for a project API key."}},
		Help: "An API key from thetvdb.com/api-information."},
	{Key: "musicbrainz", Name: "MusicBrainz", MediaTypes: []string{"music"},
		Supplies: "Artists, albums, release groups, releases, tracks, compilation series and cover art",
		Help:     "No key needed; MusicBrainz allows one request a second."},
}

// FindMetadataImplementation looks one up by key.
func FindMetadataImplementation(key string) (MetadataImplementation, bool) {
	for _, impl := range MetadataImplementations {
		if impl.Key == key {
			return impl, true
		}
	}
	return MetadataImplementation{}, false
}

// ErrMetadataProviderNotFound means no provider row has that id.
var ErrMetadataProviderNotFound = errors.New("metadata provider not found")

func scanMetadataProvider(row interface{ Scan(...any) error }) (MetadataProvider, error) {
	var p MetadataProvider
	var extra string
	if err := row.Scan(&p.ID, &p.MediaType, &p.Implementation, &p.Name, &p.Enabled, &p.Position, &p.APIKey, &extra); err != nil {
		return p, err
	}
	p.Extra = map[string]string{}
	_ = json.Unmarshal([]byte(extra), &p.Extra)
	return p, nil
}

const metadataProviderColumns = `id, media_type, implementation, name, enabled, position, api_key, extra`

// ListMetadataProviders lists every provider, by media type then order.
func ListMetadataProviders(ctx context.Context, q Queryer) ([]MetadataProvider, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+metadataProviderColumns+` FROM metadata_providers ORDER BY media_type, position, id`)
	if err != nil {
		return nil, fmt.Errorf("list metadata providers: %w", err)
	}
	defer rows.Close()
	var out []MetadataProvider
	for rows.Next() {
		p, err := scanMetadataProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetMetadataProvider reads one provider.
func GetMetadataProvider(ctx context.Context, q Queryer, id int64) (MetadataProvider, error) {
	p, err := scanMetadataProvider(q.QueryRowContext(ctx, `SELECT `+metadataProviderColumns+` FROM metadata_providers WHERE id = ?`, id))
	if err != nil {
		return p, ErrMetadataProviderNotFound
	}
	return p, nil
}

// SaveMetadataProvider adds a provider (ID 0) or updates it. An empty APIKey
// on an update keeps the stored one.
func SaveMetadataProvider(ctx context.Context, q Queryer, p MetadataProvider) (int64, error) {
	if _, ok := FindMetadataImplementation(p.Implementation); !ok {
		return 0, fmt.Errorf("UMMarr has no %q provider", p.Implementation)
	}
	extra, _ := json.Marshal(p.Extra)
	if p.ID == 0 {
		var next int
		_ = q.QueryRowContext(ctx, `SELECT COALESCE(MAX(position) + 1, 0) FROM metadata_providers WHERE media_type = ?`, p.MediaType).Scan(&next)
		res, err := q.ExecContext(ctx, `INSERT INTO metadata_providers (media_type, implementation, name, enabled, position, api_key, extra)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, p.MediaType, p.Implementation, p.Name, p.Enabled, next, p.APIKey, string(extra))
		if err != nil {
			return 0, fmt.Errorf("add metadata provider: %w", err)
		}
		return res.LastInsertId()
	}
	_, err := q.ExecContext(ctx, `UPDATE metadata_providers SET name = ?, enabled = ?,
		api_key = CASE WHEN ? = '' THEN api_key ELSE ? END, extra = ? WHERE id = ?`,
		p.Name, p.Enabled, p.APIKey, p.APIKey, string(extra), p.ID)
	if err != nil {
		return 0, fmt.Errorf("update metadata provider %d: %w", p.ID, err)
	}
	return p.ID, nil
}

// DeleteMetadataProvider removes a provider.
func DeleteMetadataProvider(ctx context.Context, q Queryer, id int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM metadata_providers WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete metadata provider %d: %w", id, err)
	}
	return nil
}

// MoveMetadataProvider moves a provider one place up or down its media
// type's order.
func MoveMetadataProvider(ctx context.Context, q Queryer, id int64, up bool) error {
	p, err := GetMetadataProvider(ctx, q, id)
	if err != nil {
		return err
	}
	all, err := ListMetadataProviders(ctx, q)
	if err != nil {
		return err
	}
	var order []MetadataProvider
	for _, candidate := range all {
		if candidate.MediaType == p.MediaType {
			order = append(order, candidate)
		}
	}
	for i, candidate := range order {
		if candidate.ID != id {
			continue
		}
		swap := i + 1
		if up {
			swap = i - 1
		}
		if swap < 0 || swap >= len(order) {
			return nil
		}
		order[i], order[swap] = order[swap], order[i]
		break
	}
	for position, candidate := range order {
		if _, err := q.ExecContext(ctx, `UPDATE metadata_providers SET position = ? WHERE id = ?`, position, candidate.ID); err != nil {
			return fmt.Errorf("reorder metadata providers: %w", err)
		}
	}
	return nil
}

// GetMetadataSourceOrders is the enabled providers' implementations per media
// type, in order - what the merge asks first.
func GetMetadataSourceOrders(ctx context.Context, q Queryer) (map[string][]string, error) {
	providers, err := ListMetadataProviders(ctx, q)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, p := range providers {
		if !p.Enabled {
			continue
		}
		out[p.MediaType] = append(out[p.MediaType], p.Implementation)
	}
	return out, nil
}

// EnabledMetadataProvider finds an enabled provider of one implementation.
func EnabledMetadataProvider(ctx context.Context, q Queryer, implementation string) (MetadataProvider, bool) {
	providers, err := ListMetadataProviders(ctx, q)
	if err != nil {
		return MetadataProvider{}, false
	}
	for _, p := range providers {
		if p.Enabled && p.Implementation == implementation {
			return p, true
		}
	}
	return MetadataProvider{}, false
}

// FieldProvenance is which provider supplied one field, and when.
type FieldProvenance struct {
	Field     string
	Provider  string
	FetchedAt time.Time
}

// ListFieldProvenance reads where an entity's fields came from.
func ListFieldProvenance(ctx context.Context, q Queryer, entityType string, entityID int64) ([]FieldProvenance, error) {
	rows, err := q.QueryContext(ctx, `SELECT field_name, provider, fetched_at FROM metadata_field_provenance
		WHERE entity_type = ? AND entity_id = ? ORDER BY field_name`, entityType, entityID)
	if err != nil {
		return nil, fmt.Errorf("list provenance: %w", err)
	}
	defer rows.Close()
	var out []FieldProvenance
	for rows.Next() {
		var p FieldProvenance
		if err := rows.Scan(&p.Field, &p.Provider, &p.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProviderCount is how many of a series' episodes took a field from a provider.
type ProviderCount struct {
	Field    string
	Provider string
	Count    int
}

// EpisodeProvenanceCounts sums where a series' episode fields came from.
func EpisodeProvenanceCounts(ctx context.Context, q Queryer, seriesID int64) ([]ProviderCount, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT p.field_name, p.provider, COUNT(*)
		FROM metadata_field_provenance p
		JOIN episodes e ON e.id = p.entity_id
		JOIN seasons se ON se.id = e.season_id
		WHERE p.entity_type = 'episode' AND se.series_id = ?
		GROUP BY p.field_name, p.provider
		ORDER BY p.field_name, COUNT(*) DESC`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("episode provenance: %w", err)
	}
	defer rows.Close()
	var out []ProviderCount
	for rows.Next() {
		var c ProviderCount
		if err := rows.Scan(&c.Field, &c.Provider, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ExternalIDsFor lists an entity's ids at each provider.
func ExternalIDsFor(ctx context.Context, q Queryer, entityType string, entityID int64) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT provider, external_id FROM external_ids WHERE entity_type = ? AND entity_id = ?`, entityType, entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, id string
		if err := rows.Scan(&p, &id); err != nil {
			return nil, err
		}
		out[p] = id
	}
	return out, rows.Err()
}

// MetadataMediaTypes are the media types providers can be added for.
var MetadataMediaTypes = []string{"movie", "series", "music"}

// ValidMetadataMediaType reports whether mediaType is one of them.
func ValidMetadataMediaType(mediaType string) bool {
	return strings.Contains(strings.Join(MetadataMediaTypes, ","), mediaType) && mediaType != ""
}
