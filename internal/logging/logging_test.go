package logging

import (
	"bytes"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags, prevLevel := log.Writer(), log.Flags(), CurrentLevel()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prevOut); log.SetFlags(prevFlags); SetLevel(prevLevel) })
	return &buf
}

// Each level adds to the one below: Standard writes errors, warnings and
// info; Verbose adds Debugf; Diagnostic adds Tracef.
func TestLevelsGateWhatIsWritten(t *testing.T) {
	buf := capture(t)
	for _, level := range Levels {
		buf.Reset()
		SetLevel(level)
		Errorf("e")
		Warnf("w")
		Infof("i")
		Debugf("d")
		Tracef("t")
		got := buf.String()
		for _, want := range []string{TagError + "e", TagWarn + "w", TagInfo + "i"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: want %q always written, got %q", level, want, got)
			}
		}
		if strings.Contains(got, TagDebug+"d") != (level >= Verbose) {
			t.Errorf("%s: verbose message written = %v", level, !(level >= Verbose))
		}
		if strings.Contains(got, TagTrace+"t") != (level == Diagnostic) {
			t.Errorf("%s: diagnostic message written = %v", level, level != Diagnostic)
		}
	}
}

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]Level{"": Standard, "Standard": Standard, "verbose": Verbose, "debug": Verbose, "DIAGNOSTIC": Diagnostic, "trace": Diagnostic} {
		if got, ok := ParseLevel(in); !ok || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	if _, ok := ParseLevel("loud"); ok {
		t.Error("want an unknown level refused")
	}
}

// Keys and passwords never reach the log.
func TestRedactURL(t *testing.T) {
	u, _ := url.Parse("https://user:hunter2@indexer.example/api?t=search&apikey=SECRET&q=heat&X-Plex-Token=TOKEN")
	got := RedactURL(u)
	if strings.Contains(got, "SECRET") || strings.Contains(got, "TOKEN") || strings.Contains(got, "hunter2") {
		t.Fatalf("secret left in %q", got)
	}
	if !strings.Contains(got, "q=heat") || !strings.Contains(got, "t=search") {
		t.Fatalf("want the harmless parameters kept, got %q", got)
	}
}

// The file rolls over at its size limit, keeping the newest Keep old files.
func TestFileRollsOver(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs", "ummarr.txt")
	f, err := OpenFile(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	line := strings.Repeat("x", 59) + "\n" // 60 bytes: two don't fit in 100
	for i := 0; i < 5; i++ {
		if _, err := f.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"ummarr.txt", "ummarr.1.txt", "ummarr.2.txt"} {
		if info, err := os.Stat(filepath.Join(dir, "logs", name)); err != nil || info.Size() != 60 {
			t.Errorf("%s: want one 60-byte line, got %v %v", name, info, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "logs", "ummarr.3.txt")); !os.IsNotExist(err) {
		t.Errorf("want only two old files kept, found a third")
	}
}
