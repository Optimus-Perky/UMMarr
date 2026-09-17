// Package backup copies the SQLite database aside and brings a copy back -
// Sonarr's System → Backup. A restore lands on the next start: the server
// swaps the file in before opening the database.
package backup

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Backup is one file in the backup folder.
type Backup struct {
	Name string
	Size int64
	Time time.Time
}

// Service manages backups of DBPath in Dir.
type Service struct {
	DB     *sql.DB
	DBPath string
	Dir    string
	Keep   int // newest scheduled backups kept; 0 keeps all
}

// RestorePath is the file the server swaps in on start when it exists.
func RestorePath(dbPath string) string { return dbPath + ".restore" }

// ApplyPending swaps a pending restore over the database, keeping the old
// database beside it. Call before opening the database.
func ApplyPending(dbPath string) (applied bool, err error) {
	pending := RestorePath(dbPath)
	if _, err := os.Stat(pending); err != nil {
		return false, nil
	}
	if _, err := os.Stat(dbPath); err == nil {
		if err := os.Rename(dbPath, dbPath+".pre-restore-"+time.Now().Format("20060102-150405")); err != nil {
			return false, fmt.Errorf("move the current database aside: %w", err)
		}
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		os.Remove(dbPath + suffix)
	}
	if err := os.Rename(pending, dbPath); err != nil {
		return false, fmt.Errorf("apply restore: %w", err)
	}
	return true, nil
}

func (s *Service) name(t time.Time, kind string) string {
	return fmt.Sprintf("ummarr-backup-%s-%s.db", kind, t.Format("2006-01-02-150405"))
}

// Create writes a consistent copy of the live database.
func (s *Service) Create(ctx context.Context, kind string) (Backup, error) {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return Backup{}, err
	}
	path := filepath.Join(s.Dir, s.name(time.Now(), kind))
	for i := 2; ; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			break
		}
		path = strings.TrimSuffix(filepath.Join(s.Dir, s.name(time.Now(), kind)), ".db") + fmt.Sprintf("-%d.db", i)
	}
	if _, err := s.DB.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return Backup{}, fmt.Errorf("back up database: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Backup{}, err
	}
	return Backup{Name: info.Name(), Size: info.Size(), Time: info.ModTime()}, nil
}

// Scheduled makes a backup and prunes old scheduled ones beyond Keep.
func (s *Service) Scheduled(ctx context.Context) error {
	if _, err := s.Create(ctx, "scheduled"); err != nil {
		return err
	}
	if s.Keep <= 0 {
		return nil
	}
	backups, err := s.List()
	if err != nil {
		return err
	}
	scheduled := 0
	for _, b := range backups { // newest first
		if !strings.Contains(b.Name, "-scheduled-") {
			continue
		}
		scheduled++
		if scheduled > s.Keep {
			os.Remove(filepath.Join(s.Dir, b.Name))
		}
	}
	return nil
}

// List lists backups, newest first.
func (s *Service) List() ([]Backup, error) {
	entries, err := os.ReadDir(s.Dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Backup
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "ummarr-backup-") || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Backup{Name: e.Name(), Size: info.Size(), Time: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

// Path is where a named backup lives, refusing names that leave the folder.
func (s *Service) Path(name string) (string, error) {
	if name == "" || name != filepath.Base(name) || !strings.HasPrefix(name, "ummarr-backup-") || !strings.HasSuffix(name, ".db") {
		return "", fmt.Errorf("not a backup name")
	}
	path := filepath.Join(s.Dir, name)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("no backup called %s", name)
	}
	return path, nil
}

// Delete removes a backup.
func (s *Service) Delete(name string) error {
	path, err := s.Path(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// Stage copies a backup to the restore path; it takes effect when the
// server next starts.
func (s *Service) Stage(name string) error {
	path, err := s.Path(name)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(RestorePath(s.DBPath), data, 0o644)
}
