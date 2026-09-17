package importer

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// dirPermBits is what a new folder takes from its parent. Setgid and sticky
// are included because a plain chmod would otherwise strip a setgid bit the
// kernel had just inherited from the parent.
const dirPermBits = os.ModePerm | os.ModeSetgid | os.ModeSticky

// Permissions decides the permissions of folders and files UMMarr creates.
// The zero value gives each one its parent folder's permissions.
type Permissions struct {
	// Explicit uses FolderMode for new folders, and FolderMode without its
	// execute bits for new files, instead of copying the parent folder.
	Explicit   bool
	FolderMode os.FileMode
	// SetGroup changes the group of everything UMMarr creates to Group.
	SetGroup bool
	Group    int
}

// ParseFolderMode reads an octal mode such as "755" or "2775" into a folder
// mode, carrying setgid and sticky over into Go's separate mode bits. The
// owner must keep rwx: without it UMMarr couldn't write into the folders it
// creates.
func ParseFolderMode(s string) (os.FileMode, error) {
	if len(s) != 3 && len(s) != 4 {
		return 0, fmt.Errorf("%q isn't a 3 or 4 digit octal mode", s)
	}
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("%q isn't an octal mode", s)
	}
	if n&0o4000 != 0 {
		return 0, fmt.Errorf("%q sets setuid, which folders don't use", s)
	}
	if n&0o700 != 0o700 {
		return 0, fmt.Errorf("%q doesn't give the owner rwx, so UMMarr couldn't write into its own folders", s)
	}
	mode := os.FileMode(n & 0o777)
	if n&0o2000 != 0 {
		mode |= os.ModeSetgid
	}
	if n&0o1000 != 0 {
		mode |= os.ModeSticky
	}
	return mode, nil
}

func (p Permissions) folderMode(parent os.FileMode) os.FileMode {
	if p.Explicit {
		return p.FolderMode & dirPermBits
	}
	return parent & dirPermBits
}

func (p Permissions) fileMode(parent os.FileMode) os.FileMode {
	base := parent.Perm()
	if p.Explicit {
		base = p.FolderMode.Perm()
	}
	return base &^ 0o111
}

func (p Permissions) apply(path string, mode os.FileMode) error {
	if err := os.Chmod(path, mode); err != nil {
		return err
	}
	if p.SetGroup {
		return os.Lchown(path, -1, p.Group)
	}
	return nil
}

// EnsureDir creates dir and any missing parents. Each folder it creates gets
// permissions from p - by default the same as the folder it was created in.
// Folders that already exist are left exactly as they are.
//
// The mode bits matter, not just an ACL: on Unraid's /mnt/user mount, other
// containers (Plex, Sonarr, Radarr) are checked against owner/group/other bits
// only, so a folder created 0755 under a 0777 share locks out everything that
// isn't its owner.
func EnsureDir(dir string, p Permissions) error {
	dir = filepath.Clean(dir)
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", dir)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(dir)
	if parent == dir {
		return err
	}
	if err := EnsureDir(parent, p); err != nil {
		return err
	}
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		if os.IsExist(err) {
			return nil // created concurrently by someone else - not ours to change
		}
		return err
	}
	return p.apply(dir, p.folderMode(parentInfo.Mode()))
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && absA == absB
}

// CopyFile copies src to dest, creating any missing folders on the way, and
// gives dest permissions from p. Always a copy, never a move - src is still
// the file the download client is (or may be) seeding from.
//
// The copy is written to a temporary file beside dest and renamed over it, so
// an existing dest is replaced rather than truncated. If that dest is a
// hardlink to the seeding download, truncating it in place would destroy the
// download's data too.
func CopyFile(src, dest string, p Permissions) error {
	dir := filepath.Dir(dest)
	if err := EnsureDir(dir, p); err != nil {
		return fmt.Errorf("create dest dir for %s: %w", dest, err)
	}
	sweepStalePartials(dir)
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open src %s: %w", src, err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dest)+".*.partial")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", dest, err)
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("copy %s to %s: %w", src, dest, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file for %s: %w", dest, err)
	}
	parentInfo, err := os.Stat(dir)
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("stat dest dir for %s: %w", dest, err)
	}
	// A file other apps can't read is the failure this exists to prevent, so
	// don't leave one behind looking like a successful import.
	if err := p.apply(tmpPath, p.fileMode(parentInfo.Mode())); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("set permissions on %s: %w", dest, err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("move copy into place at %s: %w", dest, err)
	}
	return nil
}

// CopyFileIfDifferent copies src to dest unless they're the same path, in
// which case it's a no-op.
func CopyFileIfDifferent(src, dest string, p Permissions) error {
	if samePath(src, dest) {
		return nil
	}
	return CopyFile(src, dest, p)
}

// RenameFileIfDifferent renames src to dest, for the library scan, where
// src is already inside the destination's own movie/series/album folder
// and only needs its name corrected. Copying there would write a second
// copy of the file alongside the first, doubling the space the library
// occupies for no benefit - nothing is seeding from a file that is
// already in the library.
//
// Any folder it has to create gets permissions from p. The renamed file
// keeps its own permissions unless p is explicit or sets a group, in which
// case they're applied if UMMarr owns the file.
//
// Falls back to a copy if the rename fails for any reason (a cross-device
// layout, say, which Unraid's fuse mount can present): leaving both files
// behind is wasteful but safe, whereas failing the import is not.
func RenameFileIfDifferent(src, dest string, p Permissions) error {
	if samePath(src, dest) {
		return nil // already exactly where it belongs, under the right name
	}
	if err := EnsureDir(filepath.Dir(dest), p); err != nil {
		return fmt.Errorf("create dest dir for %s: %w", dest, err)
	}
	if err := os.Rename(src, dest); err != nil {
		return CopyFile(src, dest, p)
	}
	if p.Explicit || p.SetGroup {
		if info, err := os.Stat(dest); err == nil && ownedByUs(info) {
			if parentInfo, err := os.Stat(filepath.Dir(dest)); err == nil {
				_ = p.apply(dest, p.fileMode(parentInfo.Mode()))
			}
		}
	}
	return nil
}

func ownedByUs(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}

// ImportOptions controls how ImportFile brings a downloaded file into the
// library.
type ImportOptions struct {
	Permissions Permissions
	// UseHardlinks links dest to src instead of copying when the filesystem
	// allows it, falling back to a copy when it doesn't.
	UseHardlinks bool
	// CheckFreeSpace refuses a copy that would leave less than
	// MinimumFreeBytes available where dest is being written.
	CheckFreeSpace   bool
	MinimumFreeBytes int64
}

// ImportFile brings the downloaded file src into the library at dest, leaving
// src in place for seeding. It reports whether dest ended up a hardlink.
//
// A hardlinked file keeps the download's owner and permissions: UMMarr isn't
// its owner, and changing them would change the seeding file as well.
func ImportFile(src, dest string, opts ImportOptions) (hardlinked bool, err error) {
	if samePath(src, dest) {
		return false, nil
	}
	if opts.UseHardlinks {
		if err := EnsureDir(filepath.Dir(dest), opts.Permissions); err != nil {
			return false, fmt.Errorf("create dest dir for %s: %w", dest, err)
		}
		if linkReplacing(src, dest) == nil {
			return true, nil
		}
	}
	if opts.CheckFreeSpace {
		info, err := os.Stat(src)
		if err != nil {
			return false, fmt.Errorf("stat src %s: %w", src, err)
		}
		if err := checkFreeSpace(filepath.Dir(dest), info.Size(), opts.MinimumFreeBytes); err != nil {
			return false, err
		}
	}
	return false, CopyFile(src, dest, opts.Permissions)
}

// linkReplacing hardlinks dest to src, replacing any existing dest by
// renaming a new link over it, so a failure never leaves dest missing.
func linkReplacing(src, dest string) error {
	tmp := filepath.Join(filepath.Dir(dest), fmt.Sprintf(".%s.%d.link", filepath.Base(dest), time.Now().UnixNano()))
	if err := os.Link(src, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Renaming onto a link to the same file succeeds without doing anything,
	// which would leave tmp behind; normally tmp is already gone.
	_ = os.Remove(tmp)
	return nil
}

// FreeSpace returns the bytes available to UMMarr on the filesystem holding
// path.
func FreeSpace(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

func checkFreeSpace(dir string, size, minimum int64) error {
	existing := nearestExistingDir(dir)
	available, err := FreeSpace(existing)
	if err != nil {
		return fmt.Errorf("couldn't check free space in %s: %w (turn on Skip Free Space Check to import anyway)", existing, err)
	}
	if available-size < minimum {
		return fmt.Errorf("not enough free space in %s: %s available, the file needs %s and %s must stay free",
			existing, FormatBytes(available), FormatBytes(size), FormatBytes(minimum))
	}
	return nil
}

func nearestExistingDir(dir string) string {
	for {
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}

// FormatBytes renders n in binary units, e.g. "40.6 TiB".
func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// RecycleOrRemove takes a replaced file out of the library: into recycleBin
// (keeping its name, a numbered suffix on a clash) when one is configured,
// otherwise deleting it. A missing file counts as done.
func RecycleOrRemove(path, recycleBin string) error {
	if recycleBin == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	if err := os.MkdirAll(recycleBin, 0o755); err != nil {
		return err
	}
	base := filepath.Base(path)
	dest := filepath.Join(recycleBin, base)
	for i := 1; ; i++ {
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			break
		}
		ext := filepath.Ext(base)
		dest = filepath.Join(recycleBin, fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(base, ext), i, ext))
	}
	if err := os.Rename(path, dest); err == nil {
		return nil
	}
	// Another filesystem: copy, then remove the original.
	if err := CopyFile(path, dest, Permissions{}); err != nil {
		return err
	}
	return os.Remove(path)
}

// stalePartialAge is how long a .partial file has to sit untouched before
// a later copy into the same folder treats it as abandoned. Comfortably
// longer than any single copy takes, so a copy running right now is never
// mistaken for wreckage.
const stalePartialAge = time.Hour

// sweepStalePartials deletes abandoned .partial files in dir.
//
// CopyFile removes its own temporary file on every error path, but nothing
// runs after SIGKILL, so a copy killed mid-write - a container restart
// during an import, most often - strands one. They are hidden, nothing
// refers to them and no copy resumes them, so they accumulate silently:
// 24 files and 16GB of them on this library by 2026-09-16, five of them
// copies of one episode left by imports of the same release running on top
// of each other.
//
// Best effort by design. A file that cannot be read or removed is left
// alone and the import carries on - reclaiming space is never a reason to
// fail an import that would otherwise work.
func sweepStalePartials(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-stalePartialAge)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), ".") || !strings.HasSuffix(e.Name(), ".partial") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := os.Remove(path); err != nil {
			log.Printf("could not remove abandoned partial file %s: %v", path, err)
			continue
		}
		log.Printf("removed abandoned partial file %s (%d bytes, last written %s)",
			path, info.Size(), info.ModTime().Format(time.RFC3339))
	}
}
