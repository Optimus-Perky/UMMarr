package nzbget

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/downloadclient"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
)

type fake struct {
	mu       sync.Mutex
	calls    []string
	edits    []any
	destDir  string
	badLogin bool
}

func (f *fake) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if user, pass, _ := r.BasicAuth(); f.badLogin || user != "nzbget" || pass != "pw" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var req struct {
		Method string `json:"method"`
		Params []any  `json:"params"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	f.calls = append(f.calls, req.Method)
	reply := func(result any) { json.NewEncoder(w).Encode(map[string]any{"version": "1.1", "result": result}) }
	switch req.Method {
	case "version":
		reply("21.1")
	case "append":
		reply(7)
	case "listgroups":
		reply([]map[string]any{{"NZBID": 8, "NZBName": "Downloading.Show", "Status": "DOWNLOADING", "FileSizeLo": 1000, "RemainingSizeLo": 250}})
	case "history":
		reply([]map[string]any{
			{"NZBID": 7, "Name": "Inception.2010", "Status": "SUCCESS/ALL", "FileSizeLo": 500, "DestDir": f.destDir},
			{"NZBID": 9, "Name": "Broken.Release", "Status": "FAILURE/PAR", "FileSizeLo": 100},
		})
	case "editqueue":
		f.edits = append(f.edits, req.Params)
		if req.Params[0] == "GroupDelete" {
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "not in queue"}})
			return
		}
		reply(true)
	default:
		reply(nil)
	}
}

func TestNZBGet(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	done := filepath.Join(dir, "Inception.2010")
	os.MkdirAll(done, 0o755)
	os.WriteFile(filepath.Join(done, "movie.mkv"), []byte("x"), 0o644)

	f := &fake{destDir: "/remote/Inception.2010"}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	c, err := New(Options{BaseURL: srv.URL, Username: "nzbget", Password: "pw", Category: "ummarr",
		Mapping: downloadclient.PathMapping{Remote: "/remote", Local: dir}})
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Test(ctx); err != nil {
		t.Fatalf("test: %v", err)
	}
	id, err := c.Add(ctx, &newznab.FetchedRelease{Kind: newznab.KindNZB, FileName: "inception.nzb", Data: []byte("<nzb/>")})
	if err != nil || id != "7" {
		t.Fatalf("add: %q %v", id, err)
	}
	if _, err := c.Add(ctx, &newznab.FetchedRelease{Kind: newznab.KindMagnet}); err == nil {
		t.Errorf("want a torrent refused")
	}

	statuses, err := c.Statuses(ctx, []string{"7", "8", "9"})
	if err != nil {
		t.Fatal(err)
	}
	finished := statuses["7"]
	if !finished.IsFinished || len(finished.Files) != 1 || finished.Files[0].Path != "Inception.2010/movie.mkv" || finished.SavePath != dir {
		t.Errorf("want the finished download's files found on disk, got %+v", finished)
	}
	if downloading := statuses["8"]; downloading.IsFinished || downloading.Progress != 0.75 {
		t.Errorf("want the queued download at 75%%, got %+v", downloading)
	}
	if failed := statuses["9"]; !failed.Failed {
		t.Errorf("want FAILURE/PAR reported as failed, got %+v", failed)
	}

	if err := c.Remove(ctx, "7", true); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(f.edits) != 2 || f.edits[1].([]any)[0] != "HistoryFinalDelete" {
		t.Errorf("want the history entry deleted with its files after the queue delete failed, got %+v", f.edits)
	}

	f.badLogin = true
	if err := c.Test(ctx); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("want a clear message for bad credentials, got %v", err)
	}
}
