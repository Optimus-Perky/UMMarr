package store

import (
	"context"
	"fmt"
)

// AppSettings is the singleton app_settings row - everything editable via
// the Settings UI as an override of the env-var bootstrap. See migration
// 00014 for why empty string, not NULL, means "not set".
type AppSettings struct {
	ProwlarrBaseURL, ProwlarrAPIKey string
	DelugeBaseURL, DelugePassword   string
	AuthUsername, AuthPasswordHash  string
	Host                            HostSettings
}

// GetAppSettings reads the single app_settings row - always present,
// seeded by migration 00014.
func GetAppSettings(ctx context.Context, q Queryer) (AppSettings, error) {
	var s AppSettings
	err := q.QueryRowContext(ctx, `
		SELECT prowlarr_base_url, prowlarr_api_key, deluge_base_url, deluge_password,
		       auth_username, auth_password_hash,
		       url_base, ssl_enabled, ssl_port, ssl_cert_path, ssl_key_path, proxy_enabled, proxy_url, proxy_bypass
		FROM app_settings WHERE id = 1
	`).Scan(&s.ProwlarrBaseURL, &s.ProwlarrAPIKey, &s.DelugeBaseURL, &s.DelugePassword,
		&s.AuthUsername, &s.AuthPasswordHash,
		&s.Host.URLBase, &s.Host.SSLEnabled, &s.Host.SSLPort, &s.Host.SSLCertPath, &s.Host.SSLKeyPath, &s.Host.ProxyEnabled, &s.Host.ProxyURL, &s.Host.ProxyBypass)
	if err != nil {
		return AppSettings{}, fmt.Errorf("get app settings: %w", err)
	}
	return s, nil
}

// UpdateDownloadClientSettings saves Deluge connection settings. Takes
// effect on the next grab or poll.
func UpdateDownloadClientSettings(ctx context.Context, q Queryer, baseURL, password string) error {
	_, err := q.ExecContext(ctx, `UPDATE app_settings SET deluge_base_url = ?, deluge_password = ? WHERE id = 1`, baseURL, password)
	if err != nil {
		return fmt.Errorf("update download client settings: %w", err)
	}
	return nil
}

// UpdateAuthUsername updates just the username - called on every account
// settings save regardless of whether the password field was also filled
// in (see internal/api/settings.go's UpdateAccount handler). Takes effect
// immediately - login checks this table fresh on every attempt.
func UpdateAuthUsername(ctx context.Context, q Queryer, username string) error {
	_, err := q.ExecContext(ctx, `UPDATE app_settings SET auth_username = ? WHERE id = 1`, username)
	if err != nil {
		return fmt.Errorf("update auth username: %w", err)
	}
	return nil
}

// UpdateAuthPasswordHash updates the stored bcrypt password hash - only
// called when the account settings form's password field was non-empty
// (leaving it blank means "keep the current password").
func UpdateAuthPasswordHash(ctx context.Context, q Queryer, hash string) error {
	_, err := q.ExecContext(ctx, `UPDATE app_settings SET auth_password_hash = ? WHERE id = 1`, hash)
	if err != nil {
		return fmt.Errorf("update auth password hash: %w", err)
	}
	return nil
}

// HostSettings is Settings > General > Host (migration 00025): applied when
// UMMarr starts.
type HostSettings struct {
	URLBase      string // e.g. "/ummarr" behind a reverse proxy; "" for none
	SSLEnabled   bool
	SSLPort      int
	SSLCertPath  string
	SSLKeyPath   string
	ProxyEnabled bool
	ProxyURL     string // http://host:port or https://; socks isn't supported
	ProxyBypass  string // comma-separated hosts and CIDRs reached directly
}

// UpdateHostSettings saves the Host panel.
func UpdateHostSettings(ctx context.Context, q Queryer, h HostSettings) error {
	_, err := q.ExecContext(ctx, `UPDATE app_settings SET url_base = ?, ssl_enabled = ?, ssl_port = ?, ssl_cert_path = ?, ssl_key_path = ?,
		proxy_enabled = ?, proxy_url = ?, proxy_bypass = ? WHERE id = 1`,
		h.URLBase, h.SSLEnabled, h.SSLPort, h.SSLCertPath, h.SSLKeyPath, h.ProxyEnabled, h.ProxyURL, h.ProxyBypass)
	if err != nil {
		return fmt.Errorf("update host settings: %w", err)
	}
	return nil
}
