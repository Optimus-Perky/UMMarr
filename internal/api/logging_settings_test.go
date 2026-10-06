package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/logging"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Settings -> General -> Logging saves the level and applies it at once;
// System -> Logs offers the log file for download.
func TestLoggingSettingsAndLogFile(t *testing.T) {
	db := openTestDB(t)
	logFile := filepath.Join(t.TempDir(), "ummarr.txt")
	if err := os.WriteFile(logFile, []byte("2026/10/06 12:00:00 [info] hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, LogFile: logFile}))
	t.Cleanup(srv.Close)
	previous := logging.CurrentLevel()
	t.Cleanup(func() { logging.SetLevel(previous) })

	resp, err := http.Get(srv.URL + "/settings/general")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{`name="log_level"`, `value="verbose"`, `value="diagnostic"`, logFile} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("want %q on the General tab", want)
		}
	}

	resp, err = http.PostForm(srv.URL+"/settings/logging", url.Values{"log_level": {"diagnostic"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Diagnostic") || logging.CurrentLevel() != logging.Diagnostic {
		t.Fatalf("want the level applied, got %q at %s", body, logging.CurrentLevel())
	}
	if saved, _ := store.GetLogLevel(t.Context(), db); saved != "diagnostic" {
		t.Fatalf("want diagnostic saved, got %q", saved)
	}

	resp, err = http.Get(srv.URL + "/system/logs/file")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "[info] hello") || !strings.Contains(resp.Header.Get("Content-Disposition"), "ummarr.txt") {
		t.Fatalf("want the log file as a download, got %d %q", resp.StatusCode, body)
	}
}
