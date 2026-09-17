package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/customformat"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// TestCustomFormats_Settings: add a format, see it listed, score it on a
// quality profile.
func TestCustomFormats_Settings(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	profileID, err := store.CreateQualityProfile(t.Context(), db, "Any")
	if err != nil {
		t.Fatal(err)
	}

	_, body := get(t, srv, "/settings/custom-formats")
	if !strings.Contains(body, "Custom Formats") || !strings.Contains(body, "Import JSON") {
		t.Fatalf("want the Custom Formats tab, got:\n%s", body)
	}
	_, body = get(t, srv, "/settings/custom-formats/new")
	if !strings.Contains(body, `name="c0_implementation"`) || !strings.Contains(body, "Release Title") {
		t.Fatalf("want a blank condition row in the form, got:\n%s", body)
	}

	form := url.Values{"name": {"x265"}, "c0_implementation": {customformat.ReleaseTitle}, "c0_value": {`x265|HEVC`}, "c0_required": {"on"}}
	resp, body := postForm(t, srv, "/settings/custom-formats", form)
	if resp.Header.Get("HX-Redirect") != "/settings/custom-formats" {
		t.Fatalf("want the format saved, got:\n%s", body)
	}
	formats, _ := store.ListCustomFormats(t.Context(), db)
	if len(formats) != 1 || formats[0].Name != "x265" || len(formats[0].Conditions) != 1 || !formats[0].Conditions[0].Required {
		t.Fatalf("want one saved format, got %+v", formats)
	}

	// A pattern Go can't compile is refused, not saved.
	bad := url.Values{"name": {"lookahead"}, "c0_implementation": {customformat.ReleaseTitle}, "c0_value": {"^(?!x265)"}}
	_, body = postForm(t, srv, "/settings/custom-formats", bad)
	if !strings.Contains(body, "invalid or unsupported Perl syntax") && !strings.Contains(body, "error parsing regexp") {
		t.Errorf("want the bad pattern explained, got:\n%s", body)
	}
	if list, _ := store.ListCustomFormats(t.Context(), db); len(list) != 1 {
		t.Errorf("want nothing saved from the bad form, got %d", len(list))
	}

	// Score it on the profile.
	id := itoa(formats[0].ID)
	_, body = get(t, srv, "/settings/quality-profiles/"+itoa(profileID))
	if !strings.Contains(body, `name="format_score_`+id+`"`) || !strings.Contains(body, "Minimum custom format score") {
		t.Fatalf("want the format scoring table on the profile page, got:\n%s", body)
	}
	scoreForm := url.Values{"format_score_" + id: {"25"}, "min_format_score": {"-50"}, "cutoff_format_score": {"25"}, "cutoff": {""}, "upgrade_allowed": {"on"}}
	postForm(t, srv, "/settings/quality-profiles/"+itoa(profileID)+"/items", scoreForm)
	scores, _ := store.GetProfileFormatScores(t.Context(), db, profileID)
	if scores.Scores[formats[0].ID] != 25 || scores.MinScore != -50 || scores.CutoffScore != 25 {
		t.Fatalf("want the scores saved, got %+v", scores)
	}

	if resp := deleteRequest(t, srv, "/settings/custom-formats/"+id); resp.StatusCode != 200 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if list, _ := store.ListCustomFormats(t.Context(), db); len(list) != 0 {
		t.Errorf("want the format deleted, got %d", len(list))
	}
}

// TestCustomFormats_Import: TRaSH JSON imports, and what can't be applied is
// reported rather than silently dropped.
func TestCustomFormats_Import(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)

	const trash = `[
	  {"name": "Remux", "specifications": [{"name": "Remux", "implementation": "QualityModifierSpecification", "required": true, "fields": {"value": 5}}]},
	  {"name": "Lookahead", "specifications": [{"name": "x", "implementation": "ReleaseTitleSpecification", "fields": {"value": "^(?!x265)"}}]}
	]`
	_, body := postForm(t, srv, "/settings/custom-formats/import", url.Values{"from": {"radarr"}, "json": {trash}})
	if !strings.Contains(body, "Imported 1 custom format") || !strings.Contains(body, "Lookahead") {
		t.Fatalf("want one imported and the lookahead one reported, got:\n%s", body)
	}
	formats, _ := store.ListCustomFormats(t.Context(), db)
	if len(formats) != 1 || formats[0].Conditions[0].Value != "REMUX" {
		t.Fatalf("want Radarr's 5 read as REMUX, got %+v", formats)
	}

	// Importing the same JSON again keeps both, the second renamed.
	postForm(t, srv, "/settings/custom-formats/import", url.Values{"from": {"radarr"}, "json": {trash}})
	if formats, _ = store.ListCustomFormats(t.Context(), db); len(formats) != 2 || formats[1].Name != "Remux (2)" {
		t.Fatalf("want the clash renamed, got %+v", formats)
	}

	exportResp, err := http.Get(srv.URL + "/settings/custom-formats/export")
	if err != nil {
		t.Fatal(err)
	}
	defer exportResp.Body.Close()
	data, _ := io.ReadAll(exportResp.Body)
	if !strings.Contains(exportResp.Header.Get("Content-Disposition"), "ummarr-custom-formats.json") || !strings.Contains(string(data), `"QualityModifierSpecification"`) {
		t.Fatalf("want a JSON download, got %v:\n%s", exportResp.Header, data)
	}
}

// TestDownloadClients_TransmissionAndNZBGet: both can be added from Settings,
// and NZBGet counts as the usenet client.
func TestDownloadClients_TransmissionAndNZBGet(t *testing.T) {
	db := openTestDB(t)
	download := &sync.DownloadService{DB: db}
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Download: download}))
	t.Cleanup(srv.Close)

	_, body := get(t, srv, "/settings/download-clients")
	if !strings.Contains(body, "implementation=transmission") || !strings.Contains(body, "implementation=nzbget") {
		t.Fatalf("want both in the Add dialog, got:\n%s", body)
	}
	_, body = get(t, srv, "/settings/download-clients/new?implementation=nzbget")
	if !strings.Contains(body, `name="username"`) || !strings.Contains(body, `name="password"`) || strings.Contains(body, `name="api_key"`) {
		t.Fatalf("want NZBGet's username and password (no API key), got:\n%s", body)
	}

	_, saveBody := postForm(t, srv, "/settings/download-clients", url.Values{"implementation": {"transmission"}, "name": {"Transmission"}, "enabled": {"on"},
		"base_url": {"http://media-server:9091"}, "priority": {"1"}, "username": {"t"}, "password": {"pw"}, "category": {"ummarr"}})
	postForm(t, srv, "/settings/download-clients", url.Values{"implementation": {"nzbget"}, "name": {"NZBGet"}, "enabled": {"on"},
		"base_url": {"http://media-server:6789"}, "priority": {"1"}, "username": {"n"}, "password": {"pw"}})

	clients, _ := store.ListDownloadClients(t.Context(), db)
	if len(clients) != 2 {
		t.Fatalf("want both saved, got %+v; the save said:\n%s", clients, saveBody)
	}
	protocols := map[string]string{}
	for _, c := range clients {
		protocols[c.Implementation] = c.Protocol()
		if _, err := download.Build(c); err != nil {
			t.Errorf("build %s: %v", c.Implementation, err)
		}
	}
	if protocols["transmission"] != "torrent" || protocols["nzbget"] != "usenet" {
		t.Fatalf("want Transmission on torrents and NZBGet on usenet, got %v", protocols)
	}
	if p := download.EnabledProtocols(t.Context()); !p["torrent"] || !p["usenet"] {
		t.Errorf("want both protocols available, got %v", p)
	}
}
