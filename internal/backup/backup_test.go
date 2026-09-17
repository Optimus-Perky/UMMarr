package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestBackupAndRestore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ummarr.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateQualityProfile(context.Background(), db, "Any"); err != nil {
		t.Fatal(err)
	}
	s := &Service{DB: db, DBPath: dbPath, Dir: filepath.Join(dir, "backups"), Keep: 1}
	b, err := s.Create(context.Background(), "manual")
	if err != nil || b.Size == 0 {
		t.Fatalf("create: %+v %v", b, err)
	}
	if err := s.Scheduled(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Scheduled(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	scheduled := 0
	for _, x := range list {
		if x.Name != b.Name {
			scheduled++
		}
	}
	if len(list) != 2 || scheduled != 1 {
		t.Fatalf("want the manual backup plus one kept scheduled backup, got %+v", list)
	}
	if _, err := s.Path("../etc/passwd"); err == nil {
		t.Fatal("want names outside the folder refused")
	}

	// Stage the manual backup, then change the live DB; applying the
	// pending restore on "restart" brings the backup back.
	if err := s.Stage(b.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateQualityProfile(context.Background(), db, "Extra"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	applied, err := ApplyPending(dbPath)
	if err != nil || !applied {
		t.Fatalf("apply: %v %v", applied, err)
	}
	db2, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	profiles, _ := store.ListQualityProfiles(context.Background(), db2)
	if len(profiles) != 1 || profiles[0].Name != "Any" {
		t.Fatalf("want the backup's single profile back, got %+v", profiles)
	}
	if matches, _ := filepath.Glob(dbPath + ".pre-restore-*"); len(matches) != 1 {
		t.Fatalf("want the replaced database kept aside, got %v", matches)
	}
	if err := s.Delete(b.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, b.Name)); !os.IsNotExist(err) {
		t.Fatal("want the backup deleted")
	}
}
