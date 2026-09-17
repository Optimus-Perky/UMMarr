// Package config loads settings the metadata providers need - principally
// API keys the user must obtain themselves. Nothing here ever requests,
// prompts for, or hardcodes a credential; it only reads what's already in
// the environment.
package config

import (
	"encoding/base64"
	"os"
	"time"
)

// Config holds settings for the metadata-provider layer and the web
// server. TMDBToken and OMDbAPIKey are each obtained by the user directly
// from the provider and set as environment variables - TVMaze and
// MusicBrainz need no key at all.
type Config struct {
	TMDBToken  string
	OMDbAPIKey string
	UserAgent  string
	ListenAddr string

	ProwlarrBaseURL string
	ProwlarrAPIKey  string
	DelugeBaseURL   string
	DelugePassword  string

	// DownloadPollGracePeriod is how long a grab can go without "reporting
	// in" (via the /downloads/{hash}/completed webhook or a previous
	// fallback poll) before DownloadService's fallback poller starts
	// actively asking Deluge about it again - see internal/sync/download.go.
	DownloadPollGracePeriod time.Duration

	// AuthUsername is the bootstrap login username, defaulting to "admin"
	// when UMMARR_AUTH_USERNAME is unset - only used when no username/
	// password has been saved via Settings -> Account (see
	// internal/store.AppSettings), which takes priority once set.
	AuthUsername string
	// AuthPassword, if set, enables the web UI login page - a request
	// with no valid session cookie is redirected there. Empty disables
	// auth entirely (the default for local `go run`/tests, matching every
	// other optional-feature-off-by-default convention in this codebase).
	AuthPassword string
	// SessionKey is the 32-byte AES-256 key session cookies are encrypted
	// with (see internal/auth). Nil when AuthPassword is empty or the env
	// var is missing/malformed - cmd/ummarr generates an ephemeral one at
	// startup in that case, logging a warning that sessions won't survive
	// a restart.
	SessionKey []byte
	// WebhookToken, if set, is a shared secret the /downloads/{hash}/completed
	// webhook requires as a ?token= query param - independent of the
	// session-cookie auth above, since a download client's "on complete"
	// script can't hold a browser session. Empty leaves the webhook open,
	// matching its behavior before auth existed.
	WebhookToken string
}

const (
	defaultUserAgent               = "UMMarr/0.1 (+github.com/Optimus-Perky/UMMarr)"
	defaultListenAddr              = ":8080"
	defaultDownloadPollGracePeriod = 5 * time.Minute
)

// Load reads UMMARR_TMDB_TOKEN, UMMARR_OMDB_API_KEY, UMMARR_USER_AGENT,
// UMMARR_LISTEN_ADDR, UMMARR_PROWLARR_BASE_URL, UMMARR_PROWLARR_API_KEY,
// UMMARR_DELUGE_BASE_URL, UMMARR_DELUGE_PASSWORD,
// UMMARR_DOWNLOAD_POLL_GRACE_PERIOD, UMMARR_AUTH_USERNAME, UMMARR_AUTH_PASSWORD,
// UMMARR_SESSION_KEY, and UMMARR_WEBHOOK_TOKEN from the environment.
// Missing provider keys are left empty - each provider client reports
// ErrMissingCredential only when actually invoked without its key, rather
// than failing at load time.
func Load() Config {
	userAgent := os.Getenv("UMMARR_USER_AGENT")
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	listenAddr := os.Getenv("UMMARR_LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}
	pollGracePeriod := defaultDownloadPollGracePeriod
	if s := os.Getenv("UMMARR_DOWNLOAD_POLL_GRACE_PERIOD"); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			pollGracePeriod = d
		}
	}

	authUsername := os.Getenv("UMMARR_AUTH_USERNAME")
	if authUsername == "" {
		authUsername = "admin"
	}

	var sessionKey []byte
	if s := os.Getenv("UMMARR_SESSION_KEY"); s != "" {
		if key, err := base64.StdEncoding.DecodeString(s); err == nil && len(key) == 32 {
			sessionKey = key
		}
	}

	return Config{
		TMDBToken:  os.Getenv("UMMARR_TMDB_TOKEN"),
		OMDbAPIKey: os.Getenv("UMMARR_OMDB_API_KEY"),
		UserAgent:  userAgent,
		ListenAddr: listenAddr,

		ProwlarrBaseURL: os.Getenv("UMMARR_PROWLARR_BASE_URL"),
		ProwlarrAPIKey:  os.Getenv("UMMARR_PROWLARR_API_KEY"),
		DelugeBaseURL:   os.Getenv("UMMARR_DELUGE_BASE_URL"),
		DelugePassword:  os.Getenv("UMMARR_DELUGE_PASSWORD"),

		DownloadPollGracePeriod: pollGracePeriod,

		AuthUsername: authUsername,
		AuthPassword: os.Getenv("UMMARR_AUTH_PASSWORD"),
		SessionKey:   sessionKey,
		WebhookToken: os.Getenv("UMMARR_WEBHOOK_TOKEN"),
	}
}
