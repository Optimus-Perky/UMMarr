package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/logging"
)

// statusRecorder remembers the status a handler wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the real writer (flushing).
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// logRequests logs every request except static files at Diagnostic: method,
// path (query secrets blanked), status and time taken.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !logging.Enabled(logging.Diagnostic) || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logging.Tracef("request %s %s -> %d in %s", r.Method, logging.RedactURL(r.URL), rec.status, time.Since(started).Round(time.Millisecond))
	})
}
