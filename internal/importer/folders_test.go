package importer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
)

func TestUnmappedFolders(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"Inception (2010)", "Unknown Movie (1999)", "Various Artists", "Another (2001)"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	writeFile(t, filepath.Join(root, "stray.mkv"), "not a folder", 0o644)

	got, err := importer.UnmappedFolders(root+"/", []string{filepath.Join(root, "Inception (2010)")}, "Various Artists")
	if err != nil {
		t.Fatalf("unmapped folders: %v", err)
	}
	if got != 2 {
		t.Fatalf("want 2 unmapped (Unknown Movie, Another), got %d", got)
	}
	if _, err := importer.UnmappedFolders(filepath.Join(root, "missing"), nil); err == nil {
		t.Errorf("want an error for a root folder that doesn't exist")
	}
}
