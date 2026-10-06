package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// File is an io.Writer appending to a log file that rolls over at MaxBytes:
// ummarr.txt becomes ummarr.1.txt, the old .1 becomes .2, and so on up to
// Keep old files, the oldest dropped.
type File struct {
	Path     string
	MaxBytes int64
	Keep     int

	mu   sync.Mutex
	f    *os.File
	size int64
}

// OpenFile opens (creating) the log file at path.
func OpenFile(path string, maxBytes int64, keep int) (*File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	l := &File{Path: path, MaxBytes: maxBytes, Keep: keep}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *File) open() error {
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, info.Size()
	return nil
}

func (l *File) rotated(n int) string {
	ext := filepath.Ext(l.Path)
	return fmt.Sprintf("%s.%d%s", l.Path[:len(l.Path)-len(ext)], n, ext)
}

// Write appends p, rolling the file over first when p would take it past
// MaxBytes.
func (l *File) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	if l.MaxBytes > 0 && l.size > 0 && l.size+int64(len(p)) > l.MaxBytes {
		l.rotate()
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

func (l *File) rotate() {
	l.f.Close()
	l.f = nil
	_ = os.Remove(l.rotated(l.Keep))
	for i := l.Keep - 1; i >= 1; i-- {
		_ = os.Rename(l.rotated(i), l.rotated(i+1))
	}
	if l.Keep > 0 {
		_ = os.Rename(l.Path, l.rotated(1))
	} else {
		_ = os.Remove(l.Path)
	}
	if err := l.open(); err != nil {
		fmt.Fprintf(os.Stderr, "log file: %v\n", err)
	}
}

// Close closes the file.
func (l *File) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
