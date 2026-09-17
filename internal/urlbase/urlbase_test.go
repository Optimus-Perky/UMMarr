package urlbase

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddleware(t *testing.T) {
	inner := http.NewServeMux()
	inner.HandleFunc("GET /movies", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<a href="/tv">TV</a><img src="/static/x.png"><button hx-post="/movies/1/search" hx-get="/movies?x=1">go</button><a href="https://example.com/">out</a>`))
	})
	inner.HandleFunc("POST /save", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("HX-Redirect", "/settings")
		w.WriteHeader(http.StatusOK)
	})
	inner.HandleFunc("GET /api/v3/system/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"urlBase":"/"}`))
	})
	h := Middleware("ummarr/", inner)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ummarr/movies", nil))
	body := rec.Body.String()
	for _, want := range []string{`href="/ummarr/tv"`, `src="/ummarr/static/x.png"`, `hx-post="/ummarr/movies/1/search"`, `hx-get="/ummarr/movies?x=1"`, `href="https://example.com/"`} {
		if !strings.Contains(body, want) {
			t.Errorf("want %s in %s", want, body)
		}
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ummarr/save", nil))
	if got := rec.Header().Get("HX-Redirect"); got != "/ummarr/settings" {
		t.Errorf("HX-Redirect %q", got)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ummarr/api/v3/system/status", nil))
	if rec.Body.String() != `{"urlBase":"/"}` {
		t.Errorf("JSON must be left alone, got %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != "/ummarr/" {
		t.Errorf("want / redirected to the base, got %d %s", rec.Code, rec.Header().Get("Location"))
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/other/movies", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("want paths outside the base refused, got %d", rec.Code)
	}
	if Middleware("", inner) != inner || Normalize(" /x/ ") != "/x" {
		t.Errorf("normalisation")
	}
}
