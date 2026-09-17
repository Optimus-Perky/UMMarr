package api_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/backup"
	"github.com/Optimus-Perky/UMMarr/internal/health"
	"github.com/Optimus-Perky/UMMarr/internal/logbuf"
	"github.com/Optimus-Perky/UMMarr/internal/tasks"
)

// TestSystemPage: every tab renders from the services behind it, tasks
// can be run, and backups made and listed.
func TestSystemPage(t *testing.T) {
	db := openTestDB(t)
	dir := t.TempDir()
	scheduler := tasks.New()
	ran := make(chan struct{}, 1)
	scheduler.Register(&tasks.Task{Name: "Ping", Description: "test task", Run: func(context.Context) error { ran <- struct{}{}; return nil }})
	logs := &logbuf.Buffer{}
	logs.Write([]byte("something failed badly\nall fine\n"))
	backups := &backup.Service{DB: db, DBPath: filepath.Join(dir, "x.db"), Dir: filepath.Join(dir, "backups")}
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Tasks: scheduler, Health: &health.Checker{DB: db}, Logs: logs, Backups: backups, DBPath: filepath.Join(dir, "x.db"), StartedAt: time.Now()}))
	t.Cleanup(srv.Close)

	_, body := get(t, srv, "/system")
	if !strings.Contains(body, "About") || !strings.Contains(body, `href="/system/health"`) {
		t.Fatalf("want the status tab, got:\n%s", body)
	}
	_, body = get(t, srv, "/system/health")
	if !strings.Contains(body, "No TMDB token") || !strings.Contains(body, "No indexers are enabled") {
		t.Fatalf("want health issues listed, got:\n%s", body)
	}
	_, body = get(t, srv, "/system/tasks")
	if !strings.Contains(body, "Ping") || !strings.Contains(body, "test task") {
		t.Fatalf("want the task listed, got:\n%s", body)
	}
	if resp, _ := postForm(t, srv, "/system/tasks/Ping/run", nil); !strings.Contains(resp.Header.Get("HX-Redirect"), "Started") {
		t.Fatalf("want the task started, got %q", resp.Header.Get("HX-Redirect"))
	}
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("task didn't run")
	}
	_, body = get(t, srv, "/system/logs?level=error")
	if !strings.Contains(body, "something failed badly") || strings.Contains(body, "all fine") {
		t.Fatalf("want only the error line, got:\n%s", body)
	}
	if resp, _ := postForm(t, srv, "/system/backups", nil); !strings.Contains(resp.Header.Get("HX-Redirect"), "created") {
		t.Fatalf("want a backup created, got %q", resp.Header.Get("HX-Redirect"))
	}
	_, body = get(t, srv, "/system/backups")
	if !strings.Contains(body, "ummarr-backup-manual-") || !strings.Contains(body, "/download") {
		t.Fatalf("want the backup listed, got:\n%s", body)
	}
	_, body = get(t, srv, "/system/updates")
	if !strings.Contains(body, "Updates") {
		t.Fatalf("want the updates tab, got:\n%s", body)
	}
}
