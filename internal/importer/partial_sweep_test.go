package importer_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
)

// CopyFile removes its own temporary file on every error path, but nothing
// runs after SIGKILL, so a copy killed mid-write strands one. They are
// hidden and no copy resumes them, so they pile up unseen - 16GB of them on
// the live library by 2026-09-16. A later copy into the same folder clears
// the abandoned ones.
func TestCopyFile_ClearsAbandonedPartialsInTheDestinationFolder(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.mkv")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	destDir := filepath.Join(dir, "library")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatalf("make dest dir: %v", err)
	}

	old := filepath.Join(destDir, ".Show - S01E01.mkv.123456.partial")
	fresh := filepath.Join(destDir, ".Show - S01E02.mkv.654321.partial")
	unrelated := filepath.Join(destDir, "Show - S01E03.mkv")
	hiddenOther := filepath.Join(destDir, ".nfo-cache")
	for _, f := range []string{old, fresh, unrelated, hiddenOther} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	// Only the old one is abandoned; the fresh one models a copy running
	// right now, which must never be swept.
	stale := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, stale, stale); err != nil {
		t.Fatalf("age the partial: %v", err)
	}

	dest := filepath.Join(destDir, "Show - S01E04.mkv")
	if err := importer.CopyFile(src, dest, importer.Permissions{}); err != nil {
		t.Fatalf("copy: %v", err)
	}

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the abandoned partial should have been removed")
	}
	for _, f := range []string{fresh, unrelated, hiddenOther} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("%s should have been left alone: %v", filepath.Base(f), err)
		}
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "payload" {
		t.Errorf("the copy itself must still land: %q, %v", got, err)
	}
}

// Reclaiming space is never a reason to fail an import: an unremovable
// partial is logged and skipped.
func TestCopyFile_SucceedsWhenAPartialCannotBeRemoved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the directory permissions this relies on")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "source.mkv")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	destDir := filepath.Join(dir, "library")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatalf("make dest dir: %v", err)
	}
	stuck := filepath.Join(destDir, ".Show - S01E01.mkv.1.partial")
	if err := os.WriteFile(stuck, []byte("x"), 0o644); err != nil {
		t.Fatalf("write partial: %v", err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stuck, old, old); err != nil {
		t.Fatalf("age the partial: %v", err)
	}

	dest := filepath.Join(destDir, "Show - S01E02.mkv")
	if err := importer.CopyFile(src, dest, importer.Permissions{}); err != nil {
		t.Fatalf("the copy should still succeed: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("the copy should have landed: %v", err)
	}
}
