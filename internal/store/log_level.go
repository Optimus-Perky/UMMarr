package store

import (
	"context"
	"fmt"
)

// GetLogLevel reads the saved log level's name (internal/logging.ParseLevel
// reads it).
func GetLogLevel(ctx context.Context, q Queryer) (string, error) {
	var level string
	if err := q.QueryRowContext(ctx, `SELECT log_level FROM app_settings WHERE id = 1`).Scan(&level); err != nil {
		return "", fmt.Errorf("get log level: %w", err)
	}
	return level, nil
}

// SetLogLevel saves the log level's name.
func SetLogLevel(ctx context.Context, q Queryer, level string) error {
	if _, err := q.ExecContext(ctx, `UPDATE app_settings SET log_level = ? WHERE id = 1`, level); err != nil {
		return fmt.Errorf("save log level: %w", err)
	}
	return nil
}
