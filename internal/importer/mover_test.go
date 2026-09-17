package importer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
)

func TestCopyFile_CopiesIntoNewDirectoryAndKeepsSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	if err := os.WriteFile(src, []byte("movie bytes"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	dest := filepath.Join(dir, "nested", "dest.mkv")
	if err := importer.CopyFile(src, dest, importer.Permissions{}); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}

	gotDest, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(gotDest) != "movie bytes" {
		t.Errorf("want dest content 'movie bytes', got %q", gotDest)
	}

	// The core correctness property this whole design exists for: the
	// source must still exist afterward so a download client can keep
	// seeding it.
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("want source file to still exist after CopyFile, got: %v", err)
	}
}

func TestCopyFile_FailureLeavesNoPartialDestAndKeepsSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	if err := os.WriteFile(src, []byte("movie bytes"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("/dev/full not available in this environment")
	}

	if err := importer.CopyFile(src, "/dev/full", importer.Permissions{}); err == nil {
		t.Fatalf("want an error copying into /dev/full")
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("want source file to still exist after a failed copy, got: %v", err)
	}
}

func TestCopyFileIfDifferent_SamePathDoesNotDestroyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "movie.mkv")
	want := "original movie bytes - must survive"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// The regression case this function exists for: CopyFile(path, path)
	// would truncate the file via os.Create's O_TRUNC while a separate
	// read handle from os.Open is still open on it, destroying its
	// contents. CopyFileIfDifferent must recognize src==dest and skip
	// the copy entirely.
	if err := importer.CopyFileIfDifferent(path, path, importer.Permissions{}); err != nil {
		t.Fatalf("CopyFileIfDifferent(path, path): %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != want {
		t.Fatalf("file was altered by a same-path call: want %q, got %q", want, got)
	}
}

func TestCopyFileIfDifferent_DifferentPathsStillCopies(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	if err := os.WriteFile(src, []byte("movie bytes"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	dest := filepath.Join(dir, "dest.mkv")

	if err := importer.CopyFileIfDifferent(src, dest, importer.Permissions{}); err != nil {
		t.Fatalf("CopyFileIfDifferent: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != "movie bytes" {
		t.Fatalf("want dest content 'movie bytes', got %q", got)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("want source file to still exist, got: %v", err)
	}
}

// libraryDir makes a folder with an exact mode, independent of the test
// process's umask, to stand in for a share's library root.
func libraryDir(t *testing.T, mode os.FileMode) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "library")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("mkdir library: %v", err)
	}
	if err := os.Chmod(dir, mode); err != nil {
		t.Fatalf("chmod library: %v", err)
	}
	return dir
}

func permOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode() & (os.ModePerm | os.ModeSetgid | os.ModeSticky)
}

func writeSource(t *testing.T) string {
	t.Helper()
	// 0600 so the copy can't pass by inheriting the source's mode.
	src := filepath.Join(t.TempDir(), "download.mkv")
	if err := os.WriteFile(src, []byte("episode bytes"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return src
}

// TestCopyFile_NewFoldersAndFileMirrorParentPermissions covers the case
// that locked Plex out of an imported series: folders UMMarr created came
// out 0755 and files 0640 under a 0777 share whose other apps rely on the
// "other" bits.
func TestCopyFile_NewFoldersAndFileMirrorParentPermissions(t *testing.T) {
	for _, tc := range []struct {
		name             string
		parent, wantFile os.FileMode
	}{
		{"unraid share 777", 0o777, 0o666},
		{"group-writable 775", 0o775, 0o664},
		{"private 750", 0o750, 0o640},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib := libraryDir(t, tc.parent)
			dest := filepath.Join(lib, "Show", "Season 01", "Show - S01E01.mkv")
			if err := importer.CopyFile(writeSource(t), dest, importer.Permissions{}); err != nil {
				t.Fatalf("copy: %v", err)
			}
			for _, d := range []string{filepath.Join(lib, "Show"), filepath.Join(lib, "Show", "Season 01")} {
				if got := permOf(t, d); got != tc.parent {
					t.Errorf("new folder %s: want %v, got %v", filepath.Base(d), tc.parent, got)
				}
			}
			if got := permOf(t, dest); got != tc.wantFile {
				t.Errorf("copied file: want %v, got %v", tc.wantFile, got)
			}
		})
	}
}

// TestCopyFile_LeavesExistingFolderPermissionsAlone - only folders UMMarr
// creates get changed; one that already exists keeps whatever it had.
func TestCopyFile_LeavesExistingFolderPermissionsAlone(t *testing.T) {
	lib := libraryDir(t, 0o777)
	existing := filepath.Join(lib, "Show")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatalf("mkdir existing: %v", err)
	}
	if err := os.Chmod(existing, 0o700); err != nil {
		t.Fatalf("chmod existing: %v", err)
	}

	dest := filepath.Join(existing, "Season 01", "Show - S01E01.mkv")
	if err := importer.CopyFile(writeSource(t), dest, importer.Permissions{}); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if got := permOf(t, existing); got != 0o700 {
		t.Errorf("existing folder: want it left at 0700, got %v", got)
	}
	if got := permOf(t, filepath.Join(existing, "Season 01")); got != 0o700 {
		t.Errorf("new folder: want it to mirror its own parent (0700), got %v", got)
	}
	if got := permOf(t, dest); got != 0o600 {
		t.Errorf("copied file: want 0600, got %v", got)
	}
}

// TestRenameFileIfDifferent_NewFolderMirrorsParentFileKeepsItsMode - the
// library scan's rename creates season folders like an import does, but a
// file already in the library keeps the permissions it arrived with.
func TestRenameFileIfDifferent_NewFolderMirrorsParentFileKeepsItsMode(t *testing.T) {
	lib := libraryDir(t, 0o777)
	src := filepath.Join(lib, "Show.S01E01.mkv")
	if err := os.WriteFile(src, []byte("episode bytes"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(src, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	dest := filepath.Join(lib, "Season 01", "Show - S01E01.mkv")
	if err := importer.RenameFileIfDifferent(src, dest, importer.Permissions{}); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := permOf(t, filepath.Join(lib, "Season 01")); got != 0o777 {
		t.Errorf("new folder: want 0777, got %v", got)
	}
	if got := permOf(t, dest); got != 0o644 {
		t.Errorf("renamed file: want its own 0644 kept, got %v", got)
	}
}

// TestCopyFile_NewFolderKeepsSetgid - a setgid share folder hands its bit to
// new subfolders; the chmod that mirrors permissions must not strip it.
func TestCopyFile_NewFolderKeepsSetgid(t *testing.T) {
	lib := libraryDir(t, os.ModeSetgid|0o775)
	if permOf(t, lib)&os.ModeSetgid == 0 {
		t.Skip("filesystem does not keep the setgid bit")
	}
	dest := filepath.Join(lib, "Show", "Show - S01E01.mkv")
	if err := importer.CopyFile(writeSource(t), dest, importer.Permissions{}); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if got, want := permOf(t, filepath.Join(lib, "Show")), os.ModeSetgid|0o775; got != want {
		t.Errorf("new folder: want %v, got %v", want, got)
	}
}
