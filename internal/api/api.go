// Package api is the web UI + HTTP layer for UMMarr: server-rendered
// html/template pages with htmx for interactivity, deliberately not a JS
// SPA - see the project plan for why (single-static-binary, no npm/CVE
// surface). Each media type has a library-grid list page plus a per-item
// detail page (movie_detail.html/series_detail.html/album_detail.html);
// editing beyond monitored/naming/settings and cast/media-info/quality
// parsing remain out of scope - see the detail-pages plan for the exact
// boundary.
package api

import (
	"database/sql"
	"embed"
	"html"
	"html/template"
	"net/http"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/auth"
	"github.com/Optimus-Perky/UMMarr/internal/backup"
	"github.com/Optimus-Perky/UMMarr/internal/health"
	"github.com/Optimus-Perky/UMMarr/internal/importlist"
	"github.com/Optimus-Perky/UMMarr/internal/logbuf"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/musicbrainz"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/omdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tvmaze"
	"github.com/Optimus-Perky/UMMarr/internal/nfo"
	"github.com/Optimus-Perky/UMMarr/internal/notify"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
	"github.com/Optimus-Perky/UMMarr/internal/tasks"
	"github.com/Optimus-Perky/UMMarr/internal/updates"
)

//go:embed templates/*.html templates/partials/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Deps bundles everything the handlers need. Provider clients are held
// directly (not just via the sync.*Service structs) because search-only
// endpoints (e.g. GET /movies/search) query a provider without going
// through the sync/persist path at all.
type Deps struct {
	DB *sql.DB

	TMDB        *tmdb.Client
	OMDb        *omdb.Client // nilable - fine without a key, matches sync.MovieService
	TVMaze      *tvmaze.Client
	MusicBrainz *musicbrainz.Client

	Movies   *sync.MovieService
	Series   *sync.SeriesService
	Music    *sync.MusicService
	Indexer  *sync.IndexerService
	Download *sync.DownloadService
	// Search grabs on its own: automatic search, Search all missing and RSS
	// sync. Nil disables those buttons.
	Search *sync.SearchService
	// Import is the same *sync.ImportService instance DownloadService uses
	// internally - exposed at the top level too for the Settings page's
	// "Scan for existing files" button (internal/api/settings.go), which
	// has no grab to go through DownloadService for.
	Import *sync.ImportService
	// Events records what the UI does to the library; nil records nothing.
	Events *sync.Events
	// Logs is what the process has logged lately, for System → Logs.
	Logs *logbuf.Buffer
	// Notifier sends test messages from Settings → Connect; nil disables Test.
	Notifier *notify.Service
	// Metadata writes .nfo files and images after adds from the UI; nil skips.
	Metadata *nfo.Writer
	// ImportLists previews lists from Settings; nil disables Test.
	ImportLists *importlist.Service
	// MediaInfo analyzes files for codecs and audio tracks; nil disables it.
	MediaInfo *sync.MediaAnalyzer

	// The System page.
	Tasks     *tasks.Scheduler
	Health    *health.Checker
	Backups   *backup.Service
	Updates   *updates.Checker
	DBPath    string
	StartedAt time.Time
	// Restart exits the process so the container's restart policy brings it
	// back - how a staged restore is applied.
	Restart func()
	// URLBase is the configured URL base, reported by the API.
	URLBase string

	// AuthUsername/AuthPassword and SessionCipher together gate the whole
	// web UI behind a login page (see auth.go) - the bootstrap/env-var
	// fallback credentials, checked only when no username/password has
	// been saved via Settings -> Account (internal/store.AppSettings)
	// takes priority once one has. SessionCipher nil disables auth
	// entirely, the default for local `go run`/tests.
	AuthUsername  string
	AuthPassword  string
	SessionCipher *auth.SessionCipher // nilable
	// WebhookToken, if set, is required as a ?token= query param on
	// POST /downloads/{hash}/completed - independent of the session-cookie
	// auth above, see webhooks.go.
	WebhookToken string

	// BootstrapDelugeBaseURL/BootstrapDelugePasswordSet
	// are the .env-var bootstrap values (cfg.DelugeBaseURL
	// etc.) - used only by the Settings page, to prefill the base URL
	// field and to compute "is a key/password configured at all" (DB
	// override OR bootstrap) without ever needing to read back an actual
	// secret value into rendered HTML. See settings.go.
	BootstrapDelugeBaseURL     string
	BootstrapDelugePasswordSet bool
}

// handler holds the parsed templates plus Deps - embedded by each
// media-type's handler struct so every file only needs `h.pages`/`h.deps`.
//
// pages holds one *template.Template PER PAGE, not one shared set: every
// page's content file uses {{define "content"}} for readability, but
// html/template's define isn't file-scoped - it's one shared name across
// everything parsed together, so parsing all page files into a single
// template.Template makes each one clobber the last (a real bug hit
// while building this: every page rendered as whichever file glob
// happened to parse last). Cloning "base" once per page and adding only
// that page's own content file keeps each page's "content" definition
// isolated.
type handler struct {
	deps      Deps
	pages     map[string]*template.Template
	partials  *template.Template
	loginPage *template.Template
}

var pageFiles = map[string]string{
	"home":                 "templates/home.html",
	"movies":               "templates/movies.html",
	"movie_detail":         "templates/movie_detail.html",
	"tv":                   "templates/tv.html",
	"series_detail":        "templates/series_detail.html",
	"music":                "templates/music.html",
	"album_detail":         "templates/album_detail.html",
	"artist_detail":        "templates/artist_detail.html",
	"activity":             "templates/activity.html",
	"history":              "templates/history.html",
	"season_pass":          "templates/season_pass.html",
	"scan_report":          "templates/scan_report.html",
	"calendar":             "templates/calendar.html",
	"system":               "templates/system.html",
	"api_docs":             "templates/api_docs.html",
	"settings":             "templates/settings.html",
	"quality_profile_edit": "templates/quality_profile_edit.html",
}

// base.html + every partial parse together first - partials each have
// distinct define names (movie_results, monitored_chip, ...), so unlike
// page content files (which all define "content" and would clobber each
// other, see handler's doc comment) there's no collision folding them
// all into one shared template set. This lets any page template invoke
// a partial directly via {{template "monitored_chip" ...}} instead of
// duplicating its markup per page.
func parsePages() map[string]*template.Template {
	base := template.Must(template.ParseFS(templatesFS, "templates/base.html", "templates/partials/*.html"))
	pages := make(map[string]*template.Template, len(pageFiles))
	for name, file := range pageFiles {
		pages[name] = template.Must(template.Must(base.Clone()).ParseFS(templatesFS, file))
	}
	return pages
}

func parsePartials() *template.Template {
	return template.Must(template.ParseFS(templatesFS, "templates/partials/*.html"))
}

// parseLoginPage parses login.html standalone, NOT cloned from "base" like
// every other page - the login page deliberately has no sidebar nav (no
// point revealing/linking to routes the visitor isn't authenticated for
// yet), so it's a complete, independent <html> document.
func parseLoginPage() *template.Template {
	return template.Must(template.ParseFS(templatesFS, "templates/login.html"))
}

// renderPage executes the named page's base layout, writing directly to w.
func (h *handler) renderPage(w http.ResponseWriter, page string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.pages[page].ExecuteTemplate(w, "base", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// renderLoginPage executes the standalone login template.
func (h *handler) renderLoginPage(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.loginPage.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// renderPartial executes a single named partial (an htmx-swap fragment),
// with no base-layout wrapping. Partials each have distinct names
// (movie_results, series_results, ...) so, unlike pages, they can safely
// share one parsed template set.
func (h *handler) renderPartial(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.partials.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// renderAddError writes a small inline error fragment in place of a
// search-result row's Add form (hx-target="this" hx-swap="outerHTML"
// replaces the form with this) - used by every Add handler on failure,
// instead of the HX-Redirect success path.
func renderAddError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	w.Write([]byte(`<p class="notice">Couldn't add: ` + html.EscapeString(err.Error()) + `</p>`))
}
