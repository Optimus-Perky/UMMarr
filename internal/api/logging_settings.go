package api

import (
	"html"
	"net/http"
	"os"
	"path/filepath"

	"github.com/Optimus-Perky/UMMarr/internal/logging"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// UpdateLogLevel saves Settings -> General -> Logging and applies it at
// once - no restart.
func (h *handler) UpdateLogLevel(w http.ResponseWriter, r *http.Request) {
	level, ok := logging.ParseLevel(r.FormValue("log_level"))
	if !ok {
		renderInlineError(w, "Choose Standard, Verbose or Diagnostic.")
		return
	}
	if err := store.SetLogLevel(r.Context(), h.deps.DB, level.String()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if previous := logging.CurrentLevel(); previous != level {
		logging.SetLevel(level)
		logging.Infof("log level changed from %s to %s", previous.Label(), level.Label())
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<span class="indexer-test ok">Saved - logging at ` + html.EscapeString(level.Label()) + ` from now on.</span>`))
}

// LogFileDownload sends the current log file - System -> Logs -> Download.
func (h *handler) LogFileDownload(w http.ResponseWriter, r *http.Request) {
	if h.deps.LogFile == "" {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(h.deps.LogFile)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(h.deps.LogFile)+`"`)
	http.ServeContent(w, r, filepath.Base(h.deps.LogFile), info.ModTime(), f)
}
