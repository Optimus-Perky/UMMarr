package api_test

import (
	"database/sql"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/auth"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

const testAuthUsername = "admin"
const testAuthPassword = "correct-horse-battery-staple"

func newAuthTestServer(t *testing.T) (*httptest.Server, *sql.DB) {
	t.Helper()
	db := openTestDB(t)
	cipher, err := auth.NewSessionCipher(make([]byte, auth.KeySize)) // fixed all-zero key: fine for a test, never for real deployment
	if err != nil {
		t.Fatalf("new session cipher: %v", err)
	}
	srv := httptest.NewServer(api.NewRouter(api.Deps{
		DB: db, AuthUsername: testAuthUsername, AuthPassword: testAuthPassword, SessionCipher: cipher,
	}))
	t.Cleanup(srv.Close)
	return srv, db
}

func TestRequireAuth_RedirectsUnauthenticatedBrowserRequest(t *testing.T) {
	srv, _ := newAuthTestServer(t)

	client := &http.Client{} // default: follows redirects
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	if !strings.HasSuffix(resp.Request.URL.Path, "/login") {
		t.Fatalf("want redirected to /login, ended up at %s", resp.Request.URL.Path)
	}
}

func TestRequireAuth_HTMXRequestGetsHXRedirectHeader(t *testing.T) {
	srv, _ := newAuthTestServer(t)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/activity/queue", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("HX-Request", "true")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /activity/queue: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 (htmx expects a 200 + HX-Redirect, not a real redirect), got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("HX-Redirect"); got != "/login" {
		t.Fatalf("want HX-Redirect: /login, got %q", got)
	}
}

func TestLogin_WrongPasswordShowsError(t *testing.T) {
	srv, _ := newAuthTestServer(t)

	resp, err := http.PostForm(srv.URL+"/login", map[string][]string{"username": {testAuthUsername}, "password": {"wrong"}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 (re-rendered login page), got %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "ummarr_session" {
			t.Fatalf("want no session cookie set on a failed login")
		}
	}
}

func TestLogin_WrongUsernameShowsError(t *testing.T) {
	srv, _ := newAuthTestServer(t)

	resp, err := http.PostForm(srv.URL+"/login", map[string][]string{"username": {"not-" + testAuthUsername}, "password": {testAuthPassword}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 (re-rendered login page), got %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "ummarr_session" {
			t.Fatalf("want no session cookie set for a wrong username, even with the right password")
		}
	}
}

func TestLogin_CorrectPasswordGrantsAccess(t *testing.T) {
	srv, _ := newAuthTestServer(t)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar}

	resp, err := client.PostForm(srv.URL+"/login", map[string][]string{"username": {testAuthUsername}, "password": {testAuthPassword}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.HasSuffix(resp.Request.URL.Path, "/") {
		t.Fatalf("want redirected to / after login, ended up at %s", resp.Request.URL.Path)
	}

	resp2, err := client.Get(srv.URL + "/activity")
	if err != nil {
		t.Fatalf("GET /activity: %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("want 200 for an authenticated request, got %d", resp2.StatusCode)
	}
}

// TestAccountSettings_ChangeTakesEffectWithoutRestart proves the
// no-restart claim for account changes: after saving a new username/
// password directly via the store (same as Settings -> Account would),
// the OLD bootstrap credentials stop working and the NEW ones succeed,
// on the SAME running server instance - no server restart in between.
func TestAccountSettings_ChangeTakesEffectWithoutRestart(t *testing.T) {
	srv, db := newAuthTestServer(t)
	ctx := t.Context()

	newHash, err := bcrypt.GenerateFromPassword([]byte("new-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate password hash: %v", err)
	}
	if err := store.UpdateAuthUsername(ctx, db, "newuser"); err != nil {
		t.Fatalf("update auth username: %v", err)
	}
	if err := store.UpdateAuthPasswordHash(ctx, db, string(newHash)); err != nil {
		t.Fatalf("update auth password hash: %v", err)
	}

	// Old bootstrap credentials must no longer work.
	oldResp, err := http.PostForm(srv.URL+"/login", map[string][]string{"username": {testAuthUsername}, "password": {testAuthPassword}})
	if err != nil {
		t.Fatalf("login with old credentials: %v", err)
	}
	for _, c := range oldResp.Cookies() {
		if c.Name == "ummarr_session" {
			t.Fatalf("want the old bootstrap credentials to be rejected once DB credentials are saved")
		}
	}

	// New credentials must work immediately.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar}
	newResp, err := client.PostForm(srv.URL+"/login", map[string][]string{"username": {"newuser"}, "password": {"new-password"}})
	if err != nil {
		t.Fatalf("login with new credentials: %v", err)
	}
	if !strings.HasSuffix(newResp.Request.URL.Path, "/") {
		t.Fatalf("want redirected to / after login with new credentials, ended up at %s", newResp.Request.URL.Path)
	}
}

func TestLogout_ClearsSessionAndRequiresLoginAgain(t *testing.T) {
	srv, _ := newAuthTestServer(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar}
	if _, err := client.PostForm(srv.URL+"/login", map[string][]string{"username": {testAuthUsername}, "password": {testAuthPassword}}); err != nil {
		t.Fatalf("login: %v", err)
	}

	if _, err := client.Post(srv.URL+"/logout", "", nil); err != nil {
		t.Fatalf("logout: %v", err)
	}

	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET / after logout: %v", err)
	}
	if !strings.HasSuffix(resp.Request.URL.Path, "/login") {
		t.Fatalf("want redirected to /login after logout, ended up at %s", resp.Request.URL.Path)
	}
}

func TestDownloadCompleted_RequiresTokenWhenConfigured(t *testing.T) {
	db := openTestDB(t)
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, WebhookToken: "secret-token"}))
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/downloads/deadbeef/completed", "", nil)
	if err != nil {
		t.Fatalf("webhook without token: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without the token, got %d", resp.StatusCode)
	}

	resp2, err := http.Post(srv.URL+"/downloads/deadbeef/completed?token=wrong", "", nil)
	if err != nil {
		t.Fatalf("webhook with wrong token: %v", err)
	}
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 with the wrong token, got %d", resp2.StatusCode)
	}

	// Right token, unknown hash - should get past the token check and fail
	// at the "no grab found" step instead (404, not 401).
	resp3, err := http.Post(srv.URL+"/downloads/deadbeef/completed?token=secret-token", "", nil)
	if err != nil {
		t.Fatalf("webhook with correct token: %v", err)
	}
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 (correct token, unknown grab) got %d", resp3.StatusCode)
	}
}
