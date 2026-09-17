// Package api (this file): the login/session layer. Password verification
// checks Settings-saved credentials first (internal/store.AppSettings,
// bcrypt-hashed), falling back to the env-var bootstrap
// (UMMARR_AUTH_USERNAME/UMMARR_AUTH_PASSWORD - the same plaintext-env-var
// pattern already used for Deluge's password and the Prowlarr API key
// elsewhere in this project) when nothing's been saved yet; session
// cookies are AES-256-encrypted tokens from internal/auth, so the server
// needs no session store.
package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

const sessionCookieName = "ummarr_session"
const sessionDuration = 30 * 24 * time.Hour

type loginPageData struct {
	Error bool
}

func (h *handler) Login(w http.ResponseWriter, r *http.Request) {
	h.renderLoginPage(w, loginPageData{})
}

func (h *handler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ok, err := h.checkCredentials(r.Context(), r.FormValue("username"), r.FormValue("password"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		h.renderLoginPage(w, loginPageData{Error: true})
		return
	}

	expiresAt := time.Now().Add(sessionDuration)
	token, err := h.deps.SessionCipher.NewToken(expiresAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Expires: expiresAt,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// checkCredentials checks the submitted username/password against
// DB-stored credentials (set via Settings -> Account) if any have been
// saved, otherwise against the env-var bootstrap
// (deps.AuthUsername/AuthPassword) - so the credentials already deployed
// keep working until/unless a new username/password is saved via the UI.
// Read fresh from the DB on every attempt (not cached in Deps) so a
// password change takes effect immediately, no restart needed.
func (h *handler) checkCredentials(ctx context.Context, username, password string) (bool, error) {
	settings, err := store.GetAppSettings(ctx, h.deps.DB)
	if err != nil {
		return false, err
	}
	if settings.AuthUsername != "" || settings.AuthPasswordHash != "" {
		if subtle.ConstantTimeCompare([]byte(username), []byte(settings.AuthUsername)) != 1 {
			return false, nil
		}
		return bcrypt.CompareHashAndPassword([]byte(settings.AuthPasswordHash), []byte(password)) == nil, nil
	}
	if subtle.ConstantTimeCompare([]byte(username), []byte(h.deps.AuthUsername)) != 1 {
		return false, nil
	}
	return subtle.ConstantTimeCompare([]byte(password), []byte(h.deps.AuthPassword)) == 1, nil
}

func (h *handler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// requireAuth wraps next so every request must carry a valid session
// cookie - a no-op passthrough when auth isn't configured
// (deps.SessionCipher nil), matching this codebase's nilable-optional-
// feature pattern (e.g. a nil Prowlarr client just means that feature's
// off). /login, /static/*, and the download-completed webhook (which
// authenticates separately via its own token, see webhooks.go - a
// download client's "on complete" script can't hold a browser session)
// are exempt.
func (h *handler) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.deps.SessionCipher == nil || isAuthExempt(r) {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(sessionCookieName)
		if err == nil && h.deps.SessionCipher.Verify(cookie.Value) == nil {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", "/login")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}

func isAuthExempt(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/static/") {
		return true
	}
	if r.URL.Path == "/login" {
		return true
	}
	// The Radarr-compatible API checks its own API key (or the session) - see arr_api.go.
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	return strings.HasPrefix(r.URL.Path, "/downloads/") && strings.HasSuffix(r.URL.Path, "/completed")
}
