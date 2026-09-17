package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func deleteRequest(t *testing.T, srv *httptest.Server, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete %s: %v", path, err)
	}
	resp.Body.Close()
	return resp
}

func rootFolderRow(body, path string) string {
	return regexp.MustCompile(`(?s)<tr><td>` + regexp.QuoteMeta(path) + `</td>.*?</tr>`).FindString(body)
}

func TestSettings_BuiltInRootFoldersStayAndOthersCanBeRemoved(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()

	builtIn, err := store.CreateRootFolder(ctx, db, "/data/Movies", "movie")
	if err != nil {
		t.Fatalf("create built-in: %v", err)
	}
	something, err := store.CreateRootFolder(ctx, db, "/Something", "movie")
	if err != nil {
		t.Fatalf("create /Something: %v", err)
	}
	seedTestMovieWithRootFolder(t, db, "/media/in-use")
	var inUse int64
	if err := db.QueryRow(`SELECT id FROM root_folders WHERE path = '/media/in-use'`).Scan(&inUse); err != nil {
		t.Fatalf("find in-use root folder: %v", err)
	}

	_, body := get(t, srv, "/settings/media-management")
	if row := rootFolderRow(body, "/data/Movies"); !strings.Contains(row, "Built in") || strings.Contains(row, "hx-delete") || !strings.Contains(row, "<td>Movies</td>") {
		t.Errorf("built-in row: want Built in, a readable type and no Remove button, got %q", row)
	}
	if row := rootFolderRow(body, "/media/in-use"); !strings.Contains(row, "Used by 1 movie") || strings.Contains(row, "hx-delete") {
		t.Errorf("in-use row: want why it can't be removed and no Remove button, got %q", row)
	}
	if row := rootFolderRow(body, "/Something"); !strings.Contains(row, fmt.Sprintf(`hx-delete="/settings/root-folders/%d"`, something)) || !strings.Contains(row, "Nothing on disk is deleted") {
		t.Errorf("user-added row: want a Remove button with a confirmation, got %q", row)
	}

	for _, tc := range []struct {
		name   string
		id     int64
		status int
	}{
		{"built in", builtIn, http.StatusForbidden},
		{"in use", inUse, http.StatusConflict},
		{"unknown", 999999, http.StatusNotFound},
	} {
		if resp := deleteRequest(t, srv, fmt.Sprintf("/settings/root-folders/%d", tc.id)); resp.StatusCode != tc.status {
			t.Errorf("%s: want %d, got %d", tc.name, tc.status, resp.StatusCode)
		}
	}

	resp := deleteRequest(t, srv, fmt.Sprintf("/settings/root-folders/%d", something))
	if resp.StatusCode != http.StatusOK || resp.Header.Get("HX-Redirect") != "/settings/media-management" {
		t.Fatalf("want /Something removed with a redirect back to Settings, got %d %q", resp.StatusCode, resp.Header.Get("HX-Redirect"))
	}

	var remaining []string
	rows, err := db.Query(`SELECT path FROM root_folders ORDER BY path`)
	if err != nil {
		t.Fatalf("list root folders: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan: %v", err)
		}
		remaining = append(remaining, p)
	}
	if got := strings.Join(remaining, ", "); got != "/data/Movies, /media/in-use" {
		t.Fatalf("want only /Something removed, root folders now: %s", got)
	}
}
