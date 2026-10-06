package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestQualityDefinitions_ShowAndSave(t *testing.T) {
	db := openTestDB(t)
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db}))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/settings/quality")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `name="max_Bluray-1080p"`) || !strings.Contains(string(body), `name="min_Remux-2160p" value="30"`) {
		t.Fatalf("want a row per quality with the seeded minimums, got:\n%s", body)
	}

	form := url.Values{"min_Bluray-1080p": {"5"}, "preferred_Bluray-1080p": {"25"}, "max_Bluray-1080p": {"60"}}
	resp, err = http.PostForm(srv.URL+"/settings/quality-definitions", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("HX-Redirect") != "/settings/quality" {
		t.Fatalf("want a redirect back to the tab after saving, got status %d", resp.StatusCode)
	}
	defs, err := store.QualityDefinitionsByQuality(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if got := defs["Bluray-1080p"]; got.MinSize != 5 || got.PreferredSize != 25 || got.MaxSize != 60 {
		t.Fatalf("want 5/25/60 saved, got %+v", got)
	}
	// Every box is posted on a real save; a quality left out is cleared.
	if got := defs["Remux-2160p"]; got.MinSize != 0 {
		t.Fatalf("want an unposted box saved as 0, got %+v", got)
	}

	// A minimum above the maximum saves nothing.
	resp, err = http.PostForm(srv.URL+"/settings/quality-definitions", url.Values{"min_SDTV": {"50"}, "max_SDTV": {"10"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Nothing was saved") || !strings.Contains(string(body), "SDTV: the minimum is bigger than the maximum") {
		t.Fatalf("want the problem reported, got %s", body)
	}
	if defs, _ := store.QualityDefinitionsByQuality(context.Background(), db); defs["Bluray-1080p"].MaxSize != 60 {
		t.Fatalf("a rejected save changed the definitions: %+v", defs["Bluray-1080p"])
	}
}
