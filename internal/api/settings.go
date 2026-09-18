package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/Optimus-Perky/UMMarr/internal/customformat"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
	"github.com/Optimus-Perky/UMMarr/internal/urlbase"
)

// mediaMountRoot is the container-internal path the media library is
// mounted at (docker-compose.yml maps the host media root to /data - see the
// Dockerfile/compose comments for why). Root folders are always somewhere
// under this mount, so a bare subfolder name typed into Settings (e.g.
// "Movies") gets this prefixed automatically instead of requiring the
// user to know/type the container-internal mount path.
const mediaMountRoot = "/data"

// resolveRootFolderPath turns a Settings -> Root folders form submission
// into a real container path: a bare name (no leading "/") is resolved
// relative to mediaMountRoot, an already-absolute path is used verbatim
// (an escape hatch for anyone who really does want a path outside the
// media mount, or a differently-shaped deployment).
func resolveRootFolderPath(input string) string {
	if strings.HasPrefix(input, "/") {
		return path.Clean(input)
	}
	return path.Join(mediaMountRoot, input)
}

// settingsTab is one tab of the Settings page, in the arr apps' order.
type settingsTab struct{ Slug, Label string }

var settingsTabs = []settingsTab{
	{"media-management", "Media Management"}, {"profiles", "Profiles"}, {"indexers", "Indexers"}, {"download-clients", "Download Clients"}, {"custom-formats", "Custom Formats"},
	{"import-lists", "Import Lists"}, {"connect", "Connect"}, {"metadata", "Metadata"}, {"general", "General"},
}

// settingsTabKnown reports whether slug names a Settings tab.
func settingsTabKnown(slug string) bool {
	for _, tab := range settingsTabs {
		if tab.Slug == slug {
			return true
		}
	}
	return false
}

type settingsPageData struct {
	Tab              string
	Tabs             []settingsTab
	FFprobeAvailable bool
	UnmatchedCount   int
	PlexConnected    bool
	Active           string
	PageTitle        string
	RootFolders      []rootFolderView
	QualityProfiles  []store.QualityProfile
	PreferredWords   []store.PreferredWord
	Indexers         []indexerView
	IndexerSyncHint  bool
	IndexerOptions   store.IndexerSettings
	RSSSyncRunning   bool

	AuthUsername string

	DownloadClients  []downloadClientView
	DownloadHandling store.DownloadHandling
	Notifications    []notificationView
	Host             store.HostSettings
	Metadata         store.MetadataSettings
	MetadataSources  []metadataSourceView
	CustomFormats    []customFormatView
	ImportLists      []importListView
	Exclusions       []store.Exclusion

	MovieNaming  store.NamingConfig
	SeriesNaming store.NamingConfig
	MusicNaming  store.NamingConfig
	Media        store.MediaSettings

	// Errors holds a message per media management field that couldn't be
	// saved; Saved is set after a successful save.
	Errors map[string]string
	Saved  bool
}

// loadSettingsPage gathers everything the Settings page shows, as saved.
func (h *handler) loadSettingsPage(ctx context.Context) (settingsPageData, error) {
	// No media-type filter here (unlike each library page's own picker) -
	// Settings shows every root folder across all three media types in
	// one list, so the user can see the whole setup at a glance.
	var rootFolders []store.RootFolder
	for _, mediaType := range []string{"movie", "series", "music"} {
		folders, err := store.ListRootFolders(ctx, h.deps.DB, mediaType)
		if err != nil {
			return settingsPageData{}, err
		}
		rootFolders = append(rootFolders, folders...)
	}

	qualityProfiles, err := store.ListQualityProfiles(ctx, h.deps.DB)
	if err != nil {
		return settingsPageData{}, err
	}
	preferredWords, err := store.ListPreferredWords(ctx, h.deps.DB)
	if err != nil {
		return settingsPageData{}, err
	}
	appSettings, err := store.GetAppSettings(ctx, h.deps.DB)
	if err != nil {
		return settingsPageData{}, err
	}
	media, err := store.GetMediaSettings(ctx, h.deps.DB)
	if err != nil {
		return settingsPageData{}, err
	}
	naming := map[string]store.NamingConfig{}
	for _, mediaType := range []string{"movie", "series", "music"} {
		c, err := store.GetNamingConfig(ctx, h.deps.DB, mediaType)
		if err != nil {
			return settingsPageData{}, err
		}
		naming[mediaType] = c
	}

	downloadHandling, err := store.GetDownloadHandling(ctx, h.deps.DB)
	if err != nil {
		return settingsPageData{}, err
	}
	indexerOptions, err := store.GetIndexerSettings(ctx, h.deps.DB)
	if err != nil {
		return settingsPageData{}, err
	}
	indexers, syncHint, err := h.indexerViews(ctx)
	if err != nil {
		return settingsPageData{}, err
	}

	authUsername := appSettings.AuthUsername
	if authUsername == "" {
		authUsername = h.deps.AuthUsername
	}
	downloadClients, err := h.downloadClientViews(ctx)
	if err != nil {
		return settingsPageData{}, err
	}
	notifications, err := h.notificationViews(ctx)
	if err != nil {
		return settingsPageData{}, err
	}
	customFormats, err := h.customFormatViews(ctx)
	if err != nil {
		return settingsPageData{}, err
	}
	metadataSources, err := h.metadataSourceViews(ctx)
	if err != nil {
		return settingsPageData{}, err
	}
	metadataSettings, err := store.GetMetadataSettings(ctx, h.deps.DB)
	if err != nil {
		return settingsPageData{}, err
	}
	importLists, err := h.importListViews(ctx)
	if err != nil {
		return settingsPageData{}, err
	}
	exclusions, _ := store.ListExclusions(ctx, h.deps.DB)
	if err != nil {
		return settingsPageData{}, err
	}

	return settingsPageData{
		Active:          "settings",
		PageTitle:       "Settings",
		RootFolders:     rootFolderViews(ctx, h.deps.DB, rootFolders),
		QualityProfiles: qualityProfiles,
		PreferredWords:  preferredWords,
		Indexers:        indexers,
		IndexerSyncHint: syncHint,
		IndexerOptions:  indexerOptions,

		AuthUsername: authUsername,

		DownloadClients:  downloadClients,
		DownloadHandling: downloadHandling,
		Notifications:    notifications,
		Host:             appSettings.Host,
		Metadata:         metadataSettings,
		MetadataSources:  metadataSources,
		CustomFormats:    customFormats,
		ImportLists:      importLists,
		Exclusions:       exclusions,

		MovieNaming:  naming["movie"],
		SeriesNaming: naming["series"],
		MusicNaming:  naming["music"],
		Media:        media,
	}, nil
}

func (h *handler) Settings(w http.ResponseWriter, r *http.Request) {
	tab := r.PathValue("tab")
	if tab == "" {
		tab = settingsTabs[0].Slug
	}
	if !settingsTabKnown(tab) {
		http.NotFound(w, r)
		return
	}
	data, err := h.loadSettingsPage(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.Tab, data.Tabs = tab, settingsTabs
	h.setMediaAnalysisFlags(r.Context(), &data)
	data.UnmatchedCount, _ = store.CountUnmatchedFolders(r.Context(), h.deps.DB)
	data.Saved = r.URL.Query().Get("saved") == "1"
	h.renderPage(w, "settings", data)
}

func (h *handler) CreateRootFolder(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rawPath := r.FormValue("path")
	mediaType := r.FormValue("media_type")
	if rawPath == "" || mediaType == "" {
		http.Error(w, "path and media_type are required", http.StatusBadRequest)
		return
	}

	if _, err := store.CreateRootFolder(r.Context(), h.deps.DB, resolveRootFolderPath(rawPath), mediaType); err != nil {
		renderAddError(w, err)
		return
	}

	w.Header().Set("HX-Redirect", "/settings/media-management")
	w.WriteHeader(http.StatusOK)
}

// CreatePreferredWord adds a Settings -> Preferred Words entry - a term
// whose presence in a release title adds (or, with a negative score,
// subtracts) points from that release's ranking. See internal/decision.
func (h *handler) CreatePreferredWord(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	term := strings.TrimSpace(r.FormValue("term"))
	if term == "" {
		renderAddError(w, errors.New("term is required"))
		return
	}
	score, err := strconv.Atoi(strings.TrimSpace(r.FormValue("score")))
	if err != nil {
		renderAddError(w, errors.New("score must be a whole number"))
		return
	}

	if _, err := store.CreatePreferredWord(r.Context(), h.deps.DB, term, score); err != nil {
		renderAddError(w, err)
		return
	}

	w.Header().Set("HX-Redirect", "/settings/profiles")
	w.WriteHeader(http.StatusOK)
}

// UpdatePreferredWord changes a Settings -> Preferred Words entry's term
// and/or score in place.
func (h *handler) UpdatePreferredWord(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid preferred word id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	term := strings.TrimSpace(r.FormValue("term"))
	if term == "" {
		renderAddError(w, errors.New("term is required"))
		return
	}
	score, err := strconv.Atoi(strings.TrimSpace(r.FormValue("score")))
	if err != nil {
		renderAddError(w, errors.New("score must be a whole number"))
		return
	}

	switch err := store.UpdatePreferredWord(r.Context(), h.deps.DB, id, term, score); {
	case err == nil:
		w.Header().Set("HX-Redirect", "/settings/profiles")
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, store.ErrPreferredWordNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// DeletePreferredWord removes a Settings -> Preferred Words entry.
func (h *handler) DeletePreferredWord(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid preferred word id", http.StatusBadRequest)
		return
	}
	switch err := store.DeletePreferredWord(r.Context(), h.deps.DB, id); {
	case err == nil:
		w.Header().Set("HX-Redirect", "/settings/profiles")
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, store.ErrPreferredWordNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *handler) CreateQualityProfile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	if _, err := store.CreateQualityProfile(r.Context(), h.deps.DB, name); err != nil {
		renderAddError(w, err)
		return
	}

	w.Header().Set("HX-Redirect", "/settings/profiles")
	w.WriteHeader(http.StatusOK)
}

type qualityProfileEditPageData struct {
	Active    string
	PageTitle string
	Profile   store.QualityProfile
	Items     []releaseparse.QualityProfileItem
	Formats   []customformat.Format
	Scores    store.ProfileFormatScores
}

// QualityProfileEdit is the per-profile weight editor (GET
// /settings/quality-profiles/{id}). Auto grab goes by weights set by
// hand, the way Sonarr does it. A plain number
// input per catalog quality rather than a drag-and-drop reorderable list
// (see the plan's scope note - no JS drag-reorder dependency) achieves
// the same "user-specified ordering" outcome: higher weight = more
// preferred. Nothing consumes these weights yet (auto-grab is a
// deliberately separate, later pass) - this just makes them real and
// editable.
func (h *handler) QualityProfileEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	profileID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid quality profile id", http.StatusBadRequest)
		return
	}

	profiles, err := store.ListQualityProfiles(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var profile store.QualityProfile
	found := false
	for _, p := range profiles {
		if p.ID == profileID {
			profile, found = p, true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	formats, err := store.ListCustomFormats(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	scores, err := store.GetProfileFormatScores(ctx, h.deps.DB, profileID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items, err := store.GetQualityProfileItems(ctx, h.deps.DB, profileID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.renderPage(w, "quality_profile_edit", qualityProfileEditPageData{
		Active: "settings", PageTitle: "Quality Profile: " + profile.Name,
		Profile: profile, Items: items, Formats: formats, Scores: scores,
	})
}

// UpdateQualityProfileItems saves the weight editor's form (POST
// /settings/quality-profiles/{id}/items) - one weight/allowed pair per
// releaseparse.AllQualities entry, submitted as parallel
// "weight_<quality>"/"allowed_<quality>" fields since html/template has
// no clean way to submit a slice of structs from a plain form.
func (h *handler) UpdateQualityProfileItems(w http.ResponseWriter, r *http.Request) {
	profileID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid quality profile id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	items := make([]releaseparse.QualityProfileItem, len(releaseparse.AllQualities))
	for i, quality := range releaseparse.AllQualities {
		weight, _ := strconv.Atoi(r.FormValue("weight_" + quality))
		items[i] = releaseparse.QualityProfileItem{
			Quality: quality, Weight: weight, Allowed: r.FormValue("allowed_"+quality) == "on",
		}
	}

	if err := store.UpdateQualityProfileUpgrades(r.Context(), h.deps.DB, profileID, r.FormValue("upgrade_allowed") == "on", r.FormValue("cutoff")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	scores := store.ProfileFormatScores{Scores: map[int64]int{}}
	scores.MinScore, _ = strconv.Atoi(r.FormValue("min_format_score"))
	scores.CutoffScore, _ = strconv.Atoi(r.FormValue("cutoff_format_score"))
	for key, values := range r.Form {
		if id, ok := strings.CutPrefix(key, "format_score_"); ok && len(values) > 0 {
			formatID, err := strconv.ParseInt(id, 10, 64)
			if err != nil {
				continue
			}
			scores.Scores[formatID], _ = strconv.Atoi(values[0])
		}
	}
	if err := store.SaveProfileFormatScores(r.Context(), h.deps.DB, profileID, scores); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := store.UpdateQualityProfileItems(r.Context(), h.deps.DB, profileID, items); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Redirect", "/settings/quality-profiles/"+r.PathValue("id"))
	w.WriteHeader(http.StatusOK)
}

// UpdateAccount saves the admin username, and the password too if a new
// one was entered - an empty password field means "keep the current
// password" (matches Sonarr/Radarr's own Security settings UX). Takes
// effect immediately: Login always checks store.GetAppSettings fresh.
func (h *handler) UpdateAccount(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	username := r.FormValue("username")
	if username == "" {
		http.Error(w, "username is required", http.StatusBadRequest)
		return
	}
	if err := store.UpdateAuthUsername(r.Context(), h.deps.DB, username); err != nil {
		renderAddError(w, err)
		return
	}
	if password := r.FormValue("password"); password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			renderAddError(w, err)
			return
		}
		if err := store.UpdateAuthPasswordHash(r.Context(), h.deps.DB, string(hash)); err != nil {
			renderAddError(w, err)
			return
		}
	}
	w.Header().Set("HX-Redirect", "/settings/general")
	w.WriteHeader(http.StatusOK)
}

// UpdateDownloadClientSettings saves the Deluge connection. A blank password
// keeps the current one, so the masked field never clears it.

type scanResultData struct {
	Scope                    string // what was scanned, for the report
	Imports                  libraryImportView
	Movies, Episodes, Tracks int
	Err                      string
}

// ScanLibrary is the "Scan for existing files" button's handler - runs
// synchronously (see settings.html's own notice about this) and reports
// how many movies/episodes/tracks it found already on disk and imported.

// SetDefaultQualityProfile makes one profile the one the Add forms preselect.
func (h *handler) SetDefaultQualityProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid quality profile id", http.StatusBadRequest)
		return
	}
	if err := store.SetDefaultQualityProfile(r.Context(), h.deps.DB, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/profiles")
	w.WriteHeader(http.StatusOK)
}

// ScanLibrary is the "Scan folder" button's handler - runs synchronously
// (see settings.html's own notice about this) and reports what it imported.
// The form's media_type and root_folder_id narrow the scan; both are
// optional.
func (h *handler) ScanLibrary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	scope := sync.ScanScope{MediaType: r.FormValue("media_type")}
	label := "all library folders"
	switch scope.MediaType {
	case "movie":
		label = "the movie folders"
	case "series":
		label = "the TV folders"
	case "music":
		label = "the music folders"
	case "":
	default:
		http.Error(w, "invalid media_type", http.StatusBadRequest)
		return
	}
	if v := r.FormValue("root_folder_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			http.Error(w, "invalid root_folder_id", http.StatusBadRequest)
			return
		}
		path, err := store.GetRootFolderPath(ctx, h.deps.DB, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		scope.RootFolderID, label = id, path
	}
	res, err := h.deps.Import.ScanLibrary(ctx, scope)
	if err != nil {
		h.renderPartial(w, "scan_result", scanResultData{Scope: label, Err: err.Error()})
		return
	}
	h.renderPartial(w, "scan_result", scanResultData{Scope: label, Movies: res.Movies, Episodes: res.Episodes, Tracks: res.Tracks, Imports: h.startLibraryImports(ctx, scope)})
}

type libraryImportView struct {
	Imports []sync.LibraryImport
	Running bool
}

func (h *handler) libraryImportView() libraryImportView {
	view := libraryImportView{Imports: h.deps.Import.LibraryImports()}
	for _, li := range view.Imports {
		if li.Running {
			view.Running = true
		}
	}
	return view
}

// startLibraryImports begins importing the unmapped folders of every
// library folder the scan covered, and reports every import's progress.
func (h *handler) startLibraryImports(ctx context.Context, scope sync.ScanScope) libraryImportView {
	for _, mediaType := range []string{"movie", "series", "music"} {
		if scope.MediaType != "" && scope.MediaType != mediaType {
			continue
		}
		folders, _ := store.ListRootFolders(ctx, h.deps.DB, mediaType)
		for _, f := range folders {
			if scope.RootFolderID == 0 || scope.RootFolderID == f.ID {
				h.deps.Import.StartLibraryImport(f.ID, f.Path, mediaType)
			}
		}
	}
	return h.libraryImportView()
}

// LibraryImportStatus is polled while a library import runs.
func (h *handler) LibraryImportStatus(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, "library_import_status", h.libraryImportView())
}

// UpdateHostSettings saves Settings → General → Host; it takes effect when
// UMMarr restarts.
func (h *handler) UpdateHostSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	hs := store.HostSettings{
		URLBase: urlbase.Normalize(r.FormValue("url_base")), SSLEnabled: r.FormValue("ssl_enabled") == "on",
		SSLCertPath: strings.TrimSpace(r.FormValue("ssl_cert_path")), SSLKeyPath: strings.TrimSpace(r.FormValue("ssl_key_path")),
		ProxyEnabled: r.FormValue("proxy_enabled") == "on", ProxyURL: strings.TrimSpace(r.FormValue("proxy_url")), ProxyBypass: strings.TrimSpace(r.FormValue("proxy_bypass")),
	}
	port, err := strconv.Atoi(r.FormValue("ssl_port"))
	if err != nil || port < 1 || port > 65535 {
		renderAddError(w, errors.New("SSL port must be 1-65535"))
		return
	}
	hs.SSLPort = port
	if hs.SSLEnabled && (hs.SSLCertPath == "" || hs.SSLKeyPath == "") {
		renderAddError(w, errors.New("SSL needs both a certificate and a key file"))
		return
	}
	if hs.ProxyEnabled {
		if u, err := url.Parse(hs.ProxyURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			renderAddError(w, errors.New("proxy URL must be http://host:port or https://host:port"))
			return
		}
	}
	if err := store.UpdateHostSettings(r.Context(), h.deps.DB, hs); err != nil {
		renderAddError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<p class="indexer-test ok">Saved. Restart UMMarr to apply it (System → Backups → Restore restarts it, or recreate the container).</p>`))
}

// UpdateMetadataSettings saves Settings → Metadata.
func (h *handler) UpdateMetadataSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	on := func(name string) bool { return r.FormValue(name) == "on" }
	s := store.MetadataSettings{Enabled: on("enabled"), Jellyfin: on("jellyfin"), Plex: on("plex"), MovieNFO: on("movie_nfo"), MovieImages: on("movie_images"), SeriesNFO: on("series_nfo"), EpisodeNFO: on("episode_nfo"), SeriesImages: on("series_images"), AlbumNFO: on("album_nfo"), AlbumImages: on("album_images")}
	if err := store.UpdateMetadataSettings(r.Context(), h.deps.DB, s); err != nil {
		renderAddError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<p class="indexer-test ok">Saved. New imports get metadata; System → Tasks → Write metadata does the whole library.</p>`))
}

// DeleteQualityProfile removes a profile nothing is using.
func (h *handler) DeleteQualityProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid quality profile id", http.StatusBadRequest)
		return
	}
	if err := store.DeleteQualityProfile(r.Context(), h.deps.DB, id); err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", "/settings/profiles")
	w.WriteHeader(http.StatusOK)
}
