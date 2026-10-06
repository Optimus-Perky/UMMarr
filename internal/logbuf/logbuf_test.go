package logbuf

import (
	"fmt"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/logging"
)

// Tagged lines get their level from the tag, which is dropped from the
// text; untagged lines (a library writing to the log) are judged by their
// words.
func TestLevelsFromTags(t *testing.T) {
	b := &Buffer{}
	fmt.Fprintf(b, "2026/10/06 12:00:00 %sdecision: rejected\n", logging.TagTrace)
	fmt.Fprintf(b, "2026/10/06 12:00:01 %ssearch: asking Nyaa\n", logging.TagDebug)
	fmt.Fprintf(b, "2026/10/06 12:00:02 %sindexer Nyaa: timeout\n", logging.TagWarn)
	fmt.Fprintf(b, "2026/10/06 12:00:03 %sall good, no error here\n", logging.TagInfo)
	fmt.Fprintf(b, "2026/10/06 12:00:04 goose: migration failed\n")
	want := []struct{ level, text string }{
		{"error", "2026/10/06 12:00:04 goose: migration failed"},
		{"info", "2026/10/06 12:00:03 all good, no error here"},
		{"warn", "2026/10/06 12:00:02 indexer Nyaa: timeout"},
		{"verbose", "2026/10/06 12:00:01 search: asking Nyaa"},
		{"diagnostic", "2026/10/06 12:00:00 decision: rejected"},
	}
	got := b.Lines(0)
	if len(got) != len(want) {
		t.Fatalf("want %d lines, got %d", len(want), len(got))
	}
	for i, w := range want {
		if got[i].Level != w.level || got[i].Text != w.text {
			t.Errorf("line %d: want %s %q, got %s %q", i, w.level, w.text, got[i].Level, got[i].Text)
		}
	}
}
