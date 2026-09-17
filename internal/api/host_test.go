package api_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// TestHostSettings: the Host panel saves URL base, SSL and proxy settings,
// normalising the base and refusing a half-configured SSL.
func TestHostSettings(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	_, body := get(t, srv, "/settings/general")
	if !strings.Contains(body, `name="url_base"`) || !strings.Contains(body, `name="proxy_bypass" value="localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"`) {
		t.Fatalf("want the Host panel with the default bypass list, got:\n%s", body)
	}
	form := url.Values{"url_base": {"ummarr/"}, "ssl_port": {"9898"}, "proxy_enabled": {"on"}, "proxy_url": {"http://proxy:3128"}, "proxy_bypass": {"localhost"}}
	_, body = postForm(t, srv, "/settings/host", form)
	if !strings.Contains(body, "Saved") {
		t.Fatalf("want saved, got:\n%s", body)
	}
	s, _ := store.GetAppSettings(t.Context(), db)
	if s.Host.URLBase != "/ummarr" || !s.Host.ProxyEnabled || s.Host.ProxyURL != "http://proxy:3128" || s.Host.SSLPort != 9898 {
		t.Fatalf("want the host settings saved, got %+v", s.Host)
	}
	form.Set("ssl_enabled", "on")
	_, body = postForm(t, srv, "/settings/host", form)
	if !strings.Contains(body, "certificate and a key") {
		t.Fatalf("want SSL without files refused, got:\n%s", body)
	}
}
