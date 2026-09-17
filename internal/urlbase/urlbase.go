// Package urlbase serves UMMarr under a path prefix - Sonarr's URL Base, for
// a reverse proxy that puts it at /ummarr. Requests lose the prefix on the
// way in; links, htmx targets and redirects gain it on the way out, so the
// rest of the app keeps writing paths from "/".
package urlbase

import (
	"bytes"
	"net/http"
	"strings"
)

// Normalize turns user input into "/prefix" (or "" for none).
func Normalize(base string) string {
	base = strings.Trim(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}
	return "/" + base
}

// attributes whose root-relative values get the prefix.
var attributes = []string{`href="/`, `src="/`, `action="/`, `hx-get="/`, `hx-post="/`, `hx-put="/`, `hx-delete="/`, `hx-patch="/`, `content="/`, `poster="/`}

// Rewrite prefixes every root-relative path in an HTML body.
func Rewrite(base string, body []byte) []byte {
	if base == "" {
		return body
	}
	for _, attr := range attributes {
		body = bytes.ReplaceAll(body, []byte(attr), []byte(attr[:len(attr)-1]+base+"/"))
	}
	body = bytes.ReplaceAll(body, []byte(`url('/`), []byte(`url('`+base+`/`))
	body = bytes.ReplaceAll(body, []byte(`url("/`), []byte(`url("`+base+`/`))
	return body
}

type responseWriter struct {
	http.ResponseWriter
	base        string
	status      int
	buf         bytes.Buffer
	wroteHeader bool
	rewrite     bool
}

func (w *responseWriter) fixHeaders() {
	for _, name := range []string{"HX-Redirect", "Location", "HX-Push-Url", "HX-Replace-Url"} {
		if v := w.Header().Get(name); strings.HasPrefix(v, "/") && !strings.HasPrefix(v, w.base+"/") && v != w.base {
			w.Header().Set(name, w.base+v)
		}
	}
}

func (w *responseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.fixHeaders()
	ct := w.Header().Get("Content-Type")
	w.rewrite = strings.HasPrefix(ct, "text/html") || strings.HasPrefix(ct, "text/css")
	if w.rewrite {
		w.Header().Del("Content-Length")
		return
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(p))
		}
		w.WriteHeader(http.StatusOK)
	}
	if w.rewrite {
		return w.buf.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

func (w *responseWriter) flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if !w.rewrite {
		return
	}
	body := Rewrite(w.base, w.buf.Bytes())
	w.ResponseWriter.WriteHeader(w.status)
	w.ResponseWriter.Write(body)
}

// Middleware serves next under base. With no base it's next itself.
func Middleware(base string, next http.Handler) http.Handler {
	base = Normalize(base)
	if base == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == base || r.URL.Path == "/":
			http.Redirect(w, r, base+"/", http.StatusTemporaryRedirect)
			return
		case !strings.HasPrefix(r.URL.Path, base+"/"):
			http.NotFound(w, r)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(r.URL.Path, base)
		if r2.URL.RawPath != "" {
			r2.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, base)
		}
		rw := &responseWriter{ResponseWriter: w, base: base}
		next.ServeHTTP(rw, r2)
		rw.flush()
	})
}
