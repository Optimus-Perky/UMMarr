// Package api (this file): the browser-facing protections every route
// gets, whether or not a login is configured.
//
//   - Cross-origin protection: a page on another site can't make the
//     browser POST to UMMarr. Without it, any web page visited while
//     UMMarr runs on localhost:8080 with no login could submit
//     "delete these movies and their files" as an ordinary form. Go's
//     http.CrossOriginProtection checks Sec-Fetch-Site (every current
//     browser sends it), falling back to comparing Origin with Host. A
//     request carrying neither - curl, Prowlarr, a download client's
//     on-complete script - isn't from a browser and passes, so the API
//     and the webhook work as before.
//   - Response headers: no MIME sniffing, no framing by other sites, and
//     no Referer leaving the site (an ?apikey= in a URL would otherwise
//     travel with it).
//   - Login throttling: repeated wrong passwords from one address lock it
//     out for a while, doubling each time.
package api

import (
	"net"
	"net/http"
	gosync "sync"
	"time"
)

// secureHeaders sets the headers every response should carry.
// SAMEORIGIN rather than DENY: a dashboard such as Organizr served from
// the same origin through a reverse proxy can still embed the pages.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("X-Frame-Options", "SAMEORIGIN")
		header.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// crossOriginProtection rejects state-changing requests a browser makes on
// behalf of another site (see the file comment).
func crossOriginProtection(next http.Handler) http.Handler {
	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
	}))
	return protection.Handler(next)
}

// requestIsHTTPS reports whether the browser reached UMMarr over HTTPS,
// directly or through a reverse proxy that terminated TLS - the session
// cookie is marked Secure when it did.
func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// Login throttling: the first few failures are free (a mistyped password),
// then each further failure locks the address out for twice as long as
// the last, up to loginMaxLockout. A success clears the address.
const (
	loginFreeFailures = 5
	loginBaseLockout  = time.Minute
	loginMaxLockout   = 15 * time.Minute
	loginForgetAfter  = time.Hour
)

type loginThrottle struct {
	mu      gosync.Mutex
	entries map[string]*loginAttempts
	now     func() time.Time
}

type loginAttempts struct {
	failures    int
	lockedUntil time.Time
	lastFailure time.Time
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{entries: map[string]*loginAttempts{}, now: time.Now}
}

// wait is how long the address must still wait before trying again; zero
// means go ahead.
func (t *loginThrottle) wait(addr string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.entries[addr]
	if entry == nil {
		return 0
	}
	if remaining := entry.lockedUntil.Sub(t.now()); remaining > 0 {
		return remaining
	}
	return 0
}

func (t *loginThrottle) failed(addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	// Forget stale entries as we go, so the map can't grow without bound.
	for key, entry := range t.entries {
		if now.Sub(entry.lastFailure) > loginForgetAfter {
			delete(t.entries, key)
		}
	}
	entry := t.entries[addr]
	if entry == nil {
		entry = &loginAttempts{}
		t.entries[addr] = entry
	}
	entry.failures++
	entry.lastFailure = now
	if over := entry.failures - loginFreeFailures; over > 0 {
		lockout := loginBaseLockout << min(over-1, 10)
		entry.lockedUntil = now.Add(min(lockout, loginMaxLockout))
	}
}

func (t *loginThrottle) succeeded(addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, addr)
}

// clientAddress is the address a login attempt is counted against: the
// connection's own IP. Forwarded headers are deliberately ignored - anyone
// can set them, which would make the throttle trivial to dodge.
func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
