package sync

import (
	"context"
	"database/sql"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// effectiveDelugeConfig merges Settings -> Download client over the .env
// bootstrap values - a saved value wins whenever it is non-empty.
func effectiveDelugeConfig(ctx context.Context, db *sql.DB, bootstrapBaseURL, bootstrapPassword string) (baseURL, password string) {
	baseURL, password = bootstrapBaseURL, bootstrapPassword
	if db == nil {
		return
	}
	settings, err := store.GetAppSettings(ctx, db)
	if err != nil {
		return
	}
	if settings.DelugeBaseURL != "" {
		baseURL = settings.DelugeBaseURL
	}
	if settings.DelugePassword != "" {
		password = settings.DelugePassword
	}
	return
}
