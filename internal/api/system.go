package api

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/backup"
	"github.com/Optimus-Perky/UMMarr/internal/health"
	"github.com/Optimus-Perky/UMMarr/internal/logbuf"
	"github.com/Optimus-Perky/UMMarr/internal/tasks"
	"github.com/Optimus-Perky/UMMarr/internal/updates"
	"github.com/Optimus-Perky/UMMarr/internal/version"
)

// System is Sonarr's System page: Status, Health, Tasks, Logs, Backups and
// Updates as tabs.

type taskView struct {
	tasks.Status
	IntervalText, LastRunText, NextRunText, DurationText string
}

type backupView struct {
	backup.Backup
	SizeHuman, When string
}

type systemPageData struct {
	Active, PageTitle, Tab string
	Tabs                   []string

	// Status
	Commit, Built, GoVersion, OS, Arch string
	StartedAt, Uptime                  string
	DBPath, DBSize                     string
	Movies, Series, Artists, Albums    int
	MediaAnalysis                      string

	// Health
	Issues    []health.Issue
	CheckedAt string

	Tasks []taskView

	Logs     []logbuf.Line
	LogLevel string

	Backups     []backupView
	RestoreNote string

	Update        updates.Result
	UpdateChecked string
	Notice        string
}

var systemTabs = []string{"status", "health", "tasks", "logs", "backups", "updates"}

func durationText(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Minute:
		return fmt.Sprintf("%.0fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
}

func timeText(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2 Jan 2006 15:04:05")
}

func (h *handler) System(w http.ResponseWriter, r *http.Request) {
	tab := r.PathValue("tab")
	if tab == "" {
		tab = "status"
	}
	known := false
	for _, t := range systemTabs {
		if t == tab {
			known = true
		}
	}
	if !known {
		http.NotFound(w, r)
		return
	}
	data := systemPageData{Active: "system", PageTitle: "System", Tab: tab, Tabs: systemTabs, Notice: r.URL.Query().Get("notice")}
	ctx := r.Context()
	switch tab {
	case "status":
		data.Commit, data.Built, data.GoVersion, data.OS, data.Arch = version.Commit, version.Built, runtime.Version(), runtime.GOOS, runtime.GOARCH
		data.StartedAt, data.Uptime = timeText(h.deps.StartedAt), durationText(time.Since(h.deps.StartedAt))
		data.DBPath = h.deps.DBPath
		if info, err := os.Stat(h.deps.DBPath); err == nil {
			data.DBSize = humanizeBytes(info.Size())
		}
		h.deps.DB.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM movies), (SELECT COUNT(*) FROM series), (SELECT COUNT(*) FROM artists), (SELECT COUNT(*) FROM albums)`).Scan(&data.Movies, &data.Series, &data.Artists, &data.Albums)
		data.MediaAnalysis = h.mediaAnalysisText(ctx)
	case "health":
		if h.deps.Health != nil {
			issues, at := h.deps.Health.Last()
			if at.IsZero() {
				issues, at = h.deps.Health.Check(ctx), time.Now()
			}
			data.Issues, data.CheckedAt = issues, timeText(at)
		}
	case "tasks":
		if h.deps.Tasks != nil {
			for _, st := range h.deps.Tasks.Statuses() {
				v := taskView{Status: st, IntervalText: "Manual", LastRunText: timeText(st.LastRun), NextRunText: "-", DurationText: durationText(st.LastDuration)}
				if st.Interval > 0 {
					v.IntervalText, v.NextRunText = durationText(st.Interval), timeText(st.NextRun)
				}
				data.Tasks = append(data.Tasks, v)
			}
		}
	case "logs":
		data.LogLevel = r.URL.Query().Get("level")
		if h.deps.Logs != nil {
			for _, line := range h.deps.Logs.Lines(0) {
				if data.LogLevel == "" || line.Level == data.LogLevel || (data.LogLevel == "warn" && line.Level == "error") {
					data.Logs = append(data.Logs, line)
				}
			}
		}
	case "backups":
		if h.deps.Backups != nil {
			list, _ := h.deps.Backups.List()
			for _, b := range list {
				data.Backups = append(data.Backups, backupView{Backup: b, SizeHuman: humanizeBytes(b.Size), When: timeText(b.Time)})
			}
			if _, err := os.Stat(backup.RestorePath(h.deps.DBPath)); err == nil {
				data.RestoreNote = "A restore is staged and will be applied when UMMarr next starts."
			}
		}
	case "updates":
		if h.deps.Updates != nil {
			data.Update = h.deps.Updates.Last()
			data.UpdateChecked = timeText(data.Update.CheckedAt)
		}
	}
	h.renderPage(w, "system", data)
}

func (h *handler) RunTask(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if h.deps.Tasks == nil {
		http.Error(w, "tasks aren't available", http.StatusServiceUnavailable)
		return
	}
	notice := "Started " + name + "."
	if err := h.deps.Tasks.RunNow(r.Context(), name); err != nil {
		notice = err.Error()
	}
	w.Header().Set("HX-Redirect", "/system/tasks?notice="+urlQuery(notice))
	w.WriteHeader(http.StatusOK)
}

func (h *handler) CreateBackup(w http.ResponseWriter, r *http.Request) {
	if h.deps.Backups == nil {
		http.Error(w, "backups aren't available", http.StatusServiceUnavailable)
		return
	}
	notice := "Backup created."
	if b, err := h.deps.Backups.Create(r.Context(), "manual"); err != nil {
		notice = err.Error()
	} else {
		notice = "Backup " + b.Name + " created."
	}
	w.Header().Set("HX-Redirect", "/system/backups?notice="+urlQuery(notice))
	w.WriteHeader(http.StatusOK)
}

func (h *handler) DownloadBackup(w http.ResponseWriter, r *http.Request) {
	if h.deps.Backups == nil {
		http.NotFound(w, r)
		return
	}
	path, err := h.deps.Backups.Path(r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+r.PathValue("name")+"\"")
	http.ServeFile(w, r, path)
}

func (h *handler) DeleteBackup(w http.ResponseWriter, r *http.Request) {
	if h.deps.Backups == nil {
		http.NotFound(w, r)
		return
	}
	notice := "Backup deleted."
	if err := h.deps.Backups.Delete(r.PathValue("name")); err != nil {
		notice = err.Error()
	}
	w.Header().Set("HX-Redirect", "/system/backups?notice="+urlQuery(notice))
	w.WriteHeader(http.StatusOK)
}

// RestoreBackup stages a backup and restarts, so the server comes back on
// it. Docker's restart policy brings the process back after it exits.
func (h *handler) RestoreBackup(w http.ResponseWriter, r *http.Request) {
	if h.deps.Backups == nil {
		http.NotFound(w, r)
		return
	}
	if err := h.deps.Backups.Stage(r.PathValue("name")); err != nil {
		w.Header().Set("HX-Redirect", "/system/backups?notice="+urlQuery(err.Error()))
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("HX-Redirect", "/system/backups?notice="+urlQuery("Restore staged - UMMarr is restarting to apply it. Reload in a few seconds."))
	w.WriteHeader(http.StatusOK)
	if h.deps.Restart != nil {
		go func() {
			time.Sleep(time.Second)
			h.deps.Restart()
		}()
	}
}

func (h *handler) CheckUpdates(w http.ResponseWriter, r *http.Request) {
	if h.deps.Updates != nil {
		h.deps.Updates.Check(r.Context())
	}
	w.Header().Set("HX-Redirect", "/system/updates")
	w.WriteHeader(http.StatusOK)
}

func urlQuery(s string) string {
	return strings.NewReplacer(" ", "+", "&", "%26", "#", "%23", "?", "%3F", "\n", " ").Replace(s)
}
