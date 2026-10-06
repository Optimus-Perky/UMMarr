package logging

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Transport logs every request made through it at Diagnostic: method, URL
// with secrets blanked out, status and how long it took. Wrapped around
// http.DefaultTransport at startup, it covers the metadata providers,
// indexers, download clients and notifications alike.
type Transport struct {
	Base http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if !Enabled(Diagnostic) {
		return base.RoundTrip(r)
	}
	started := time.Now()
	resp, err := base.RoundTrip(r)
	elapsed := time.Since(started).Round(time.Millisecond)
	if err != nil {
		Tracef("http %s %s failed after %s: %v", r.Method, RedactURL(r.URL), elapsed, err)
		return resp, err
	}
	Tracef("http %s %s -> %d in %s", r.Method, RedactURL(r.URL), resp.StatusCode, elapsed)
	return resp, nil
}

// secretParams are query parameters whose values never reach the log.
var secretParams = map[string]bool{
	"apikey": true, "api_key": true, "token": true, "password": true, "pass": true,
	"passkey": true, "key": true, "x-plex-token": true, "secret": true, "auth": true,
}

// RedactURL is u with secret query values and any user:password replaced.
func RedactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	clean := *u
	if clean.User != nil {
		clean.User = url.User("xxx")
	}
	if clean.RawQuery != "" {
		q := clean.Query()
		for name := range q {
			if secretParams[strings.ToLower(name)] {
				q.Set(name, "xxx")
			}
		}
		clean.RawQuery = q.Encode()
	}
	return clean.String()
}
