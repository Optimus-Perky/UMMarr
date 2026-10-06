package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"

	"github.com/Optimus-Perky/UMMarr/internal/store"
	"testing"
	"time"
)

// The database opened in its default rollback-journal mode takes an
// exclusive lock on the whole file to write, so a writer blocks every
// reader and a reader blocks the writer. That is where UMMarr's "database
// is locked" failures came from. These pragmas have to reach every
// connection in the pool, not just the first.
func TestOpen_AppliesItsPragmasToEveryConnection(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	want := map[string]string{
		"journal_mode": "wal",
		"synchronous":  "1", // NORMAL
		"cache_size":   "-64000",
		"foreign_keys": "1",
		"busy_timeout": "60000",
	}

	// Hold several connections open at once so the checks cannot all land
	// on the same pooled connection.
	const conns = 4
	held := make([]*sql.Conn, conns)
	for i := range held {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("open connection %d: %v", i, err)
		}
		held[i] = c
		defer c.Close()
	}

	for i, c := range held {
		for pragma, expected := range want {
			var got string
			if err := c.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
				t.Fatalf("connection %d: read %s: %v", i, pragma, err)
			}
			if got != expected {
				t.Errorf("connection %d: %s = %q, want %q", i, pragma, got, expected)
			}
		}
	}
}

// database/sql opens connections without limit by default, so a busy
// UMMarr competes with itself for SQLite's single write lock and each
// connection carries its own page cache.
func TestOpen_BoundsTheConnectionPool(t *testing.T) {
	db := openTestDB(t)
	if got := db.Stats().MaxOpenConnections; got != 8 {
		t.Errorf("MaxOpenConnections = %d, want 8", got)
	}
}

// What WAL buys, stated as behaviour: an open read no longer blocks a
// write. Under the old rollback journal this deadlocked until the reader
// finished - the shape that stalled migration 34 for twenty minutes.
func TestOpen_AReaderDoesNotBlockAWriter(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO t (v) VALUES ('a')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// A long-lived read transaction, left open across the write.
	reader, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin read: %v", err)
	}
	defer reader.Rollback() //nolint:errcheck
	var v string
	if err := reader.QueryRowContext(ctx, `SELECT v FROM t WHERE id = 1`).Scan(&v); err != nil {
		t.Fatalf("read: %v", err)
	}

	done := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := db.ExecContext(ctx, `INSERT INTO t (v) VALUES ('b')`)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("write while a read was open: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the write blocked behind an open read - WAL is not in effect")
	}
	wg.Wait()
}

// WAL is a property of the database file, so it survives reopening.
func TestOpen_WALSurvivesReopening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")
	for i := 0; i < 2; i++ {
		db := openDBAt(t, path)
		var mode string
		if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
			t.Fatalf("open %d: read journal_mode: %v", i, err)
		}
		if mode != "wal" {
			t.Errorf("open %d: journal_mode = %q, want wal", i, mode)
		}
		db.Close()
	}
}

func openDBAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("open db at %s: %v", path, err)
	}
	return db
}

// Two transactions that each read and then write, at the same moment. With
// SQLite's default DEFERRED transactions the second to write fails at once
// with "database is locked" - busy_timeout can't save a transaction that
// has already read. Taking the write lock up front makes them queue.
func TestWithTx_ConcurrentReadThenWriteTransactionsQueue(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE counter (n INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO counter VALUES (0)`); err != nil {
		t.Fatal(err)
	}
	const workers = 6
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.WithTx(ctx, db, func(tx *sql.Tx) error {
				var n int
				if err := tx.QueryRowContext(ctx, `SELECT n FROM counter`).Scan(&n); err != nil {
					return err
				}
				time.Sleep(5 * time.Millisecond) // widen the window
				_, err := tx.ExecContext(ctx, `UPDATE counter SET n = ?`, n+1)
				return err
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("transaction failed: %v", err)
		}
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT n FROM counter`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != workers {
		t.Fatalf("want every increment kept (%d), got %d - a lost update", workers, n)
	}
}
