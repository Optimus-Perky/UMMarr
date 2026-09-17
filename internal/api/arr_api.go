package api

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/version"
)

// This file is the part of Radarr's v3 API that Prowlarr's app sync uses
// (NzbDrone.Core.Applications.Radarr.RadarrV3Proxy): system status and the
// indexer endpoints. In Prowlarr, UMMarr is added as a Radarr app; ticking
// TV and Audio in its Sync Categories makes each Prowlarr indexer serve all
// three media types here.

// arrAPIVersion is the Radarr version UMMarr reports. Prowlarr requires at
// least 4.0.4 for a v4+ Radarr.
const arrAPIVersion = "5.26.2.10099"

// privateValue is how Radarr returns API keys and passwords.
const privateValue = "********"

var arrStarted = time.Now()

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// arrAPI wraps an API handler: every response carries the version header
// Prowlarr checks, and a request needs UMMarr's API key (X-Api-Key header or
// apikey query, as in Radarr) or a signed-in session.
func (h *handler) arrAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Application-Version", arrAPIVersion)
		if !h.arrAuthorized(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Unauthorized"})
			return
		}
		next(w, r)
	}
}

func (h *handler) arrAuthorized(r *http.Request) bool {
	given := r.Header.Get("X-Api-Key")
	if given == "" {
		given = r.URL.Query().Get("apikey")
	}
	if given != "" {
		if key, err := store.GetAPIKey(r.Context(), h.deps.DB); err == nil && key != "" &&
			subtle.ConstantTimeCompare([]byte(given), []byte(key)) == 1 {
			return true
		}
	}
	if h.deps.SessionCipher != nil {
		if cookie, err := r.Cookie(sessionCookieName); err == nil && h.deps.SessionCipher.Verify(cookie.Value) == nil {
			return true
		}
	}
	return false
}

// ArrSystemStatus is GET /api/v3/system/status.
func (h *handler) ArrSystemStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"appName": "UMMarr", "instanceName": "UMMarr", "version": arrAPIVersion,
		"isDocker": true, "isLinux": true, "osName": "linux", "urlBase": h.deps.URLBase, "authentication": "forms", "build": version.Commit,
		"branch": "main", "startTime": arrStarted.UTC().Format(time.RFC3339),
	})
}

type arrField struct {
	Order                       int    `json:"order"`
	Name                        string `json:"name"`
	Label                       string `json:"label,omitempty"`
	Unit                        string `json:"unit,omitempty"`
	HelpText                    string `json:"helpText,omitempty"`
	Value                       any    `json:"value"`
	Type                        string `json:"type"`
	Advanced                    bool   `json:"advanced"`
	Privacy                     string `json:"privacy"`
	IsFloat                     bool   `json:"isFloat"`
	SelectOptionsProviderAction string `json:"selectOptionsProviderAction,omitempty"`
}

type arrIndexer struct {
	ID                      int64        `json:"id"`
	Name                    string       `json:"name"`
	Fields                  []arrField   `json:"fields"`
	ImplementationName      string       `json:"implementationName"`
	Implementation          string       `json:"implementation"`
	ConfigContract          string       `json:"configContract"`
	InfoLink                string       `json:"infoLink"`
	Tags                    []int        `json:"tags"`
	Presets                 []arrIndexer `json:"presets,omitempty"`
	EnableRss               bool         `json:"enableRss"`
	EnableAutomaticSearch   bool         `json:"enableAutomaticSearch"`
	EnableInteractiveSearch bool         `json:"enableInteractiveSearch"`
	SupportsRss             bool         `json:"supportsRss"`
	SupportsSearch          bool         `json:"supportsSearch"`
	Protocol                string       `json:"protocol"`
	Priority                int          `json:"priority"`
	DownloadClientID        int          `json:"downloadClientId"`
}

// schemaFields are Radarr's NewznabSettings (and, for Torznab, its
// TorznabSettings and SeedCriteriaSettings) fields with their defaults.
func schemaFields(implementation string) []arrField {
	fields := []arrField{
		{Name: "baseUrl", Label: "URL", Type: "textbox", Value: ""},
		{Name: "apiPath", Label: "API Path", Type: "textbox", Advanced: true, Value: "/api"},
		{Name: "apiKey", Label: "API Key", Type: "textbox", Privacy: "apiKey", Value: ""},
		{Name: "categories", Label: "Categories", Type: "select", SelectOptionsProviderAction: "newznabCategories", Value: newznab.SyncCategories[newznab.MediaMovie]},
		{Name: "additionalParameters", Label: "Additional Parameters", Type: "textbox", Advanced: true, Value: nil},
		{Name: "multiLanguages", Label: "Multi Languages", Type: "select", Advanced: true, Value: []int{}},
		{Name: "failDownloads", Label: "Fail Downloads", Type: "select", Advanced: true, Value: []int{}},
		{Name: "removeYear", Label: "Remove Year", Type: "checkbox", Advanced: true, Value: false},
	}
	if implementation == newznab.Torznab {
		fields = append(fields,
			arrField{Name: "minimumSeeders", Label: "Minimum Seeders", Type: "number", Advanced: true, Value: 1},
			arrField{Name: "seedCriteria.seedRatio", Label: "Seed Ratio", Type: "number", IsFloat: true, Value: nil},
			arrField{Name: "seedCriteria.seedTime", Label: "Seed Time", Unit: "minutes", Type: "number", Advanced: true, Value: nil},
			arrField{Name: "rejectBlocklistedTorrentHashesWhileGrabbing", Label: "Reject Blocklisted Torrent Hashes While Grabbing", Type: "checkbox", Advanced: true, Value: false},
			arrField{Name: "requiredFlags", Label: "Required Flags", Type: "select", Advanced: true, Value: []int{}},
		)
	}
	for i := range fields {
		fields[i].Order = i
		if fields[i].Privacy == "" {
			fields[i].Privacy = "normal"
		}
	}
	return fields
}

func nullableInt(n sql.NullInt64) any {
	if !n.Valid {
		return nil
	}
	return n.Int64
}

// indexerResource is ix as Radarr's IndexerResource. The API key comes back
// masked, as Radarr returns it.
func indexerResource(ix store.Indexer) arrIndexer {
	res := arrIndexer{
		ID: ix.ID, Name: ix.Name, ImplementationName: ix.Implementation, Implementation: ix.Implementation,
		ConfigContract: ix.Implementation + "Settings", InfoLink: "https://wiki.servarr.com/radarr/supported#" + strings.ToLower(ix.Implementation),
		Tags: ix.Tags, EnableRss: ix.EnableRSS, EnableAutomaticSearch: ix.EnableAutomaticSearch, EnableInteractiveSearch: ix.EnableInteractiveSearch,
		SupportsRss: true, SupportsSearch: true, Protocol: ix.Protocol(), Priority: ix.Priority, DownloadClientID: ix.DownloadClientID,
	}
	if res.Tags == nil {
		res.Tags = []int{}
	}
	intsOrEmpty := func(ids []int) []int {
		if ids == nil {
			return []int{}
		}
		return ids
	}
	var extra []arrField
	_ = json.Unmarshal(ix.ExtraFields, &extra)
	known := map[string]bool{}
	for _, f := range schemaFields(ix.Implementation) {
		known[f.Name] = true
		switch f.Name {
		case "baseUrl":
			f.Value = ix.BaseURL
		case "apiPath":
			f.Value = ix.APIPath
		case "apiKey":
			f.Value = ""
			if ix.APIKey != "" {
				f.Value = privateValue
			}
		case "categories":
			f.Value = intsOrEmpty(ix.Categories)
		case "additionalParameters":
			f.Value = ix.AdditionalParameters
		case "minimumSeeders":
			f.Value = ix.MinimumSeeders
		case "seedCriteria.seedRatio":
			f.Value = nil
			if ix.SeedRatio.Valid {
				f.Value = ix.SeedRatio.Float64
			}
		case "seedCriteria.seedTime":
			f.Value = nullableInt(ix.SeedTime)
		case "rejectBlocklistedTorrentHashesWhileGrabbing":
			f.Value = ix.RejectBlocklisted
		case "requiredFlags":
			f.Value = intsOrEmpty(ix.RequiredFlags)
		default:
			for _, e := range extra {
				if e.Name == f.Name {
					f.Value = e.Value
				}
			}
		}
		res.Fields = append(res.Fields, f)
	}
	// Sonarr's and Lidarr's names for fields UMMarr keeps.
	for name, value := range map[string]any{
		"animeCategories": intsOrEmpty(ix.AnimeCategories), "seedCriteria.seasonPackSeedTime": nullableInt(ix.SeasonPackSeedTime),
		"seedCriteria.discographySeedTime": nullableInt(ix.DiscographySeedTime),
	} {
		if name == "animeCategories" && len(ix.AnimeCategories) == 0 || name != "animeCategories" && value == nil {
			continue
		}
		known[name] = true
		res.Fields = append(res.Fields, arrField{Order: len(res.Fields), Name: name, Type: "number", Privacy: "normal", Value: value})
	}
	for _, e := range extra {
		if !known[e.Name] {
			e.Order, e.Privacy = len(res.Fields), "normal"
			res.Fields = append(res.Fields, e)
		}
	}
	return res
}

type validationFailure struct {
	PropertyName   string `json:"propertyName"`
	ErrorMessage   string `json:"errorMessage"`
	AttemptedValue any    `json:"attemptedValue,omitempty"`
	Severity       string `json:"severity"`
	IsWarning      bool   `json:"isWarning"`
}

func failure(property, message string, attempted any) validationFailure {
	return validationFailure{PropertyName: property, ErrorMessage: message, AttemptedValue: attempted, Severity: "error"}
}

func jsonInts(v any) []int {
	list, _ := v.([]any)
	out := []int{}
	for _, item := range list {
		switch n := item.(type) {
		case float64:
			out = append(out, int(n))
		case string:
			if i, err := strconv.Atoi(n); err == nil {
				out = append(out, i)
			}
		}
	}
	return out
}

func jsonString(v any) string {
	s, _ := v.(string)
	return s
}

func jsonNullInt(v any) sql.NullInt64 {
	if n, ok := v.(float64); ok {
		return sql.NullInt64{Int64: int64(n), Valid: true}
	}
	return sql.NullInt64{}
}

// indexerFromResource applies an IndexerResource from the API to base and
// validates it as Radarr does. An API key of ******** keeps the saved key.
func (h *handler) indexerFromResource(r *http.Request, res arrIndexer, base store.Indexer) (store.Indexer, []validationFailure) {
	ix := base
	ix.Name = strings.TrimSpace(res.Name)
	ix.Implementation = res.Implementation
	ix.EnableRSS, ix.EnableAutomaticSearch, ix.EnableInteractiveSearch = res.EnableRss, res.EnableAutomaticSearch, res.EnableInteractiveSearch
	ix.Priority, ix.DownloadClientID, ix.Tags = res.Priority, res.DownloadClientID, res.Tags
	if ix.Priority == 0 {
		ix.Priority = store.DefaultIndexerPriority
	}
	var extra []arrField
	for _, f := range res.Fields {
		switch f.Name {
		case "baseUrl":
			ix.BaseURL = strings.TrimSpace(jsonString(f.Value))
		case "apiPath":
			ix.APIPath = strings.TrimSpace(jsonString(f.Value))
		case "apiKey":
			if key := jsonString(f.Value); key != privateValue {
				ix.APIKey = key
			}
		case "categories":
			ix.Categories = jsonInts(f.Value)
		case "animeCategories":
			ix.AnimeCategories = jsonInts(f.Value)
		case "additionalParameters":
			ix.AdditionalParameters = strings.TrimSpace(jsonString(f.Value))
		case "minimumSeeders":
			if n, ok := f.Value.(float64); ok {
				ix.MinimumSeeders = int(n)
			}
		case "seedCriteria.seedRatio":
			ix.SeedRatio = sql.NullFloat64{}
			if n, ok := f.Value.(float64); ok {
				ix.SeedRatio = sql.NullFloat64{Float64: n, Valid: true}
			}
		case "seedCriteria.seedTime":
			ix.SeedTime = jsonNullInt(f.Value)
		case "seedCriteria.seasonPackSeedTime":
			ix.SeasonPackSeedTime = jsonNullInt(f.Value)
		case "seedCriteria.discographySeedTime":
			ix.DiscographySeedTime = jsonNullInt(f.Value)
		case "requiredFlags":
			ix.RequiredFlags = jsonInts(f.Value)
		case "rejectBlocklistedTorrentHashesWhileGrabbing":
			ix.RejectBlocklisted, _ = f.Value.(bool)
		default:
			extra = append(extra, arrField{Name: f.Name, Value: f.Value})
		}
	}
	if extra == nil {
		extra = []arrField{}
	}
	ix.ExtraFields, _ = json.Marshal(extra)
	if ix.APIPath == "" {
		ix.APIPath = "/api"
	}

	var failures []validationFailure
	if ix.Name == "" {
		failures = append(failures, failure("Name", "'Name' must not be empty.", ix.Name))
	} else if taken, err := store.IndexerNameTaken(r.Context(), h.deps.DB, ix.Name, ix.ID); err != nil || taken {
		failures = append(failures, failure("Name", "Should be unique", ix.Name))
	}
	if !validImplementation(ix.Implementation) {
		failures = append(failures, failure("Implementation", "UMMarr supports the Newznab and Torznab indexer types", ix.Implementation))
	}
	if !ix.Enabled() {
		return ix, failures // Radarr only validates the settings of an enabled indexer
	}
	if u, err := url.Parse(ix.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		failures = append(failures, failure("BaseUrl", "Invalid Url: '"+ix.BaseURL+"'", ix.BaseURL))
	}
	if !strings.HasPrefix(ix.APIPath, "/") {
		failures = append(failures, failure("ApiPath", "Must be a valid URL path (ie: '/api')", ix.APIPath))
	}
	if len(ix.Categories) == 0 && len(ix.AnimeCategories) == 0 {
		failures = append(failures, failure("", "'Categories' must be provided", nil))
	}
	if ix.AdditionalParameters != "" && !additionalParameters.MatchString(ix.AdditionalParameters) {
		failures = append(failures, failure("AdditionalParameters", "'Additional Parameters' is not in the correct format.", ix.AdditionalParameters))
	}
	return ix, failures
}

func decodeIndexer(w http.ResponseWriter, r *http.Request) (arrIndexer, bool) {
	var res arrIndexer
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&res); err != nil {
		writeJSON(w, http.StatusBadRequest, []validationFailure{failure("", "Couldn't read the indexer: "+err.Error(), nil)})
		return res, false
	}
	return res, true
}

func forceSave(r *http.Request) bool {
	v, _ := strconv.ParseBool(r.URL.Query().Get("forceSave"))
	return v
}

// testIndexer runs Radarr's connection test, answering 400 when it fails.
func (h *handler) testIndexer(w http.ResponseWriter, r *http.Request, ix store.Indexer) bool {
	if err := h.indexerService().Test(r.Context(), ix); err != nil {
		writeJSON(w, http.StatusBadRequest, []validationFailure{failure("", err.Error(), nil)})
		return false
	}
	return true
}

// syncedIndexer reads an indexer the API can see: converted entries from the
// old Prowlarr connection stay out of it, so Prowlarr's sync doesn't adopt them.
func (h *handler) syncedIndexer(w http.ResponseWriter, r *http.Request) (store.Indexer, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "NotFound"})
		return store.Indexer{}, false
	}
	ix, err := store.GetIndexer(r.Context(), h.deps.DB, id)
	if errors.Is(err, store.ErrIndexerNotFound) || err == nil && ix.Converted {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "NotFound"})
		return store.Indexer{}, false
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return store.Indexer{}, false
	}
	return ix, true
}

// ArrListIndexers is GET /api/v3/indexer.
func (h *handler) ArrListIndexers(w http.ResponseWriter, r *http.Request) {
	indexers, err := store.ListIndexers(r.Context(), h.deps.DB)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	out := []arrIndexer{}
	for _, ix := range indexers {
		if !ix.Converted {
			out = append(out, indexerResource(ix))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ArrGetIndexer is GET /api/v3/indexer/{id}.
func (h *handler) ArrGetIndexer(w http.ResponseWriter, r *http.Request) {
	if ix, ok := h.syncedIndexer(w, r); ok {
		writeJSON(w, http.StatusOK, indexerResource(ix))
	}
}

// ArrIndexerSchema is GET /api/v3/indexer/schema: templates for the two
// indexer types, ordered by name as Radarr orders them.
func (h *handler) ArrIndexerSchema(w http.ResponseWriter, r *http.Request) {
	out := []arrIndexer{}
	for _, implementation := range []string{newznab.Newznab, newznab.Torznab} {
		res := indexerResource(store.Indexer{Implementation: implementation, Priority: store.DefaultIndexerPriority})
		res.EnableRss, res.EnableAutomaticSearch, res.EnableInteractiveSearch = true, true, true
		res.Fields = schemaFields(implementation)
		res.Presets = []arrIndexer{}
		out = append(out, res)
	}
	writeJSON(w, http.StatusOK, out)
}

// ArrCreateIndexer is POST /api/v3/indexer. As in Radarr, an enabled indexer
// is tested first unless forceSave is set; Prowlarr retries with forceSave
// when the test fails.
func (h *handler) ArrCreateIndexer(w http.ResponseWriter, r *http.Request) {
	res, ok := decodeIndexer(w, r)
	if !ok {
		return
	}
	ix, failures := h.indexerFromResource(r, res, store.Indexer{})
	ix.ID, ix.Synced, ix.Converted = 0, true, false
	if len(failures) > 0 {
		writeJSON(w, http.StatusBadRequest, failures)
		return
	}
	if ix.Enabled() && !forceSave(r) && !h.testIndexer(w, r, ix) {
		return
	}
	err := store.WithTx(r.Context(), h.deps.DB, func(tx *sql.Tx) error {
		id, err := store.CreateIndexer(r.Context(), tx, ix)
		if err != nil {
			return err
		}
		ix.ID = id
		return store.AbsorbConvertedIndexer(r.Context(), tx, ix)
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	saved, err := store.GetIndexer(r.Context(), h.deps.DB, ix.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v3/indexer/%d", saved.ID))
	writeJSON(w, http.StatusCreated, indexerResource(saved))
}

func sameIndexer(a, b store.Indexer) bool {
	ja, _ := json.Marshal(indexerResource(a))
	jb, _ := json.Marshal(indexerResource(b))
	return string(ja) == string(jb) && a.APIKey == b.APIKey
}

// ArrUpdateIndexer is PUT /api/v3/indexer/{id}. Only a changed, enabled
// indexer is tested, and forceSave skips the test.
func (h *handler) ArrUpdateIndexer(w http.ResponseWriter, r *http.Request) {
	existing, ok := h.syncedIndexer(w, r)
	if !ok {
		return
	}
	res, ok := decodeIndexer(w, r)
	if !ok {
		return
	}
	ix, failures := h.indexerFromResource(r, res, existing)
	ix.ID, ix.Synced = existing.ID, true
	if len(failures) > 0 {
		writeJSON(w, http.StatusBadRequest, failures)
		return
	}
	if !sameIndexer(existing, ix) {
		if ix.Enabled() && !forceSave(r) && !h.testIndexer(w, r, ix) {
			return
		}
		err := store.WithTx(r.Context(), h.deps.DB, func(tx *sql.Tx) error {
			if err := store.UpdateIndexer(r.Context(), tx, ix); err != nil {
				return err
			}
			return store.AbsorbConvertedIndexer(r.Context(), tx, ix)
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
			return
		}
	}
	saved, err := store.GetIndexer(r.Context(), h.deps.DB, ix.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, indexerResource(saved))
}

// ArrDeleteIndexer is DELETE /api/v3/indexer/{id}.
func (h *handler) ArrDeleteIndexer(w http.ResponseWriter, r *http.Request) {
	ix, ok := h.syncedIndexer(w, r)
	if !ok {
		return
	}
	if err := store.DeleteIndexer(r.Context(), h.deps.DB, ix.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// ArrTestIndexer is POST /api/v3/indexer/test. Prowlarr's app test sends a
// placeholder indexer here only to read the version header, so a failing
// connection test isn't an error for an indexer with id 0 and no URL.
func (h *handler) ArrTestIndexer(w http.ResponseWriter, r *http.Request) {
	res, ok := decodeIndexer(w, r)
	if !ok {
		return
	}
	base := store.Indexer{}
	if res.ID > 0 {
		if saved, err := store.GetIndexer(r.Context(), h.deps.DB, res.ID); err == nil {
			base = saved
		}
	}
	ix, failures := h.indexerFromResource(r, res, base)
	var real []validationFailure
	for _, f := range failures {
		if f.ErrorMessage != "Should be unique" {
			real = append(real, f)
		}
	}
	if len(real) > 0 {
		writeJSON(w, http.StatusBadRequest, real)
		return
	}
	if !h.testIndexer(w, r, ix) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// ShowAPIKey returns UMMarr's API key as text, for the Settings page's Show
// and Copy buttons. The page itself never contains it.
func (h *handler) ShowAPIKey(w http.ResponseWriter, r *http.Request) {
	key, err := store.EnsureAPIKey(r.Context(), h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(key))
}

// RegenerateAPIKey replaces UMMarr's API key.
func (h *handler) RegenerateAPIKey(w http.ResponseWriter, r *http.Request) {
	if _, err := store.RegenerateAPIKey(r.Context(), h.deps.DB); err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<p class="indexer-test ok">New key made. Use Show or Copy, and give it to Prowlarr.</p>`))
}
