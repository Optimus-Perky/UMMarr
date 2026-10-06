package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// An API with a per-minute quota needs its 429 waited out, not retried
// three times inside the same second: the default backoff is a fraction
// of a second, which lands every attempt in the same exhausted window.
func TestMinRetryWaitOutlastsTheDefaultBackoff(t *testing.T) {
	long := retryWait("", 0, 20*time.Second)
	if long < 20*time.Second {
		t.Errorf("first wait %v, want at least the 20s floor", long)
	}
	if second := retryWait("", 1, 20*time.Second); second <= long {
		t.Errorf("second wait %v is not longer than the first %v", second, long)
	}
	// Without a floor, the old behaviour is untouched.
	if quick := retryWait("", 0, 0); quick > time.Second {
		t.Errorf("default backoff grew to %v", quick)
	}
	// Retry-After still wins: the server knows better than the floor.
	if told := retryWait("2", 0, 20*time.Second); told != 2*time.Second {
		t.Errorf("Retry-After ignored, waited %v", told)
	}
}

// A Retry-After of an hour (or a date far ahead) would park whatever task
// asked; it is capped, and the HTTP-date form is understood.
func TestRetryAfterIsCappedAndAcceptsADate(t *testing.T) {
	if wait := retryWait("3600", 0, 0); wait != maxRetryAfter {
		t.Errorf("Retry-After 3600: waited %v, want the %v cap", wait, maxRetryAfter)
	}
	soon := time.Now().Add(3 * time.Second).UTC().Format(http.TimeFormat)
	if wait := retryWait(soon, 0, 0); wait <= 0 || wait > 3*time.Second {
		t.Errorf("Retry-After as a date 3s ahead: waited %v", wait)
	}
}

// A 502 from a gateway is retried like a 503, and a connection the server
// drops is retried rather than failing the whole refresh.
func TestGetRetriesGatewayErrorsAndDroppedConnections(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.WriteHeader(http.StatusBadGateway)
		case 2:
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			conn.Close()
		default:
			w.Write([]byte("ok"))
		}
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, MaxRetries: 3}
	body, err := c.Get(context.Background(), "/", nil, nil)
	if err != nil || string(body) != "ok" {
		t.Fatalf("want ok after retries, got %q, %v", body, err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("want 3 calls, got %d", got)
	}
}

func TestDefaultHTTPHasATimeout(t *testing.T) {
	if DefaultHTTP.Timeout <= 0 {
		t.Fatal("the default client must time out")
	}
}
