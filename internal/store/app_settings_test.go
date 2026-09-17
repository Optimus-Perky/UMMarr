package store_test

import (
	"context"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestGetAppSettings_SeededDefaults(t *testing.T) {
	db := openTestDB(t)
	settings, err := store.GetAppSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("get app settings: %v", err)
	}
	settings.Host = store.HostSettings{} // the Host defaults are deliberately non-empty
	if settings != (store.AppSettings{}) {
		t.Fatalf("want all-empty defaults, got %+v", settings)
	}
}

func TestUpdateDownloadClientSettings(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := store.UpdateDownloadClientSettings(ctx, db, "http://media-server:8112", "the-password"); err != nil {
		t.Fatalf("update download client settings: %v", err)
	}
	settings, err := store.GetAppSettings(ctx, db)
	if err != nil {
		t.Fatalf("get app settings: %v", err)
	}
	if settings.DelugeBaseURL != "http://media-server:8112" || settings.DelugePassword != "the-password" {
		t.Fatalf("download client settings not persisted: %+v", settings)
	}
}

func TestUpdateAuthUsernameAndPasswordHash(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := store.UpdateAuthUsername(ctx, db, "testuser"); err != nil {
		t.Fatalf("update auth username: %v", err)
	}
	if err := store.UpdateAuthPasswordHash(ctx, db, "a-bcrypt-hash"); err != nil {
		t.Fatalf("update auth password hash: %v", err)
	}
	settings, err := store.GetAppSettings(ctx, db)
	if err != nil {
		t.Fatalf("get app settings: %v", err)
	}
	if settings.AuthUsername != "testuser" || settings.AuthPasswordHash != "a-bcrypt-hash" {
		t.Fatalf("auth credentials not persisted: %+v", settings)
	}
}
