package transmission

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/downloadclient"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
)

const hash = "0123456789abcdef0123456789abcdef01234567"

type fake struct {
	mu       sync.Mutex
	calls    []string
	added    []any
	removed  []any
	sessions int
}

func (f *fake) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Transmission answers a call with no session id with 409 and the id.
	if r.Header.Get("X-Transmission-Session-Id") != "token" {
		f.sessions++
		w.Header().Set("X-Transmission-Session-Id", "token")
		w.WriteHeader(http.StatusConflict)
		return
	}
	var req struct {
		Method    string         `json:"method"`
		Arguments map[string]any `json:"arguments"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	f.calls = append(f.calls, req.Method)
	reply := func(args any) {
		json.NewEncoder(w).Encode(map[string]any{"result": "success", "arguments": args})
	}
	switch req.Method {
	case "torrent-add":
		f.added = append(f.added, req.Arguments)
		reply(map[string]any{"torrent-added": map[string]any{"hashString": hash, "name": "Inception.2010"}})
	case "torrent-get":
		reply(map[string]any{"torrents": []map[string]any{{
			"hashString": hash, "name": "Inception.2010", "status": 6, "percentDone": 1, "leftUntilDone": 0,
			"totalSize": 8 << 30, "downloadDir": "/downloads", "error": 0, "errorString": "",
			"files": []map[string]any{{"name": "Inception.2010/movie.mkv", "length": 8 << 30}},
		}, {
			"hashString": "ffff", "name": "Broken", "status": 4, "percentDone": 0.5, "leftUntilDone": 100,
			"totalSize": 200, "downloadDir": "/downloads", "error": 3, "errorString": "Tracker gone",
		}}})
	case "torrent-remove":
		f.removed = append(f.removed, req.Arguments)
		reply(nil)
	case "session-get":
		reply(map[string]any{"version": "4.0.5"})
	default:
		reply(nil)
	}
}

func newTestClient(t *testing.T) (*Client, *fake) {
	t.Helper()
	f := &fake{}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL, Label: "ummarr", Mapping: downloadclient.PathMapping{Remote: "/downloads", Local: "/data/downloads"}})
	if err != nil {
		t.Fatal(err)
	}
	return c, f
}

func TestTransmission(t *testing.T) {
	ctx := context.Background()
	c, f := newTestClient(t)

	if err := c.Test(ctx); err != nil {
		t.Fatalf("test: %v", err)
	}
	id, err := c.Add(ctx, &newznab.FetchedRelease{Kind: newznab.KindMagnet, MagnetURI: "magnet:?xt=urn:btih:" + hash})
	if err != nil || id != hash {
		t.Fatalf("add: %q %v", id, err)
	}
	if got := f.added[0].(map[string]any)["labels"]; got == nil {
		t.Errorf("want the label sent, got %+v", f.added[0])
	}
	if f.sessions != 1 {
		t.Errorf("want the session id fetched once and reused, got %d handshakes", f.sessions)
	}

	statuses, err := c.Statuses(ctx, []string{hash, "ffff"})
	if err != nil {
		t.Fatal(err)
	}
	done := statuses[hash]
	if !done.IsFinished || done.SavePath != "/data/downloads" || len(done.Files) != 1 || done.Files[0].Path != "Inception.2010/movie.mkv" {
		t.Errorf("want the finished torrent mapped with its files, got %+v", done)
	}
	if bad := statuses["ffff"]; !bad.Failed || bad.Message != "Tracker gone" {
		t.Errorf("want the errored torrent reported failed, got %+v", bad)
	}

	if err := c.SetSeedRatio(ctx, hash, 2); err != nil {
		t.Fatalf("seed ratio: %v", err)
	}
	if err := c.Remove(ctx, hash, true); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if args := f.removed[0].(map[string]any); args["delete-local-data"] != true {
		t.Errorf("want the data deleted too, got %+v", args)
	}
	if _, err := c.Add(ctx, &newznab.FetchedRelease{Kind: newznab.KindNZB}); err == nil {
		t.Errorf("want a usenet release refused")
	}
}
