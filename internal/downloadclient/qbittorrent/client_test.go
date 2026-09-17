package qbittorrent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/downloadclient"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
)

type fake struct {
	mu       sync.Mutex
	loggedIn bool
	added    []string // urls or file names
	category string
}

func (f *fake) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/api/v2/auth/login":
		r.ParseForm()
		if r.FormValue("username") == "admin" && r.FormValue("password") == "pw" {
			f.loggedIn = true
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "s3cret", Path: "/"})
			w.Write([]byte("Ok."))
			return
		}
		w.Write([]byte("Fails."))
	default:
		if c, err := r.Cookie("SID"); err != nil || c.Value != "s3cret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/v2/app/version":
			w.Write([]byte("v5.0.0"))
		case "/api/v2/torrents/add":
			r.ParseMultipartForm(1 << 20)
			if urls := r.FormValue("urls"); urls != "" {
				f.added = append(f.added, urls)
			}
			if _, hdr, err := r.FormFile("torrents"); err == nil {
				f.added = append(f.added, hdr.Filename)
			}
			f.category = r.FormValue("category")
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			json.NewEncoder(w).Encode([]map[string]any{{"hash": "C12FE1C06BBA254A9DC9F519B335AA7C1367A88A", "name": "Inception.2010", "state": "uploading", "progress": 1, "save_path": "/downloads/complete", "size": 12, "amount_left": 0}})
		case "/api/v2/torrents/files":
			json.NewEncoder(w).Encode([]map[string]any{{"name": "Inception.2010/inception.mkv", "size": 10}, {"name": "Inception.2010/sample.mkv", "size": 2}})
		case "/api/v2/torrents/setShareLimits":
			r.ParseForm()
			if r.FormValue("ratioLimit") != "1.50" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Write([]byte("Ok."))
		default:
			http.NotFound(w, r)
		}
	}
}

func TestClient(t *testing.T) {
	f := &fake{}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()
	c, err := New(Options{BaseURL: srv.URL, Username: "admin", Password: "pw", Category: "ummarr", Mapping: downloadclient.PathMapping{Remote: "/downloads", Local: "/data/torrents"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Test(ctx); err != nil {
		t.Fatalf("test: %v", err)
	}
	id, err := c.Add(ctx, &newznab.FetchedRelease{Kind: newznab.KindMagnet, MagnetURI: "magnet:?xt=urn:btih:C12FE1C06BBA254A9DC9F519B335AA7C1367A88A"})
	if err != nil || id != "c12fe1c06bba254a9dc9f519b335aa7c1367a88a" {
		t.Fatalf("add magnet: %s %v", id, err)
	}
	if f.category != "ummarr" || len(f.added) != 1 || !strings.HasPrefix(f.added[0], "magnet:") {
		t.Fatalf("want the magnet added under the category, got %+v", f)
	}
	statuses, err := c.Statuses(ctx, []string{id})
	if err != nil {
		t.Fatal(err)
	}
	st := statuses[id]
	if !st.IsFinished || st.SavePath != "/data/torrents/complete" || len(st.Files) != 2 || st.Files[0].Path != "Inception.2010/inception.mkv" {
		t.Fatalf("want a finished, path-mapped status with files, got %+v", st)
	}
	if err := c.SetSeedRatio(ctx, id, 1.5); err != nil {
		t.Fatalf("seed ratio: %v", err)
	}
	// A bad password is reported plainly.
	bad, _ := New(Options{BaseURL: srv.URL, Username: "admin", Password: "wrong"})
	if err := bad.Test(ctx); err == nil || !strings.Contains(err.Error(), "username and password") {
		t.Fatalf("want a login error, got %v", err)
	}
}
