package httpclient

import (
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
