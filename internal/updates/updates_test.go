package updates

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCheck: the checker asks GitHub which branch the repository uses, so a
// repo on master isn't checked against main - which answers 422 and used to
// read as "a private repository needs a token".
func TestCheck(t *testing.T) {
	var repoLookups int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/Optimus-Perky/UMMarr":
			repoLookups++
			json.NewEncoder(w).Encode(map[string]any{"default_branch": "master", "private": false})
		case "/repos/Optimus-Perky/UMMarr/commits/master":
			json.NewEncoder(w).Encode(map[string]any{"sha": "abcdef1234567", "commit": map[string]any{"message": "Newest thing\n\nbody", "author": map[string]any{"date": "2026-09-15T10:00:00Z"}}})
		case "/repos/Optimus-Perky/UMMarr/commits/main":
			w.WriteHeader(http.StatusUnprocessableEntity) // what GitHub really answers
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &Checker{Repo: "Optimus-Perky/UMMarr", Current: "1234567", APIBase: srv.URL}
	r := c.Check(context.Background())
	if r.Error != "" || r.Latest != "abcdef1" || !r.Behind || r.Message != "Newest thing" {
		t.Fatalf("got %+v", r)
	}
	c.Current = "abcdef1-dirty"
	if r := c.Check(context.Background()); r.Behind {
		t.Fatalf("want the head build reported current, got %+v", r)
	}
	if repoLookups != 1 {
		t.Errorf("want the branch asked once and remembered, got %d lookups", repoLookups)
	}

	// A branch someone set by hand is used as given.
	pinned := &Checker{Repo: "Optimus-Perky/UMMarr", Branch: "main", Current: "1234567", APIBase: srv.URL}
	if r := pinned.Check(context.Background()); r.Error == "" {
		t.Errorf("want the pinned branch's 422 reported, got %+v", r)
	}

	missing := &Checker{Repo: "nobody/nothing", Current: "1234567", APIBase: srv.URL}
	if r := missing.Check(context.Background()); r.Error == "" {
		t.Fatal("want a failed check reported")
	}
}
