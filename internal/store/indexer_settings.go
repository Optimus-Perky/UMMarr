package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// IndexerSettings is the indexer_settings row (migration 00018): Radarr's
// Settings -> Indexers -> Options, plus UMMarr's own bookkeeping.
type IndexerSettings struct {
	MinimumAge               int // minutes, usenet only
	Retention                int // days, usenet only
	MaximumSize              int // MB
	PreferIndexerFlags       bool
	AvailabilityDelay        int // days
	RSSSyncInterval          int // minutes, 0 = off
	WhitelistedHardcodedSubs string
	AllowHardcodedSubs       bool

	ProwlarrConverted bool
	LastRSSSync       sql.NullTime
	LastRSSResult     string
}

// GetIndexerSettings reads the indexer_settings row.
func GetIndexerSettings(ctx context.Context, q Queryer) (IndexerSettings, error) {
	var s IndexerSettings
	err := q.QueryRowContext(ctx, `
		SELECT minimum_age, retention, maximum_size, prefer_indexer_flags, availability_delay, rss_sync_interval,
		       whitelisted_hardcoded_subs, allow_hardcoded_subs, prowlarr_converted, last_rss_sync, last_rss_result
		FROM indexer_settings WHERE id = 1
	`).Scan(&s.MinimumAge, &s.Retention, &s.MaximumSize, &s.PreferIndexerFlags, &s.AvailabilityDelay, &s.RSSSyncInterval,
		&s.WhitelistedHardcodedSubs, &s.AllowHardcodedSubs, &s.ProwlarrConverted, &s.LastRSSSync, &s.LastRSSResult)
	if err != nil {
		return IndexerSettings{}, fmt.Errorf("get indexer settings: %w", err)
	}
	return s, nil
}

// UpdateIndexerOptions saves the Options section.
func UpdateIndexerOptions(ctx context.Context, q Queryer, s IndexerSettings) error {
	_, err := q.ExecContext(ctx, `
		UPDATE indexer_settings SET minimum_age = ?, retention = ?, maximum_size = ?, prefer_indexer_flags = ?,
		       availability_delay = ?, rss_sync_interval = ?, whitelisted_hardcoded_subs = ?, allow_hardcoded_subs = ?
		WHERE id = 1
	`, s.MinimumAge, s.Retention, s.MaximumSize, s.PreferIndexerFlags, s.AvailabilityDelay, s.RSSSyncInterval,
		s.WhitelistedHardcodedSubs, s.AllowHardcodedSubs)
	if err != nil {
		return fmt.Errorf("update indexer options: %w", err)
	}
	return nil
}

// MarkProwlarrConverted records that the old Prowlarr connection has been
// dealt with, so the conversion never runs again.
func MarkProwlarrConverted(ctx context.Context, q Queryer) error {
	if _, err := q.ExecContext(ctx, `UPDATE indexer_settings SET prowlarr_converted = 1 WHERE id = 1`); err != nil {
		return fmt.Errorf("mark prowlarr converted: %w", err)
	}
	return nil
}

// RecordRSSSync stores when RSS sync last ran and what it did.
func RecordRSSSync(ctx context.Context, q Queryer, at time.Time, result string) error {
	if _, err := q.ExecContext(ctx, `UPDATE indexer_settings SET last_rss_sync = ?, last_rss_result = ? WHERE id = 1`, at.UTC(), result); err != nil {
		return fmt.Errorf("record rss sync: %w", err)
	}
	return nil
}

func newAPIKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// EnsureAPIKey gives UMMarr an API key if it has none yet, like Radarr's
// 32-character key, and returns it.
func EnsureAPIKey(ctx context.Context, q Queryer) (string, error) {
	var key string
	if err := q.QueryRowContext(ctx, `SELECT api_key FROM app_settings WHERE id = 1`).Scan(&key); err != nil {
		return "", fmt.Errorf("get api key: %w", err)
	}
	if key != "" {
		return key, nil
	}
	return RegenerateAPIKey(ctx, q)
}

// RegenerateAPIKey replaces UMMarr's API key. Anything using the old key
// (Prowlarr's app sync) stops working until it's given the new one.
func RegenerateAPIKey(ctx context.Context, q Queryer) (string, error) {
	key, err := newAPIKey()
	if err != nil {
		return "", err
	}
	if _, err := q.ExecContext(ctx, `UPDATE app_settings SET api_key = ? WHERE id = 1`, key); err != nil {
		return "", fmt.Errorf("save api key: %w", err)
	}
	return key, nil
}

// GetAPIKey reads UMMarr's API key ("" before EnsureAPIKey has run).
func GetAPIKey(ctx context.Context, q Queryer) (string, error) {
	var key string
	if err := q.QueryRowContext(ctx, `SELECT api_key FROM app_settings WHERE id = 1`).Scan(&key); err != nil {
		return "", fmt.Errorf("get api key: %w", err)
	}
	return key, nil
}
