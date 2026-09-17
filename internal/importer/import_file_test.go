package importer_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
)

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	ia, err := os.Stat(a)
	if err != nil {
		t.Fatalf("stat %s: %v", a, err)
	}
	ib, err := os.Stat(b)
	if err != nil {
		t.Fatalf("stat %s: %v", b, err)
	}
	return os.SameFile(ia, ib)
}

func TestImportFile_HardlinkSharesTheDownloadAndKeepsItsMode(t *testing.T) {
	download := filepath.Join(t.TempDir(), "Movie.2010.mkv")
	writeFile(t, download, "seeding bytes", 0o640)
	lib := libraryDir(t, 0o777)
	dest := filepath.Join(lib, "Movie (2010)", "Movie (2010).mkv")

	hardlinked, err := importer.ImportFile(download, dest, importer.ImportOptions{UseHardlinks: true})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !hardlinked || !sameFile(t, download, dest) {
		t.Fatalf("want dest hardlinked to the download (hardlinked=%v)", hardlinked)
	}
	if got := permOf(t, dest); got != 0o640 {
		t.Errorf("want the linked file to keep the download's 0640, got %v", got)
	}
	if got := permOf(t, filepath.Dir(dest)); got != 0o777 {
		t.Errorf("want the new movie folder to mirror its parent, got %v", got)
	}
}

// TestImportFile_ReplacingAHardlinkedFileLeavesTheDownloadIntact is the reason
// copies are written to a temp file and renamed into place. Once library files
// can be hardlinks of seeding downloads, overwriting one in place would write
// straight through into the download.
func TestImportFile_ReplacingAHardlinkedFileLeavesTheDownloadIntact(t *testing.T) {
	dir := t.TempDir()
	download := filepath.Join(dir, "downloads", "Movie.2010.mkv")
	writeFile(t, download, "seeding bytes", 0o644)
	dest := filepath.Join(dir, "library", "Movie (2010).mkv")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Link(download, dest); err != nil {
		t.Skipf("hardlinks unsupported here: %v", err)
	}

	t.Run("a different download copied over it", func(t *testing.T) {
		upgrade := filepath.Join(dir, "downloads", "Movie.2010.2160p.mkv")
		writeFile(t, upgrade, "a better release", 0o644)
		if _, err := importer.ImportFile(upgrade, dest, importer.ImportOptions{}); err != nil {
			t.Fatalf("import: %v", err)
		}
		if got := readFile(t, dest); got != "a better release" {
			t.Errorf("want dest replaced, got %q", got)
		}
		if got := readFile(t, download); got != "seeding bytes" {
			t.Fatalf("the seeding download was overwritten through the hardlink: %q", got)
		}
	})

	t.Run("the same download copied over its own link", func(t *testing.T) {
		if err := os.Remove(dest); err != nil {
			t.Fatalf("reset dest: %v", err)
		}
		if err := os.Link(download, dest); err != nil {
			t.Fatalf("relink: %v", err)
		}
		if _, err := importer.ImportFile(download, dest, importer.ImportOptions{}); err != nil {
			t.Fatalf("import: %v", err)
		}
		if got := readFile(t, download); got != "seeding bytes" {
			t.Fatalf("re-importing emptied the seeding download: %q", got)
		}
		if got := readFile(t, dest); got != "seeding bytes" {
			t.Errorf("want dest to hold the download's contents, got %q", got)
		}
	})
}

func TestImportFile_FreeSpaceCheck(t *testing.T) {
	download := filepath.Join(t.TempDir(), "Movie.2010.mkv")
	writeFile(t, download, "movie bytes", 0o644)
	lib := libraryDir(t, 0o777)
	dest := filepath.Join(lib, "Movie (2010)", "Movie (2010).mkv")

	_, err := importer.ImportFile(download, dest, importer.ImportOptions{CheckFreeSpace: true, MinimumFreeBytes: 1 << 62})
	if err == nil || !strings.Contains(err.Error(), "not enough free space") {
		t.Fatalf("want the copy refused for lack of space, got %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("want nothing written when refused, stat gave %v", statErr)
	}

	if _, err := importer.ImportFile(download, dest, importer.ImportOptions{CheckFreeSpace: false, MinimumFreeBytes: 1 << 62}); err != nil {
		t.Fatalf("want the copy to go ahead with the check skipped: %v", err)
	}
}

func TestImportFile_HardlinkFallsBackToCopyAcrossFilesystems(t *testing.T) {
	const other = "/dev/shm"
	download := filepath.Join(t.TempDir(), "Movie.2010.mkv")
	writeFile(t, download, "movie bytes", 0o644)
	lib, err := os.MkdirTemp(other, "ummarr-lib-")
	if err != nil {
		t.Skipf("no second filesystem at %s: %v", other, err)
	}
	t.Cleanup(func() { os.RemoveAll(lib) })
	var a, b syscall.Stat_t
	if syscall.Stat(download, &a) != nil || syscall.Stat(lib, &b) != nil || a.Dev == b.Dev {
		t.Skipf("%s is on the same filesystem as the temp dir", other)
	}

	dest := filepath.Join(lib, "Movie (2010).mkv")
	hardlinked, err := importer.ImportFile(download, dest, importer.ImportOptions{UseHardlinks: true})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if hardlinked || readFile(t, dest) != "movie bytes" {
		t.Fatalf("want a plain copy when a hardlink isn't possible (hardlinked=%v)", hardlinked)
	}
}

func TestParseFolderMode(t *testing.T) {
	cases := []struct {
		in      string
		want    os.FileMode
		wantErr bool
	}{
		{"755", 0o755, false},
		{"777", 0o777, false},
		{"2775", os.ModeSetgid | 0o775, false},
		{"644", 0, true},  // owner can't enter its own folders
		{"4755", 0, true}, // setuid
		{"75", 0, true},
		{"abc", 0, true},
		{"", 0, true},
	}
	for _, c := range cases {
		got, err := importer.ParseFolderMode(c.in)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("ParseFolderMode(%q) = %v, %v; want %v (error=%v)", c.in, got, err, c.want, c.wantErr)
		}
	}
}

func TestCopyFile_ExplicitPermissionsAndGroup(t *testing.T) {
	lib := libraryDir(t, 0o777)
	dest := filepath.Join(lib, "Show", "Season 01", "Show - S01E01.mkv")
	p := importer.Permissions{Explicit: true, FolderMode: 0o750, SetGroup: true, Group: os.Getgid()}
	if err := importer.CopyFile(writeSource(t), dest, p); err != nil {
		t.Fatalf("copy: %v", err)
	}
	for _, d := range []string{filepath.Join(lib, "Show"), filepath.Join(lib, "Show", "Season 01")} {
		if got := permOf(t, d); got != 0o750 {
			t.Errorf("new folder %s: want 0750, got %v", filepath.Base(d), got)
		}
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("file: want 0640, got %v", got)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Gid) != os.Getgid() {
		t.Errorf("want the file's group set to %d", os.Getgid())
	}
}

func TestRenameFileIfDifferent_ExplicitPermissionsApplyToAnOwnedFile(t *testing.T) {
	lib := libraryDir(t, 0o777)
	src := filepath.Join(lib, "Show.S01E01.mkv")
	writeFile(t, src, "episode bytes", 0o600)
	dest := filepath.Join(lib, "Season 01", "Show - S01E01.mkv")
	if err := importer.RenameFileIfDifferent(src, dest, importer.Permissions{Explicit: true, FolderMode: 0o755}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := permOf(t, dest); got != 0o644 {
		t.Errorf("want the renamed file set to 0644, got %v", got)
	}
}

func TestRemoveEmptyDirs(t *testing.T) {
	t.Run("keeps anything holding a file", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "Show")
		for _, d := range []string{"Season 1", "Extras/empty/deeper"} {
			if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
		}
		writeFile(t, filepath.Join(root, "Season 2", "ep.mkv"), "x", 0o644)
		writeFile(t, filepath.Join(root, "Art", ".keep"), "", 0o644)

		removed, err := importer.RemoveEmptyDirs(root)
		if err != nil {
			t.Fatalf("remove: %v", err)
		}
		if removed != 4 {
			t.Errorf("want 4 empty directories removed (Season 1, Extras, empty, deeper), got %d", removed)
		}
		for _, gone := range []string{"Season 1", "Extras"} {
			if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
				t.Errorf("want %s removed", gone)
			}
		}
		for _, kept := range []string{"Season 2/ep.mkv", "Art/.keep"} {
			if _, err := os.Stat(filepath.Join(root, kept)); err != nil {
				t.Errorf("want %s kept: %v", kept, err)
			}
		}
	})

	t.Run("removes the folder itself when nothing is left", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "Movie (2010)")
		if err := os.MkdirAll(filepath.Join(root, "Subs"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if _, err := importer.RemoveEmptyDirs(root); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Errorf("want the empty movie folder removed")
		}
	})

	t.Run("a missing folder is not an error", func(t *testing.T) {
		if removed, err := importer.RemoveEmptyDirs(filepath.Join(t.TempDir(), "nope")); err != nil || removed != 0 {
			t.Errorf("want 0, nil; got %d, %v", removed, err)
		}
	})
}

func TestExtraNames(t *testing.T) {
	cases := []struct{ extra, main, newMain, want string }{
		{"Movie.2010.en.srt", "Movie.2010.mkv", "The Matrix (1999).mkv", "The Matrix (1999).en.srt"},
		{"Subs/movie.2010.forced.EN.srt", "Movie.2010.mkv", "The Matrix (1999).mkv", "The Matrix (1999).forced.EN.srt"},
		{"English.srt", "Movie.2010.mkv", "The Matrix (1999).mkv", "The Matrix (1999).English.srt"},
		{"Movie.2010.srt", "Movie.2010.mkv", "The Matrix (1999).mkv", "The Matrix (1999).srt"},
		{"Movie.2010.nfo", "Movie.2010.mkv", "The Matrix (1999).mkv", "The Matrix (1999).nfo-orig"},
	}
	for _, c := range cases {
		if got := importer.ExtraName(c.extra, c.main, c.newMain); got != c.want {
			t.Errorf("ExtraName(%q) = %q, want %q", c.extra, got, c.want)
		}
	}
	if got := importer.KeptExtraName("Scans/cover.jpg"); got != "cover.jpg" {
		t.Errorf("KeptExtraName = %q, want cover.jpg", got)
	}
	if !importer.BelongsTo("Show.S01E01.en.srt", "Show.S01E01.mkv") || importer.BelongsTo("Show.S01E02.en.srt", "Show.S01E01.mkv") {
		t.Errorf("BelongsTo should match only files named after the main file")
	}
	exts := importer.ParseExtensions(" srt, .NFO ,,sub")
	if len(exts) != 3 || !exts[".srt"] || !exts[".nfo"] || !exts[".sub"] {
		t.Errorf("ParseExtensions = %v", exts)
	}
}
