package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// A database one migration behind is copied into backups/ before Open
// migrates it; a brand-new database isn't.
func TestOpen_BacksUpADatabaseBeforeMigratingIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ummarr.db")

	migrations, err := func() (goose.Migrations, error) {
		goose.SetBaseFS(migrationsFS)
		return goose.CollectMigrations("migrations", 0, goose.MaxVersion)
	}()
	if err != nil {
		t.Fatal(err)
	}
	last, _ := migrations.Last()
	older, err := sql.Open("sqlite", path+"?"+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(older, "migrations", last.Version-1); err != nil {
		t.Fatalf("migrate to the previous version: %v", err)
	}
	older.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.Close()
	entries, _ := os.ReadDir(filepath.Join(dir, "backups"))
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "ummarr-backup-premigration-v") {
		t.Fatalf("want one pre-migration backup, got %v", entries)
	}

	// Opening again: nothing left to migrate, so no second backup.
	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	db.Close()
	if entries, _ := os.ReadDir(filepath.Join(dir, "backups")); len(entries) != 1 {
		t.Fatalf("want no new backup when nothing migrates, got %d files", len(entries))
	}
}

func TestOpen_NewDatabaseIsNotBackedUp(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "new.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := os.Stat(filepath.Join(dir, "backups")); !os.IsNotExist(err) {
		t.Fatalf("want no backups folder for a new database, got %v", err)
	}
}
