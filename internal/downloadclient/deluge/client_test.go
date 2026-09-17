package deluge_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/downloadclient/deluge"
)

type rpcCall struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
	ID     int    `json:"id"`
}

func newTestClient(t *testing.T, handler http.HandlerFunc) *deluge.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	c, err := deluge.New(deluge.Options{BaseURL: srv.URL, Password: "hunter2", HTTP: &http.Client{Jar: jar}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestAddMagnet_LoginThenAdd(t *testing.T) {
	var contentTypes []string
	var sawSessionCookie bool
	var calls []rpcCall

	srv := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentTypes = append(contentTypes, r.Header.Get("Content-Type"))

		var call rpcCall
		json.NewDecoder(r.Body).Decode(&call)
		calls = append(calls, call)

		switch call.Method {
		case "auth.login":
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "abc123"})
			json.NewEncoder(w).Encode(map[string]any{"result": true, "error": nil, "id": call.ID})
		case "core.add_torrent_magnet":
			if c, err := r.Cookie("_session_id"); err == nil && c.Value == "abc123" {
				sawSessionCookie = true
			}
			json.NewEncoder(w).Encode(map[string]any{"result": "deadbeef", "error": nil, "id": call.ID})
		}
	})

	c := newTestClient(t, srv)
	torrentID, err := c.AddMagnet(context.Background(), "magnet:?xt=urn:btih:deadbeef", deluge.AddOptions{DownloadLocation: "/media/movies"})
	if err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}
	if torrentID != "deadbeef" {
		t.Errorf("want torrent id deadbeef, got %q", torrentID)
	}
	if !sawSessionCookie {
		t.Errorf("want the _session_id cookie from login replayed on the add call")
	}
	for _, ct := range contentTypes {
		if ct != "application/json" {
			t.Errorf("want Content-Type application/json on every call, got %q", ct)
		}
	}
	if len(calls) != 2 || calls[0].Method != "auth.login" || calls[1].Method != "core.add_torrent_magnet" {
		t.Fatalf("want login then add, got %+v", calls)
	}
	gotOpts, _ := calls[1].Params[1].(map[string]any)
	if gotOpts["download_location"] != "/media/movies" {
		t.Errorf("want download_location passed through, got %+v", calls[1].Params)
	}
}

func TestCall_ReAuthenticatesOnSessionRejection(t *testing.T) {
	loginCount := 0
	addAttempts := 0

	srv := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call rpcCall
		json.NewDecoder(r.Body).Decode(&call)

		switch call.Method {
		case "auth.login":
			loginCount++
			http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "session-from-login"})
			json.NewEncoder(w).Encode(map[string]any{"result": true, "error": nil, "id": call.ID})
		case "core.add_torrent_magnet":
			addAttempts++
			if addAttempts == 1 {
				// Simulate an expired/rejected session on the first attempt.
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"result": "deadbeef", "error": nil, "id": call.ID})
		}
	})

	c := newTestClient(t, srv)
	torrentID, err := c.AddMagnet(context.Background(), "magnet:?xt=urn:btih:deadbeef", deluge.AddOptions{})
	if err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}
	if torrentID != "deadbeef" {
		t.Errorf("want torrent id deadbeef, got %q", torrentID)
	}
	if loginCount != 2 {
		t.Errorf("want exactly one re-login after the rejected session, got %d total logins", loginCount)
	}
	if addAttempts != 2 {
		t.Errorf("want exactly one retry of the add call, got %d attempts", addAttempts)
	}
}

func TestGetTorrentsStatus_DecodesFields(t *testing.T) {
	srv := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call rpcCall
		json.NewDecoder(r.Body).Decode(&call)

		switch call.Method {
		case "auth.login":
			json.NewEncoder(w).Encode(map[string]any{"result": true, "error": nil, "id": call.ID})
		case "core.get_torrents_status":
			json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"deadbeef": map[string]any{
						"name": "Some.Movie.2010", "state": "Downloading", "progress": 42.5,
						"is_finished": false, "save_path": "/media/movies", "total_size": 1000, "total_done": 425, "eta": 60, "ratio": 0.0,
					},
				},
				"error": nil, "id": call.ID,
			})
		}
	})

	c := newTestClient(t, srv)
	statuses, err := c.GetTorrentsStatus(context.Background(), []string{"deadbeef"})
	if err != nil {
		t.Fatalf("GetTorrentsStatus: %v", err)
	}
	s, ok := statuses["deadbeef"]
	if !ok {
		t.Fatalf("want a status for deadbeef, got %+v", statuses)
	}
	if s.Name != "Some.Movie.2010" || s.State != "Downloading" || s.Progress != 42.5 || s.SavePath != "/media/movies" {
		t.Errorf("status fields not decoded correctly: %+v", s)
	}
}

func TestGetTorrentsStatus_DecodesFiles(t *testing.T) {
	var sawFilesKey bool

	srv := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call rpcCall
		json.NewDecoder(r.Body).Decode(&call)

		switch call.Method {
		case "auth.login":
			json.NewEncoder(w).Encode(map[string]any{"result": true, "error": nil, "id": call.ID})
		case "core.get_torrents_status":
			if keys, ok := call.Params[1].([]any); ok {
				for _, k := range keys {
					if k == "files" {
						sawFilesKey = true
					}
				}
			}
			json.NewEncoder(w).Encode(map[string]any{
				"result": map[string]any{
					"deadbeef": map[string]any{
						"name": "Some.Movie.2010", "state": "Downloading", "progress": 100.0,
						"is_finished": true, "save_path": "/downloads", "total_size": 1000, "total_done": 1000, "eta": 0, "ratio": 0.0,
						"files": []map[string]any{
							{"index": 0, "path": "Some.Movie.2010/movie.mkv", "size": 900},
							{"index": 1, "path": "Some.Movie.2010/sample.mkv", "size": 100},
						},
					},
				},
				"error": nil, "id": call.ID,
			})
		}
	})

	c := newTestClient(t, srv)
	statuses, err := c.GetTorrentsStatus(context.Background(), []string{"deadbeef"})
	if err != nil {
		t.Fatalf("GetTorrentsStatus: %v", err)
	}
	if !sawFilesKey {
		t.Fatalf("want torrentStatusKeys to include \"files\"")
	}
	s := statuses["deadbeef"]
	if len(s.Files) != 2 {
		t.Fatalf("want 2 files, got %+v", s.Files)
	}
	if s.Files[0].Path != "Some.Movie.2010/movie.mkv" || s.Files[0].Size != 900 {
		t.Errorf("file[0] not decoded correctly: %+v", s.Files[0])
	}
	if s.Files[1].Path != "Some.Movie.2010/sample.mkv" || s.Files[1].Size != 100 {
		t.Errorf("file[1] not decoded correctly: %+v", s.Files[1])
	}
}

func TestNew_MissingBaseURL(t *testing.T) {
	if _, err := deluge.New(deluge.Options{}); err != deluge.ErrMissingCredential {
		t.Fatalf("want ErrMissingCredential, got %v", err)
	}
}
