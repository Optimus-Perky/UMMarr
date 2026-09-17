package sabnzbd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/downloadclient"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
)

func TestClient(t *testing.T) {
	local := t.TempDir()
	os.MkdirAll(filepath.Join(local, "complete", "tv", "Show.S01E01"), 0o755)
	os.WriteFile(filepath.Join(local, "complete", "tv", "Show.S01E01", "show.mkv"), []byte("video"), 0o644)
	var gotCat, gotName string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") != "key" {
			json.NewEncoder(w).Encode(map[string]any{"status": false, "error": "API Key Incorrect"})
			return
		}
		switch r.URL.Query().Get("mode") {
		case "version":
			json.NewEncoder(w).Encode(map[string]any{"version": "4.3.0"})
		case "addfile":
			r.ParseMultipartForm(1 << 20)
			_, hdr, _ := r.FormFile("name")
			gotName, gotCat = hdr.Filename, r.URL.Query().Get("cat")
			json.NewEncoder(w).Encode(map[string]any{"status": true, "nzo_ids": []string{"SABnzbd_nzo_abc"}})
		case "queue":
			json.NewEncoder(w).Encode(map[string]any{"queue": map[string]any{"slots": []map[string]any{{"nzo_id": "SABnzbd_nzo_q", "filename": "Other", "status": "Downloading", "percentage": "42"}}}})
		case "history":
			json.NewEncoder(w).Encode(map[string]any{"history": map[string]any{"slots": []map[string]any{
				{"nzo_id": "SABnzbd_nzo_abc", "name": "Show.S01E01", "status": "Completed", "storage": "/downloads/complete/tv/Show.S01E01", "bytes": 5},
				{"nzo_id": "SABnzbd_nzo_bad", "name": "Broken", "status": "Failed", "fail_message": "Unpacking failed, archive requires a password"},
			}}})
		}
	}))
	defer srv.Close()
	c, err := New(Options{BaseURL: srv.URL, APIKey: "key", Category: "tv", Mapping: downloadclient.PathMapping{Remote: "/downloads", Local: local}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Test(ctx); err != nil {
		t.Fatalf("test: %v", err)
	}
	id, err := c.Add(ctx, &newznab.FetchedRelease{Kind: newznab.KindNZB, FileName: "Show.S01E01.nzb", Data: []byte("<nzb/>")})
	if err != nil || id != "SABnzbd_nzo_abc" || gotName != "Show.S01E01.nzb" || gotCat != "tv" {
		t.Fatalf("add: %s %v (%s %s)", id, err, gotName, gotCat)
	}
	statuses, err := c.Statuses(ctx, []string{"SABnzbd_nzo_abc", "SABnzbd_nzo_bad", "SABnzbd_nzo_q"})
	if err != nil {
		t.Fatal(err)
	}
	done := statuses["SABnzbd_nzo_abc"]
	if !done.IsFinished || done.SavePath != filepath.Join(local, "complete", "tv") || len(done.Files) != 1 || done.Files[0].Path != filepath.Join("Show.S01E01", "show.mkv") {
		t.Fatalf("want the completed download's files from its storage folder, got %+v", done)
	}
	if bad := statuses["SABnzbd_nzo_bad"]; !bad.Failed || bad.Message == "" {
		t.Fatalf("want the failure reported, got %+v", bad)
	}
	if q := statuses["SABnzbd_nzo_q"]; q.IsFinished || q.Progress != 0.42 {
		t.Fatalf("want the queued download in progress, got %+v", q)
	}
	wrong, _ := New(Options{BaseURL: srv.URL, APIKey: "nope"})
	if err := wrong.Test(ctx); err == nil {
		t.Fatalf("want the bad key reported")
	}
}
