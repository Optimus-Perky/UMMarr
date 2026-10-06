package api_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A form another site submits through the browser is refused, while the
// same request from a non-browser client (no Sec-Fetch-Site, no Origin) or
// from UMMarr's own pages goes through.
func TestCrossOriginProtection_RefusesCrossSiteBrowserPost(t *testing.T) {
	srv, _ := newAuthTestServer(t)
	post := func(headers map[string]string) int {
		t.Helper()
		form := url.Values{"username": {"nobody"}, "password": {"wrong"}}
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/login", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /login: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if got := post(map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}); got != http.StatusForbidden {
		t.Errorf("cross-site browser POST: want 403, got %d", got)
	}
	if got := post(map[string]string{"Origin": "https://evil.example"}); got != http.StatusForbidden {
		t.Errorf("cross-origin POST from an older browser: want 403, got %d", got)
	}
	if got := post(map[string]string{"Sec-Fetch-Site": "same-origin"}); got != http.StatusOK {
		t.Errorf("same-origin POST: want 200, got %d", got)
	}
	if got := post(nil); got != http.StatusOK {
		t.Errorf("non-browser POST: want 200, got %d", got)
	}
}

func TestSecureHeaders_OnEveryResponse(t *testing.T) {
	srv, _ := newAuthTestServer(t)
	resp, err := http.Get(srv.URL + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	resp.Body.Close()
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "SAMEORIGIN",
		"Referrer-Policy":        "same-origin",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s: want %q, got %q", header, want, got)
		}
	}
}

// Five wrong passwords are a typo; the sixth locks the address out, and
// even the right password is turned away until the lockout ends.
func TestLogin_RepeatedFailuresLockTheAddressOut(t *testing.T) {
	srv, _ := newAuthTestServer(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	attempt := func(password string) int {
		t.Helper()
		resp, err := client.PostForm(srv.URL+"/login", url.Values{"username": {testAuthUsername}, "password": {password}})
		if err != nil {
			t.Fatalf("POST /login: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for i := 0; i < 6; i++ {
		if got := attempt("wrong"); got != http.StatusOK {
			t.Fatalf("attempt %d: want the login page again (200), got %d", i+1, got)
		}
	}
	if got := attempt(testAuthPassword); got != http.StatusTooManyRequests {
		t.Fatalf("right password while locked out: want 429, got %d", got)
	}
}

func TestLogin_SessionCookieIsSecureBehindHTTPSProxy(t *testing.T) {
	srv, _ := newAuthTestServer(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	form := url.Values{"username": {testAuthUsername}, "password": {testAuthPassword}}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	resp.Body.Close()
	cookies := resp.Cookies()
	if len(cookies) == 0 || !cookies[0].Secure {
		t.Fatalf("want a Secure session cookie, got %+v", cookies)
	}
}
