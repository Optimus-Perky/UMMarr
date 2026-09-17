package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Settings > Import Lists, as in Radarr/Sonarr: a card per list, an Add
// dialog offering each source, an edit dialog with Test (a preview), and
// the exclusion list.

var listLabels = map[string]string{
	store.ListTMDBList: "TMDB List", store.ListTMDBPopular: "TMDB Popular", store.ListTMDBTopRated: "TMDB Top Rated", store.ListTMDBTrending: "TMDB Trending",
	store.ListTMDBDiscover: "TMDB Discover", store.ListPlexRSS: "Plex Watchlist (RSS)", store.ListTrakt: "Trakt List",
}

var listFields = map[string][]string{
	store.ListTMDBList: {"list_id"}, store.ListTMDBPopular: {"limit"}, store.ListTMDBTopRated: {"limit"}, store.ListTMDBTrending: {"limit"},
	store.ListTMDBDiscover: {"genres", "min_rating", "min_votes", "language", "year_from", "year_to", "limit"},
	store.ListPlexRSS:      {"url"}, store.ListTrakt: {"user", "list", "client_id"},
}

var listRequired = map[string][]string{
	store.ListTMDBList: {"list_id"}, store.ListPlexRSS: {"url"}, store.ListTrakt: {"user", "list", "client_id"},
}

type importListView struct {
	store.ImportList
	Label          string
	MediaLabel     string
	LastSyncText   string
	RootFolderPath string
	QualityProfile string
	Set            map[string]bool
}

func (h *handler) importListView(ctx context.Context, l store.ImportList) importListView {
	v := importListView{ImportList: l, Label: listLabels[l.Implementation], MediaLabel: map[string]string{"movie": "Movies", "series": "TV"}[l.MediaType], LastSyncText: "never", Set: map[string]bool{}}
	if l.LastSync.Valid {
		v.LastSyncText = l.LastSync.Time.Local().Format("2 Jan 15:04")
	}
	if l.RootFolderID.Valid {
		if p, err := store.GetRootFolderPath(ctx, h.deps.DB, l.RootFolderID.Int64); err == nil {
			v.RootFolderPath = p
		}
	}
	if l.QualityProfileID.Valid {
		if profiles, err := store.ListQualityProfiles(ctx, h.deps.DB); err == nil {
			for _, p := range profiles {
				if p.ID == l.QualityProfileID.Int64 {
					v.QualityProfile = p.Name
				}
			}
		}
	}
	for k, val := range l.Settings {
		v.Set[k] = val != ""
	}
	return v
}

type importListFormData struct {
	List            importListView
	IsNew           bool
	Errors          map[string]string
	RootFolders     []store.RootFolder // for the list's media type
	QualityProfiles []store.QualityProfile
}

func defaultImportList(implementation, mediaType string) store.ImportList {
	if mediaType != "series" {
		mediaType = "movie"
	}
	l := store.ImportList{Implementation: implementation, Enabled: true, MediaType: mediaType, Name: listLabels[implementation], Settings: map[string]string{}, Monitored: true, AutoAdd: true}
	switch implementation {
	case store.ListTMDBPopular, store.ListTMDBTopRated, store.ListTMDBTrending, store.ListTMDBDiscover:
		l.Settings["limit"] = "50"
	}
	return l
}

func (h *handler) importListFormData(ctx context.Context, l store.ImportList, isNew bool, errs map[string]string) importListFormData {
	folders, _ := store.ListRootFolders(ctx, h.deps.DB, l.MediaType)
	profiles, _ := store.ListQualityProfiles(ctx, h.deps.DB)
	return importListFormData{List: h.importListView(ctx, l), IsNew: isNew, Errors: errs, RootFolders: folders, QualityProfiles: profiles}
}

func parseImportListForm(r *http.Request, existing store.ImportList) (store.ImportList, map[string]string) {
	l := existing
	errs := map[string]string{}
	l.Name = strings.TrimSpace(r.FormValue("name"))
	if l.Name == "" {
		errs["name"] = "Name is required."
	}
	if impl := r.FormValue("implementation"); impl != "" {
		l.Implementation = impl
	}
	if listLabels[l.Implementation] == "" {
		errs["implementation"] = "Unknown list type."
	}
	if mt := r.FormValue("media_type"); mt == "movie" || mt == "series" {
		l.MediaType = mt
	}
	l.Enabled, l.Monitored, l.AutoAdd, l.SearchOnAdd = r.FormValue("enabled") == "on", r.FormValue("monitored") == "on", r.FormValue("auto_add") == "on", r.FormValue("search_on_add") == "on"
	if id, err := strconv.ParseInt(r.FormValue("root_folder_id"), 10, 64); err == nil && id > 0 {
		l.RootFolderID = sql.NullInt64{Int64: id, Valid: true}
	} else {
		l.RootFolderID = sql.NullInt64{}
	}
	if id, err := strconv.ParseInt(r.FormValue("quality_profile_id"), 10, 64); err == nil && id > 0 {
		l.QualityProfileID = sql.NullInt64{Int64: id, Valid: true}
	} else {
		l.QualityProfileID = sql.NullInt64{}
	}
	settings := map[string]string{}
	for k, v := range existing.Settings {
		settings[k] = v
	}
	for _, field := range listFields[l.Implementation] {
		v := strings.TrimSpace(r.FormValue(field))
		if v == "" && field == "client_id" {
			continue // blank keeps the saved id
		}
		settings[field] = v
	}
	l.Settings = settings
	for _, field := range listRequired[l.Implementation] {
		if settings[field] == "" {
			errs[field] = "Required."
		}
	}
	return l, errs
}

func (h *handler) importListViews(ctx context.Context) ([]importListView, error) {
	lists, err := store.ListImportLists(ctx, h.deps.DB)
	if err != nil {
		return nil, err
	}
	views := make([]importListView, 0, len(lists))
	for _, l := range lists {
		views = append(views, h.importListView(ctx, l))
	}
	return views, nil
}

func (h *handler) NewImportListForm(w http.ResponseWriter, r *http.Request) {
	implementation := r.URL.Query().Get("implementation")
	if listLabels[implementation] == "" {
		http.Error(w, "unknown list type", http.StatusBadRequest)
		return
	}
	h.renderPartial(w, "import_list_form", h.importListFormData(r.Context(), defaultImportList(implementation, r.URL.Query().Get("media_type")), true, nil))
}

func (h *handler) importListFromPath(w http.ResponseWriter, r *http.Request) (store.ImportList, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid import list id", http.StatusBadRequest)
		return store.ImportList{}, false
	}
	l, err := store.GetImportList(r.Context(), h.deps.DB, id)
	if errors.Is(err, store.ErrImportListNotFound) {
		http.NotFound(w, r)
		return store.ImportList{}, false
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return store.ImportList{}, false
	}
	return l, true
}

func (h *handler) EditImportListForm(w http.ResponseWriter, r *http.Request) {
	if l, ok := h.importListFromPath(w, r); ok {
		h.renderPartial(w, "import_list_form", h.importListFormData(r.Context(), l, false, nil))
	}
}

func (h *handler) saveImportList(w http.ResponseWriter, r *http.Request, existing store.ImportList, isNew bool) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	l, errs := parseImportListForm(r, existing)
	if taken, err := store.ImportListNameTaken(r.Context(), h.deps.DB, l.Name, l.ID); err == nil && taken {
		errs["name"] = "Another list already has that name."
	}
	if len(errs) > 0 {
		h.renderPartial(w, "import_list_form", h.importListFormData(r.Context(), l, isNew, errs))
		return
	}
	var err error
	if isNew {
		_, err = store.CreateImportList(r.Context(), h.deps.DB, l)
	} else {
		err = store.UpdateImportList(r.Context(), h.deps.DB, l)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/import-lists")
	w.WriteHeader(http.StatusOK)
}

func (h *handler) CreateImportList(w http.ResponseWriter, r *http.Request) {
	h.saveImportList(w, r, defaultImportList(r.FormValue("implementation"), r.FormValue("media_type")), true)
}

func (h *handler) UpdateImportList(w http.ResponseWriter, r *http.Request) {
	if existing, ok := h.importListFromPath(w, r); ok {
		h.saveImportList(w, r, existing, false)
	}
}

func (h *handler) DeleteImportList(w http.ResponseWriter, r *http.Request) {
	l, ok := h.importListFromPath(w, r)
	if !ok {
		return
	}
	if err := store.DeleteImportList(r.Context(), h.deps.DB, l.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/import-lists")
	w.WriteHeader(http.StatusOK)
}

// TestImportList fetches the list with the dialog's values and shows what
// it would add.
func (h *handler) TestImportList(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	base := defaultImportList(r.FormValue("implementation"), r.FormValue("media_type"))
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		if saved, err := store.GetImportList(r.Context(), h.deps.DB, id); err == nil {
			base = saved
		}
	}
	l, errs := parseImportListForm(r, base)
	if len(errs) > 0 {
		var messages []string
		for field, msg := range errs {
			messages = append(messages, field+": "+msg)
		}
		h.renderPartial(w, "indexer_test_result", indexerTestResult{Message: strings.Join(messages, " ")})
		return
	}
	if h.deps.ImportLists == nil {
		h.renderPartial(w, "indexer_test_result", indexerTestResult{Message: "import lists aren't available"})
		return
	}
	items, err := h.deps.ImportLists.Preview(r.Context(), l)
	if err != nil {
		h.renderPartial(w, "indexer_test_result", indexerTestResult{Message: err.Error()})
		return
	}
	newCount := 0
	var sample []string
	for _, it := range items {
		if !it.Tracked && !it.Excluded {
			newCount++
			if len(sample) < 5 {
				title := it.Title
				if it.Year > 0 {
					title = fmt.Sprintf("%s (%d)", it.Title, it.Year)
				}
				sample = append(sample, title)
			}
		}
	}
	msg := fmt.Sprintf("Found %d titles, %d not yet in the library", len(items), newCount)
	if len(sample) > 0 {
		msg += ": " + strings.Join(sample, ", ")
		if newCount > len(sample) {
			msg += ", …"
		}
	}
	h.renderPartial(w, "indexer_test_result", indexerTestResult{OK: true, Message: msg})
}

func (h *handler) SyncImportLists(w http.ResponseWriter, r *http.Request) {
	if h.deps.Tasks == nil {
		http.Error(w, "tasks aren't available", http.StatusServiceUnavailable)
		return
	}
	msg := "Import list sync started - new titles appear in History."
	if err := h.deps.Tasks.RunNow(r.Context(), "Import list sync"); err != nil {
		msg = err.Error()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<p class="indexer-test ok">%s</p>`, msg)
}

func (h *handler) DeleteExclusion(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid exclusion id", http.StatusBadRequest)
		return
	}
	if err := store.DeleteExclusion(r.Context(), h.deps.DB, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/import-lists")
	w.WriteHeader(http.StatusOK)
}

func (h *handler) AddExclusion(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tmdbID, err := strconv.Atoi(strings.TrimSpace(r.FormValue("tmdb_id")))
	mediaType := r.FormValue("media_type")
	if err != nil || tmdbID <= 0 || (mediaType != "movie" && mediaType != "series") {
		renderAddError(w, errors.New("a TMDB id and a media type are needed"))
		return
	}
	if err := store.AddExclusion(r.Context(), h.deps.DB, store.Exclusion{MediaType: mediaType, TMDBID: tmdbID, Title: strings.TrimSpace(r.FormValue("title"))}); err != nil {
		renderAddError(w, err)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/import-lists")
	w.WriteHeader(http.StatusOK)
}
