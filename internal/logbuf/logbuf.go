// Package logbuf keeps the last lines the process logged, for the System →
// Logs page, alongside wherever the log normally goes.
package logbuf

import (
	"strings"
	"sync"
	"time"
)

// Line is one logged line.
type Line struct {
	Time  time.Time
	Level string // error, warn or info
	Text  string
}

// Buffer is an io.Writer that remembers the newest Capacity lines.
type Buffer struct {
	Capacity int

	mu      sync.Mutex
	lines   []Line
	partial string
}

// Write splits p into lines; a trailing fragment waits for its newline.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	text := b.partial + string(p)
	for {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			break
		}
		b.add(text[:i])
		text = text[i+1:]
	}
	b.partial = text
	return len(p), nil
}

func (b *Buffer) add(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	capacity := b.Capacity
	if capacity <= 0 {
		capacity = 2000
	}
	b.lines = append(b.lines, Line{Time: time.Now(), Level: level(text), Text: text})
	if len(b.lines) > capacity {
		b.lines = b.lines[len(b.lines)-capacity:]
	}
}

// level reads a line's severity from its wording, since the standard log
// package has none.
func level(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "panic") || strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "fatal"):
		return "error"
	case strings.Contains(lower, "warn") || strings.Contains(lower, "retry") || strings.Contains(lower, "trying again") || strings.Contains(lower, "missing"):
		return "warn"
	}
	return "info"
}

// Lines returns the newest lines first, at most n (0 for all).
func (b *Buffer) Lines(n int) []Line {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Line, 0, len(b.lines))
	for i := len(b.lines) - 1; i >= 0; i-- {
		out = append(out, b.lines[i])
		if n > 0 && len(out) >= n {
			break
		}
	}
	return out
}
