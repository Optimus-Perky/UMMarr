// Package store owns the database connection and schema migrations for
// mediamgr. Migrations are embedded into the binary (via embed.FS) rather
// than read from disk, keeping the single-static-binary deployment goal
// intact - no separate migrations directory needs to ship alongside it.
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// maxOpenConns bounds the connection pool. database/sql opens connections
// without limit by default, so a busy UMMarr ends up competing with itself
// for SQLite's single write lock, and each connection carries its own page
// cache. Eight is well clear of how deeply the code nests database calls -
// a bound below that could deadlock, with a transaction holding one
// connection while the work inside it waits for another.
const maxOpenConns = 8

// pragmas are applied to every connection in the pool.
//
//   - journal_mode(WAL): the default rollback journal takes an exclusive
//     lock on the whole database to write, so a writer blocks every reader
//     and any reader blocks the writer. That is where UMMarr's "database is
//     locked" failures came from: metadata refreshes losing rows while an
//     import copied files, and on 2026-09-16 a migration that could not
//     commit for twenty minutes because one read-only query was open. WAL
//     readers and writers do not block each other.
//   - synchronous(NORMAL): the pairing WAL is designed for. A power cut can
//     cost the last transaction or two; it cannot corrupt the database.
//   - cache_size(-64000): 64MB per connection, against a database of about
//     50MB - it is a ceiling filled on demand, not an allocation.
//   - foreign_keys(1): off by default per connection, so it cannot be left
//     to the schema.
//   - busy_timeout(60000): wait rather than fail when a write does collide.
//   - _txlock=immediate: a transaction takes the write lock when it begins.
//     SQLite's default (DEFERRED) takes it at the first write, and a
//     transaction that has already read and then finds another writer in
//     the way fails at once with SQLITE_BUSY - busy_timeout can't help,
//     because waiting would break the snapshot it has read from. Every
//     WithTx in UMMarr reads then writes, so each waits its turn instead.
const pragmas = "_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" +
	"&_pragma=cache_size(-64000)&_pragma=foreign_keys(1)&_pragma=busy_timeout(60000)" +
	"&_txlock=immediate"

// Open opens (creating if necessary) a SQLite database at path and applies
// any pending migrations.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?"+pragmas)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxOpenConns)
	db.SetConnMaxLifetime(0) // a local file - nothing goes stale

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return nil, fmt.Errorf("set goose dialect: %w", err)
	}
	if err := backupBeforeMigrating(db, path); err != nil {
		return nil, err
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return db, nil
}

// backupBeforeMigrating copies an existing database aside when this build
// is about to migrate it, so a migration that goes wrong - or a downgrade
// after one that went right - has the pre-upgrade data to go back to. The
// copy lands with the other backups (System -> Backups), where it can be
// restored like any of them. A new, empty database has nothing to keep.
func backupBeforeMigrating(db *sql.DB, path string) error {
	current, err := goose.GetDBVersion(db)
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	migrations, err := goose.CollectMigrations("migrations", 0, goose.MaxVersion)
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	last, err := migrations.Last()
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	if current == 0 || current >= last.Version {
		return nil
	}
	dir := filepath.Join(filepath.Dir(path), "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create backups folder: %w", err)
	}
	dest := filepath.Join(dir, fmt.Sprintf("ummarr-backup-premigration-v%d-%s.db", current, time.Now().Format("2006-01-02-150405")))
	if _, err := db.Exec(`VACUUM INTO ?`, dest); err != nil {
		return fmt.Errorf("back up the database before migrating it: %w", err)
	}
	log.Printf("backed up the database to %s before migrating it from version %d to %d", dest, current, last.Version)
	return nil
}
