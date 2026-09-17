package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Indexer is one row of the indexers table (migration 00018): a Torznab or
// Newznab indexer with Radarr's settings.
type Indexer struct {
	ID                      int64
	Name                    string
	Implementation          string // "Torznab" or "Newznab"
	EnableRSS               bool
	EnableAutomaticSearch   bool
	EnableInteractiveSearch bool
	Priority                int
	BaseURL                 string
	APIPath                 string
	APIKey                  string
	Categories              []int
	AnimeCategories         []int
	AdditionalParameters    string
	MinimumSeeders          int
	SeedRatio               sql.NullFloat64
	SeedTime                sql.NullInt64 // minutes
	SeasonPackSeedTime      sql.NullInt64
	DiscographySeedTime     sql.NullInt64
	RequiredFlags           []int
	RejectBlocklisted       bool
	Tags                    []int
	DownloadClientID        int
	Synced                  bool
	Converted               bool
	ExtraFields             json.RawMessage

	// GrabLimit is how many releases may be grabbed from this indexer between
	// the tracker's daily resets (0 = no limit); GrabLimitResetHour is the UTC
	// hour that counter resets at.
	GrabLimit          int
	GrabLimitResetHour int

	LastError     string
	Failures      int
	DisabledUntil sql.NullTime
	Added         time.Time
}

// DefaultIndexerPriority is Radarr's default priority.
const DefaultIndexerPriority = 25

// Protocol is "usenet" for Newznab and "torrent" for Torznab.
func (i Indexer) Protocol() string {
	if i.Implementation == "Newznab" {
		return "usenet"
	}
	return "torrent"
}

// Enabled reports whether any of RSS, automatic or interactive search is on.
func (i Indexer) Enabled() bool {
	return i.EnableRSS || i.EnableAutomaticSearch || i.EnableInteractiveSearch
}

// AllCategories is Categories plus AnimeCategories.
func (i Indexer) AllCategories() []int {
	return append(append([]int{}, i.Categories...), i.AnimeCategories...)
}

// BackedOff reports whether the indexer is resting after failures at now.
func (i Indexer) BackedOff(now time.Time) bool {
	return i.DisabledUntil.Valid && i.DisabledUntil.Time.After(now)
}

// indexerBackoff follows Radarr's escalating back-off: each consecutive
// failure rests the indexer longer, up to a day.
var indexerBackoff = []time.Duration{
	0, time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 12 * time.Hour, 24 * time.Hour,
}

// IndexerBackoff is how long an indexer rests after failures consecutive failures.
func IndexerBackoff(failures int) time.Duration {
	if failures < 0 {
		failures = 0
	}
	if failures >= len(indexerBackoff) {
		failures = len(indexerBackoff) - 1
	}
	return indexerBackoff[failures]
}

var ErrIndexerNotFound = errors.New("indexer not found")

const indexerColumns = `
	id, name, implementation, enable_rss, enable_automatic_search, enable_interactive_search, priority,
	base_url, api_path, api_key, categories, anime_categories, additional_parameters, minimum_seeders,
	seed_ratio, seed_time, season_pack_seed_time, discography_seed_time, required_flags, reject_blocklisted,
	tags, download_client_id, synced, converted, extra_fields, last_error, failures, disabled_until, added,
	grab_limit, grab_limit_reset_hour
`

func intsJSON(ids []int) string {
	if len(ids) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ids)
	return string(b)
}

func parseInts(s string) []int {
	var ids []int
	_ = json.Unmarshal([]byte(s), &ids)
	return ids
}

func scanIndexer(row interface{ Scan(...any) error }) (Indexer, error) {
	var i Indexer
	var cats, anime, flags, tags, extra string
	err := row.Scan(&i.ID, &i.Name, &i.Implementation, &i.EnableRSS, &i.EnableAutomaticSearch, &i.EnableInteractiveSearch,
		&i.Priority, &i.BaseURL, &i.APIPath, &i.APIKey, &cats, &anime, &i.AdditionalParameters, &i.MinimumSeeders,
		&i.SeedRatio, &i.SeedTime, &i.SeasonPackSeedTime, &i.DiscographySeedTime, &flags, &i.RejectBlocklisted,
		&tags, &i.DownloadClientID, &i.Synced, &i.Converted, &extra, &i.LastError, &i.Failures, &i.DisabledUntil, &i.Added,
		&i.GrabLimit, &i.GrabLimitResetHour)
	if err != nil {
		return Indexer{}, err
	}
	i.Categories, i.AnimeCategories, i.RequiredFlags, i.Tags = parseInts(cats), parseInts(anime), parseInts(flags), parseInts(tags)
	i.ExtraFields = json.RawMessage(extra)
	return i, nil
}

// ListIndexers lists every indexer by name.
func ListIndexers(ctx context.Context, q Queryer) ([]Indexer, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+indexerColumns+` FROM indexers ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("list indexers: %w", err)
	}
	defer rows.Close()
	var out []Indexer
	for rows.Next() {
		i, err := scanIndexer(rows)
		if err != nil {
			return nil, fmt.Errorf("scan indexer: %w", err)
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// GetIndexer reads one indexer.
func GetIndexer(ctx context.Context, q Queryer, id int64) (Indexer, error) {
	i, err := scanIndexer(q.QueryRowContext(ctx, `SELECT `+indexerColumns+` FROM indexers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Indexer{}, ErrIndexerNotFound
	}
	if err != nil {
		return Indexer{}, fmt.Errorf("get indexer %d: %w", id, err)
	}
	return i, nil
}

func extraJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "[]"
	}
	return string(raw)
}

func indexerArgs(i Indexer) []any {
	return []any{i.Name, i.Implementation, i.EnableRSS, i.EnableAutomaticSearch, i.EnableInteractiveSearch, i.Priority,
		i.BaseURL, i.APIPath, i.APIKey, intsJSON(i.Categories), intsJSON(i.AnimeCategories), i.AdditionalParameters,
		i.MinimumSeeders, i.SeedRatio, i.SeedTime, i.SeasonPackSeedTime, i.DiscographySeedTime, intsJSON(i.RequiredFlags),
		i.RejectBlocklisted, intsJSON(i.Tags), i.DownloadClientID, i.Synced, i.Converted, extraJSON(i.ExtraFields),
		i.GrabLimit, i.GrabLimitResetHour}
}

// CreateIndexer adds an indexer and returns its id.
func CreateIndexer(ctx context.Context, q Queryer, i Indexer) (int64, error) {
	res, err := q.ExecContext(ctx, `
		INSERT INTO indexers (
			name, implementation, enable_rss, enable_automatic_search, enable_interactive_search, priority,
			base_url, api_path, api_key, categories, anime_categories, additional_parameters,
			minimum_seeders, seed_ratio, seed_time, season_pack_seed_time, discography_seed_time, required_flags,
			reject_blocklisted, tags, download_client_id, synced, converted, extra_fields,
			grab_limit, grab_limit_reset_hour
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, indexerArgs(i)...)
	if err != nil {
		return 0, fmt.Errorf("create indexer: %w", err)
	}
	return res.LastInsertId()
}

// UpdateIndexer saves an indexer's settings. Its failure state is kept, but
// a changed indexer gets a fresh chance: the back-off is cleared.
func UpdateIndexer(ctx context.Context, q Queryer, i Indexer) error {
	res, err := q.ExecContext(ctx, `
		UPDATE indexers SET
			name = ?, implementation = ?, enable_rss = ?, enable_automatic_search = ?, enable_interactive_search = ?, priority = ?,
			base_url = ?, api_path = ?, api_key = ?, categories = ?, anime_categories = ?, additional_parameters = ?,
			minimum_seeders = ?, seed_ratio = ?, seed_time = ?, season_pack_seed_time = ?, discography_seed_time = ?, required_flags = ?,
			reject_blocklisted = ?, tags = ?, download_client_id = ?, synced = ?, converted = ?, extra_fields = ?,
			grab_limit = ?, grab_limit_reset_hour = ?,
			disabled_until = NULL
		WHERE id = ?
	`, append(indexerArgs(i), i.ID)...)
	if err != nil {
		return fmt.Errorf("update indexer %d: %w", i.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrIndexerNotFound
	}
	return nil
}

// DeleteIndexer removes an indexer. Grabs keep their history.
func DeleteIndexer(ctx context.Context, q Queryer, id int64) error {
	res, err := q.ExecContext(ctx, `DELETE FROM indexers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete indexer %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrIndexerNotFound
	}
	return nil
}

// RecordIndexerSuccess clears an indexer's failures.
func RecordIndexerSuccess(ctx context.Context, q Queryer, id int64) error {
	_, err := q.ExecContext(ctx, `UPDATE indexers SET failures = 0, last_error = '', disabled_until = NULL WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("record indexer %d success: %w", id, err)
	}
	return nil
}

// RecordIndexerFailure counts a failure and rests the indexer for the
// escalating back-off period.
func RecordIndexerFailure(ctx context.Context, q Queryer, id int64, message string, now time.Time) error {
	var failures int
	if err := q.QueryRowContext(ctx, `SELECT failures FROM indexers WHERE id = ?`, id).Scan(&failures); err != nil {
		return fmt.Errorf("record indexer %d failure: %w", id, err)
	}
	failures++
	_, err := q.ExecContext(ctx, `UPDATE indexers SET failures = ?, last_error = ?, disabled_until = ? WHERE id = ?`,
		failures, message, now.Add(IndexerBackoff(failures)).UTC(), id)
	if err != nil {
		return fmt.Errorf("record indexer %d failure: %w", id, err)
	}
	return nil
}

func indexerEndpoint(i Indexer) string {
	return strings.ToLower(strings.TrimRight(i.BaseURL, "/")) + "|" + strings.Trim(i.APIPath, "/")
}

// IndexerNameTaken reports whether another indexer already has name (ignoring
// case), as Radarr's "Should be unique" check does. Converted entries don't
// count: Prowlarr's sync adds indexers under the same names.
func IndexerNameTaken(ctx context.Context, q Queryer, name string, exceptID int64) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM indexers WHERE name = ? COLLATE NOCASE AND id != ? AND converted = 0`, name, exceptID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check indexer name: %w", err)
	}
	return n > 0, nil
}

// mediaTypeOfCategory mirrors newznab.MediaTypeOf without importing it.
func mediaTypeOfCategory(id int) int { return id / 1000 } // 2 movies, 5 TV, 3 audio

func mediaTypesOf(ids []int) map[int]bool {
	types := map[int]bool{}
	for _, id := range ids {
		types[mediaTypeOfCategory(id)] = true
	}
	return types
}

func withoutMediaTypes(ids []int, types map[int]bool) []int {
	out := []int{}
	for _, id := range ids {
		if !types[mediaTypeOfCategory(id)] {
			out = append(out, id)
		}
	}
	return out
}

// AbsorbConvertedIndexer lets an indexer Prowlarr synced take over from the
// entry converted from UMMarr's old Prowlarr connection for the same endpoint.
// Prowlarr only syncs the categories an indexer says it supports, so the
// hand-over goes by media type: once the synced entry has any movie, TV or
// audio category, the converted entry drops all of that type, and is removed
// once it has none left.
func AbsorbConvertedIndexer(ctx context.Context, q Queryer, synced Indexer) error {
	all, err := ListIndexers(ctx, q)
	if err != nil {
		return err
	}
	covered := mediaTypesOf(synced.AllCategories())
	for _, c := range all {
		if !c.Converted || c.ID == synced.ID || indexerEndpoint(c) != indexerEndpoint(synced) {
			continue
		}
		c.Categories, c.AnimeCategories = withoutMediaTypes(c.Categories, covered), withoutMediaTypes(c.AnimeCategories, covered)
		if len(c.Categories) == 0 && len(c.AnimeCategories) == 0 {
			err = DeleteIndexer(ctx, q, c.ID)
		} else {
			err = UpdateIndexer(ctx, q, c)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
