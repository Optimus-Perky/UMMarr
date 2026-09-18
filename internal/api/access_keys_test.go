package api_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// An access key stands in for a login, so a tool can fetch a page rather
// than only the API - and is revoked on its own, unlike the API key
// Prowlarr syncs with. These check it actually opens the pages, that a
// wrong key does not, and that revoking takes effect at once.

func TestAccessKey_AuthenticatesPagesAndIsRevocable(t *testing.T) {
	srv, db := newAuthTestServer(t)
	ctx := t.Context()

	// With auth on and no key, a page sends you to the login.
	resp := requestWithoutRedirects(t, srv.URL+"/music", "")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("want a redirect to login without a key, got %d", resp.StatusCode)
	}

	key, err := store.CreateAccessKey(ctx, db, "Claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(key.Key) < 32 {
		t.Fatalf("want a long random key, got %q", key.Key)
	}

	// The key opens a page, by header and by query parameter (an <img>
	// can only do the latter).
	if resp := requestWithoutRedirects(t, srv.URL+"/music", key.Key); resp.StatusCode != http.StatusOK {
		t.Errorf("want the key to open the page, got %d", resp.StatusCode)
	}
	if resp := requestWithoutRedirects(t, srv.URL+"/music?apikey="+key.Key, ""); resp.StatusCode != http.StatusOK {
		t.Errorf("want ?apikey= to open the page, got %d", resp.StatusCode)
	}
	// And the API.
	if resp := requestWithoutRedirects(t, srv.URL+"/api/v3/system/status", key.Key); resp.StatusCode != http.StatusOK {
		t.Errorf("want the key to work on the API too, got %d", resp.StatusCode)
	}

	// A wrong key is no better than none.
	if resp := requestWithoutRedirects(t, srv.URL+"/music", key.Key+"x"); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("want a wrong key refused, got %d", resp.StatusCode)
	}

	// Using it is recorded, so a forgotten key is visible in Settings.
	keys, err := store.ListAccessKeys(ctx, db)
	if err != nil || len(keys) != 1 {
		t.Fatalf("list keys: %v %+v", err, keys)
	}
	if keys[0].LastUsed == nil {
		t.Error("want the key's last use recorded")
	}

	// Revoking stops it immediately.
	if err := store.DeleteAccessKey(ctx, db, key.ID); err != nil {
		t.Fatal(err)
	}
	if resp := requestWithoutRedirects(t, srv.URL+"/music", key.Key); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("want a revoked key refused, got %d", resp.StatusCode)
	}
}

func TestAccessKeys_SettingsPanel(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)

	resp, body := postForm(t, srv, "/settings/access-keys", url.Values{"name": {"Claude"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create = %d: %s", resp.StatusCode, body)
	}
	keys, err := store.ListAccessKeys(t.Context(), db)
	if err != nil || len(keys) != 1 || keys[0].Name != "Claude" {
		t.Fatalf("want the named key created, got %+v (%v)", keys, err)
	}

	// Settings shows it, with the key itself - the point is to hand it over.
	_, body = get(t, srv, "/settings/general")
	if !strings.Contains(body, "Claude") || !strings.Contains(body, keys[0].Key) {
		t.Errorf("want the key listed on the settings page")
	}
	if !strings.Contains(body, "Revoke") {
		t.Errorf("want a revoke button")
	}

	// A key with no name is refused rather than created unlabelled.
	if _, body := postForm(t, srv, "/settings/access-keys", url.Values{"name": {"  "}}); !strings.Contains(body, "give the key a name") {
		t.Errorf("want a nameless key refused, got: %s", body)
	}
}

// requestWithoutRedirects sends one request with an optional key header and
// returns the response as it stands, so a redirect to the login is visible
// rather than followed.
func requestWithoutRedirects(t *testing.T, url, key string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("request %s: %v", url, err)
	}
	if key != "" {
		req.Header.Set("X-Api-Key", key)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}
