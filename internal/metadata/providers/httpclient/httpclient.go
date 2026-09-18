// Package httpclient is a small shared HTTP helper embedded by every
// metadata provider client (tmdb, tvmaze, musicbrainz, omdb). It centralizes
// rate limiting, retry-on-429, and User-Agent handling so each provider
// package only has to supply its base URL, auth headers, and a limiter
// tuned to its own rate limit - see internal/metadata/providers/*/client.go.
package httpclient

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/time/rate"
)

// Client performs rate-limited HTTP GETs with retry-on-429. HTTP and
// BaseURL are both overridable so tests can point at an httptest.Server
// with no live network calls.
type Client struct {
	HTTP       *http.Client
	BaseURL    string
	UserAgent  string
	Limiter    *rate.Limiter
	MaxRetries int
	// MinRetryWait is the shortest a 429 is worth waiting out. The default
	// backoff is sub-second, which suits a server shedding a burst and is
	// useless against an API with a per-minute quota: every quick retry
	// lands in the same exhausted window and burns an attempt. A provider
	// with a quota sets this to something on the order of its window.
	MinRetryWait time.Duration
}

// StatusError is returned for any non-2xx response, so callers can
// distinguish e.g. a 404 (not found) from a transport failure.
type StatusError struct {
	StatusCode int
	Body       string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("http %d: %s", e.StatusCode, e.Body)
}

// Get issues a rate-limited GET to BaseURL+path with the given query
// params and extra headers (in addition to User-Agent, which is always
// set). Retries on 429 up to MaxRetries times, honoring Retry-After when
// present and falling back to exponential backoff with jitter otherwise.
func (c *Client) Get(ctx context.Context, path string, query url.Values, headers http.Header) ([]byte, error) {
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	reqURL := c.BaseURL + path
	if len(query) > 0 {
		reqURL += "?" + query.Encode()
	}

	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if c.Limiter != nil {
			if err := c.Limiter.Wait(ctx); err != nil {
				return nil, fmt.Errorf("rate limiter: %w", err)
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		for k, vs := range headers {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		if c.UserAgent != "" {
			req.Header.Set("User-Agent", c.UserAgent)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("do request: %w", err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read response body: %w", readErr)
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			// 503 observed live from MusicBrainz under normal load
			// ("currently busy, please try again later") - a transient
			// overload signal worth retrying the same as 429, not just a
			// hard-limit response.
			lastErr = &StatusError{StatusCode: resp.StatusCode, Body: string(body)}
			if attempt == c.MaxRetries {
				break
			}
			if err := sleepBackoff(ctx, resp.Header.Get("Retry-After"), attempt, c.MinRetryWait); err != nil {
				return nil, err
			}
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, &StatusError{StatusCode: resp.StatusCode, Body: string(body)}
		}

		return body, nil
	}

	return nil, lastErr
}

// retryWait is how long to hold off before retrying, kept apart from the
// sleeping so it can be checked without one.
func retryWait(retryAfter string, attempt int, minWait time.Duration) time.Duration {
	wait := backoffDuration(attempt)
	if minWait > 0 {
		// Each attempt waits a little longer, so a quota that resets on a
		// rolling window is crossed rather than repeatedly missed.
		if stepped := time.Duration(attempt+1) * minWait; stepped > wait {
			wait = stepped
		}
	}
	if retryAfter != "" {
		if secs, err := strconv.Atoi(retryAfter); err == nil {
			wait = time.Duration(secs) * time.Second
		}
	}
	return wait
}

func sleepBackoff(ctx context.Context, retryAfter string, attempt int, minWait time.Duration) error {
	timer := time.NewTimer(retryWait(retryAfter, attempt, minWait))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func backoffDuration(attempt int) time.Duration {
	base := time.Duration(1<<attempt) * 200 * time.Millisecond
	jitter := time.Duration(rand.Int63n(int64(base) / 2))
	return base + jitter
}
