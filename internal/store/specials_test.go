package store_test

import (
	"context"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Specials (season 0) are one-offs, recaps and webisodes that usually have
// no air date and often no release at all, so they are not monitored by
// default - otherwise every search pass looks for episodes that will never
// be found. Real seasons are still monitored on creation.
func TestUpsertSeason_SpecialsAreNotMonitoredByDefault(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seriesID := seedSeriesForPhantoms(t, db)

	for _, tc := range []struct {
		season        int
		wantMonitored bool
	}{
		{season: 0, wantMonitored: false},
		{season: 1, wantMonitored: true},
		{season: 2, wantMonitored: true},
	} {
		id, err := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: tc.season})
		if err != nil {
			t.Fatalf("upsert season %d: %v", tc.season, err)
		}
		var monitored bool
		if err := db.QueryRowContext(ctx, `SELECT monitored FROM seasons WHERE id = ?`, id).Scan(&monitored); err != nil {
			t.Fatalf("read season %d: %v", tc.season, err)
		}
		if monitored != tc.wantMonitored {
			t.Errorf("season %d monitored = %v, want %v", tc.season, monitored, tc.wantMonitored)
		}
	}
}

// Refreshing a series must not undo a monitoring choice - the upsert only
// ever updates the poster for a season that already exists.
func TestUpsertSeason_KeepsAMonitoringChoiceOnRefresh(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seriesID := seedSeriesForPhantoms(t, db)

	id, err := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: 3})
	if err != nil {
		t.Fatalf("upsert season: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE seasons SET monitored = 0 WHERE id = ?`, id); err != nil {
		t.Fatalf("unmonitor: %v", err)
	}

	if _, err := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: 3, Poster: "new.jpg"}); err != nil {
		t.Fatalf("re-upsert season: %v", err)
	}

	var monitored bool
	if err := db.QueryRowContext(ctx, `SELECT monitored FROM seasons WHERE id = ?`, id).Scan(&monitored); err != nil {
		t.Fatalf("read season: %v", err)
	}
	if monitored {
		t.Error("a refresh turned monitoring back on")
	}
}

// Migration 37 switches off the specials already recorded as monitored.
func TestMigration37_UnmonitorsExistingSpecials(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seriesID := seedSeriesForPhantoms(t, db)

	specials, err := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: 0})
	if err != nil {
		t.Fatalf("upsert specials: %v", err)
	}
	real, err := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: 1})
	if err != nil {
		t.Fatalf("upsert real season: %v", err)
	}
	// Put them in the state the old default left behind.
	if _, err := db.ExecContext(ctx, `UPDATE seasons SET monitored = 1 WHERE id IN (?, ?)`, specials, real); err != nil {
		t.Fatalf("seed monitored: %v", err)
	}

	sqlText, err := migrationSQL("00037_unmonitor_specials.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if _, err := db.ExecContext(ctx, sqlText); err != nil {
		t.Fatalf("run migration 37: %v", err)
	}

	var specialsMonitored, realMonitored bool
	if err := db.QueryRowContext(ctx, `SELECT monitored FROM seasons WHERE id = ?`, specials).Scan(&specialsMonitored); err != nil {
		t.Fatalf("read specials: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT monitored FROM seasons WHERE id = ?`, real).Scan(&realMonitored); err != nil {
		t.Fatalf("read real season: %v", err)
	}
	if specialsMonitored {
		t.Error("season 0 should have been unmonitored")
	}
	if !realMonitored {
		t.Error("a real season must be left monitored")
	}
}
