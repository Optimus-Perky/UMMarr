package api

import (
	"context"
	"database/sql"
	"errors"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	gosync "sync"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// indexerView is one card on Settings -> Indexers.
type indexerView struct {
	store.Indexer
	Media        string // e.g. "Movies · TV · Music"
	RestingUntil string // set while the indexer backs off after failures
}

func mediaLabel(ix store.Indexer) string {
	var parts []string
	for _, m := range []struct{ mediaType, label string }{
		{newznab.MediaMovie, "Movies"}, {newznab.MediaSeries, "TV"}, {newznab.MediaMusic, "Music"},
	} {
		if len(newznab.CategoriesFor(ix.AllCategories(), m.mediaType)) > 0 {
			parts = append(parts, m.label)
		}
	}
	return strings.Join(parts, " · ")
}

// indexerViews lists the indexer cards. syncHint is true when Prowlarr has
// synced indexers but only with movie categories - its Radarr app's Sync
// Categories need TV and Audio ticked for UMMarr to use them for everything.
func (h *handler) indexerViews(ctx context.Context) (views []indexerView, syncHint bool, err error) {
	indexers, err := store.ListIndexers(ctx, h.deps.DB)
	if err != nil {
		return nil, false, err
	}
	synced, syncedBeyondMovies := 0, false
	now := time.Now()
	for _, ix := range indexers {
		v := indexerView{Indexer: ix, Media: mediaLabel(ix)}
		if ix.BackedOff(now) {
			v.RestingUntil = ix.DisabledUntil.Time.Local().Format("15:04 on 2 Jan")
		}
		if ix.Synced {
			synced++
			if strings.Contains(v.Media, "TV") || strings.Contains(v.Media, "Music") {
				syncedBeyondMovies = true
			}
		}
		views = append(views, v)
	}
	return views, synced > 0 && !syncedBeyondMovies, nil
}

func (h *handler) indexerService() *sync.IndexerService {
	if h.deps.Indexer != nil {
		return h.deps.Indexer
	}
	return &sync.IndexerService{DB: h.deps.DB}
}

type categoryOption struct {
	ID      int
	Name    string
	Checked bool
}

type categoryGroup struct {
	Label   string
	Options []categoryOption
}

type flagOption struct {
	Value   int
	Name    string
	Checked bool
}

type indexerFormData struct {
	Indexer        store.Indexer
	IsNew          bool
	Errors         map[string]string
	CategoryGroups []categoryGroup
	AnimeOptions   []categoryOption
	Flags          []flagOption

	SeedRatio, SeedTime, SeasonPackSeedTime, DiscographySeedTime string
}

func contains(ids []int, id int) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func nullInt(n sql.NullInt64) string {
	if !n.Valid {
		return ""
	}
	return strconv.FormatInt(n.Int64, 10)
}

func newIndexerFormData(ix store.Indexer, isNew bool, errs map[string]string) indexerFormData {
	d := indexerFormData{Indexer: ix, IsNew: isNew, Errors: errs,
		SeedTime: nullInt(ix.SeedTime), SeasonPackSeedTime: nullInt(ix.SeasonPackSeedTime), DiscographySeedTime: nullInt(ix.DiscographySeedTime)}
	if ix.SeedRatio.Valid {
		d.SeedRatio = strconv.FormatFloat(ix.SeedRatio.Float64, 'f', -1, 64)
	}
	groups := []categoryGroup{{Label: "Movies"}, {Label: "TV"}, {Label: "Audio"}}
	for _, c := range newznab.StandardCategories {
		opt := categoryOption{ID: c.ID, Name: c.Name, Checked: contains(ix.Categories, c.ID)}
		switch newznab.MediaTypeOf(c.ID) {
		case newznab.MediaMovie:
			groups[0].Options = append(groups[0].Options, opt)
		case newznab.MediaSeries:
			groups[1].Options = append(groups[1].Options, opt)
			d.AnimeOptions = append(d.AnimeOptions, categoryOption{ID: c.ID, Name: c.Name, Checked: contains(ix.AnimeCategories, c.ID)})
		case newznab.MediaMusic:
			groups[2].Options = append(groups[2].Options, opt)
		}
	}
	// Categories outside the standard list (an indexer's own, 100000 and up)
	// stay visible so saving the form doesn't drop them.
	other := categoryGroup{Label: "Other"}
	for _, id := range ix.Categories {
		if newznab.CategoryName(id) == "" {
			other.Options = append(other.Options, categoryOption{ID: id, Name: strconv.Itoa(id), Checked: true})
		}
	}
	if len(other.Options) > 0 {
		groups = append(groups, other)
	}
	d.CategoryGroups = groups
	for _, f := range newznab.FlagNames {
		d.Flags = append(d.Flags, flagOption{Value: int(f.Flag), Name: f.Name, Checked: contains(ix.RequiredFlags, int(f.Flag))})
	}
	return d
}

// defaultIndexer is a new indexer as Radarr starts one, with categories for
// all three media types.
func defaultIndexer(implementation string) store.Indexer {
	var cats []int
	for _, mediaType := range []string{newznab.MediaMovie, newznab.MediaSeries, newznab.MediaMusic} {
		cats = append(cats, newznab.SyncCategories[mediaType]...)
	}
	return store.Indexer{
		Implementation: implementation, EnableRSS: true, EnableAutomaticSearch: true, EnableInteractiveSearch: true,
		Priority: store.DefaultIndexerPriority, APIPath: "/api", Categories: cats, MinimumSeeders: 1,
	}
}

func validImplementation(s string) bool {
	return s == newznab.Torznab || s == newznab.Newznab
}

var additionalParameters = regexp.MustCompile(`^(&[^&=]+=[^&]*)+$`)

func formInts(values []string) []int {
	var out []int
	for _, v := range values {
		if n, err := strconv.Atoi(v); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// parseIndexerForm applies the indexer form to ix. A blank API key keeps the
// saved one, so the masked field never clears it.
func parseIndexerForm(r *http.Request, ix store.Indexer) (store.Indexer, map[string]string) {
	errs := map[string]string{}
	text := func(name string) string { return strings.TrimSpace(r.FormValue(name)) }
	checked := func(name string) bool { return r.FormValue(name) == "on" }
	optionalInt := func(name string) sql.NullInt64 {
		v := text(name)
		if v == "" {
			return sql.NullInt64{}
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			errs[name] = "Enter a whole number, 0 or more, or leave it empty."
			return sql.NullInt64{}
		}
		return sql.NullInt64{Int64: n, Valid: true}
	}

	if ix.Name = text("name"); ix.Name == "" {
		errs["name"] = "Name is required."
	}
	if impl := text("implementation"); validImplementation(impl) {
		ix.Implementation = impl
	} else if !validImplementation(ix.Implementation) {
		errs["implementation"] = "Pick Torznab or Newznab."
	}
	ix.EnableRSS, ix.EnableAutomaticSearch, ix.EnableInteractiveSearch = checked("enable_rss"), checked("enable_automatic_search"), checked("enable_interactive_search")

	ix.BaseURL = text("base_url")
	if u, err := url.Parse(ix.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs["base_url"] = "Enter the indexer's full URL, starting with http:// or https://."
	}
	if ix.APIPath = text("api_path"); ix.APIPath == "" {
		ix.APIPath = "/api"
	} else if !strings.HasPrefix(ix.APIPath, "/") {
		errs["api_path"] = "Start the API path with /, e.g. /api."
	}
	if key := text("api_key"); key != "" {
		ix.APIKey = key
	}
	ix.Categories, ix.AnimeCategories = formInts(r.Form["categories"]), formInts(r.Form["anime_categories"])
	if len(ix.Categories) == 0 && len(ix.AnimeCategories) == 0 {
		errs["categories"] = "Pick at least one category."
	}
	if ix.AdditionalParameters = text("additional_parameters"); ix.AdditionalParameters != "" && !additionalParameters.MatchString(ix.AdditionalParameters) {
		errs["additional_parameters"] = "Use the form &name=value, e.g. &foo=bar."
	}

	if ix.Implementation == newznab.Torznab {
		if n, err := strconv.Atoi(text("minimum_seeders")); err != nil || n < 0 {
			errs["minimum_seeders"] = "Enter a whole number, 0 or more."
		} else {
			ix.MinimumSeeders = n
		}
		ix.SeedRatio = sql.NullFloat64{}
		if v := text("seed_ratio"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err != nil || f < 0 {
				errs["seed_ratio"] = "Enter a ratio such as 1.0, or leave it empty."
			} else {
				ix.SeedRatio = sql.NullFloat64{Float64: f, Valid: true}
			}
		}
		ix.SeedTime = optionalInt("seed_time")
		ix.SeasonPackSeedTime = optionalInt("season_pack_seed_time")
		ix.DiscographySeedTime = optionalInt("discography_seed_time")
		ix.RequiredFlags = formInts(r.Form["required_flags"])
		ix.RejectBlocklisted = checked("reject_blocklisted")
	}
	if n, err := strconv.Atoi(text("priority")); err != nil || n < 1 || n > 50 {
		errs["priority"] = "Pick a priority from 1 (highest) to 50 (lowest)."
	} else {
		ix.Priority = n
	}
	if v := text("grab_limit"); v != "" {
		if n, err := strconv.Atoi(v); err != nil || n < 0 {
			errs["grab_limit"] = "Enter a whole number, or 0 for no limit."
		} else {
			ix.GrabLimit = n
		}
	}
	if v := text("grab_limit_reset_hour"); v != "" {
		if n, err := strconv.Atoi(v); err != nil || n < 0 || n > 23 {
			errs["grab_limit_reset_hour"] = "Enter the hour of day in UTC, 0 to 23."
		} else {
			ix.GrabLimitResetHour = n
		}
	}
	return ix, errs
}

func (h *handler) NewIndexerForm(w http.ResponseWriter, r *http.Request) {
	implementation := r.URL.Query().Get("implementation")
	if !validImplementation(implementation) {
		http.Error(w, "implementation must be Torznab or Newznab", http.StatusBadRequest)
		return
	}
	h.renderPartial(w, "indexer_form", newIndexerFormData(defaultIndexer(implementation), true, nil))
}

func (h *handler) indexerFromPath(w http.ResponseWriter, r *http.Request) (store.Indexer, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid indexer id", http.StatusBadRequest)
		return store.Indexer{}, false
	}
	ix, err := store.GetIndexer(r.Context(), h.deps.DB, id)
	if errors.Is(err, store.ErrIndexerNotFound) {
		http.NotFound(w, r)
		return store.Indexer{}, false
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return store.Indexer{}, false
	}
	return ix, true
}

func (h *handler) EditIndexerForm(w http.ResponseWriter, r *http.Request) {
	if ix, ok := h.indexerFromPath(w, r); ok {
		h.renderPartial(w, "indexer_form", newIndexerFormData(ix, false, nil))
	}
}

// CreateIndexer saves the Add Indexer dialog. Problems re-render the dialog
// with each message beside its field (status 200, so htmx swaps it in).
func (h *handler) CreateIndexer(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ix, errs := parseIndexerForm(r, defaultIndexer(r.FormValue("implementation")))
	if len(errs) > 0 {
		h.renderPartial(w, "indexer_form", newIndexerFormData(ix, true, errs))
		return
	}
	if _, err := store.CreateIndexer(r.Context(), h.deps.DB, ix); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/indexers")
	w.WriteHeader(http.StatusOK)
}

// UpdateIndexer saves the Edit Indexer dialog.
func (h *handler) UpdateIndexer(w http.ResponseWriter, r *http.Request) {
	existing, ok := h.indexerFromPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ix, errs := parseIndexerForm(r, existing)
	if len(errs) > 0 {
		h.renderPartial(w, "indexer_form", newIndexerFormData(ix, false, errs))
		return
	}
	if err := store.UpdateIndexer(r.Context(), h.deps.DB, ix); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/indexers")
	w.WriteHeader(http.StatusOK)
}

func (h *handler) DeleteIndexer(w http.ResponseWriter, r *http.Request) {
	ix, ok := h.indexerFromPath(w, r)
	if !ok {
		return
	}
	if err := store.DeleteIndexer(r.Context(), h.deps.DB, ix.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/indexers")
	w.WriteHeader(http.StatusOK)
}

type indexerTestResult struct {
	Name    string
	OK      bool
	Message string
}

// TestIndexer tests the dialog's current values without saving them. For a
// saved indexer, a blank API key field tests with the saved key.
func (h *handler) TestIndexer(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	base := defaultIndexer(r.FormValue("implementation"))
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		if saved, err := store.GetIndexer(r.Context(), h.deps.DB, id); err == nil {
			base = saved
		}
	}
	ix, errs := parseIndexerForm(r, base)
	if len(errs) > 0 {
		messages := make([]string, 0, len(errs))
		for _, name := range []string{"name", "implementation", "base_url", "api_path", "categories", "additional_parameters", "minimum_seeders", "seed_ratio", "seed_time", "season_pack_seed_time", "discography_seed_time", "priority"} {
			if msg, ok := errs[name]; ok {
				messages = append(messages, msg)
			}
		}
		h.renderPartial(w, "indexer_test_result", indexerTestResult{Message: strings.Join(messages, " ")})
		return
	}
	if err := h.indexerService().Test(r.Context(), ix); err != nil {
		h.renderPartial(w, "indexer_test_result", indexerTestResult{Message: err.Error()})
		return
	}
	h.renderPartial(w, "indexer_test_result", indexerTestResult{OK: true})
}

// TestAllIndexers tests every indexer at once, recording failures the same
// way a failed search does.
func (h *handler) TestAllIndexers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	indexers, err := store.ListIndexers(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	results := make([]indexerTestResult, len(indexers))
	var wg gosync.WaitGroup
	for i, ix := range indexers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = indexerTestResult{Name: ix.Name, OK: true}
			if err := h.indexerService().Test(ctx, ix); err != nil {
				results[i] = indexerTestResult{Name: ix.Name, Message: err.Error()}
			}
		}()
	}
	wg.Wait()
	for i, ix := range indexers {
		if results[i].OK {
			_ = store.RecordIndexerSuccess(ctx, h.deps.DB, ix.ID)
		} else {
			_ = store.RecordIndexerFailure(ctx, h.deps.DB, ix.ID, results[i].Message, time.Now())
		}
	}
	h.renderPartial(w, "indexer_test_all", results)
}

func renderInlineError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<p class="indexer-test failed">` + html.EscapeString(message) + `</p>`))
}

// BulkEditIndexers is Manage Indexers: switch RSS, automatic and interactive
// search on or off, set the priority, or delete, for every selected indexer.
func (h *handler) BulkEditIndexers(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var ids []int64
	for _, v := range r.Form["ids"] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		renderInlineError(w, "Select at least one indexer.")
		return
	}
	priority := 0
	if v := strings.TrimSpace(r.FormValue("priority")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 50 {
			renderInlineError(w, "Pick a priority from 1 (highest) to 50 (lowest), or leave it empty.")
			return
		}
		priority = n
	}
	apply := func(name string, current *bool) {
		switch r.FormValue(name) {
		case "enable":
			*current = true
		case "disable":
			*current = false
		}
	}
	ctx := r.Context()
	err := store.WithTx(ctx, h.deps.DB, func(tx *sql.Tx) error {
		for _, id := range ids {
			if r.FormValue("action") == "delete" {
				if err := store.DeleteIndexer(ctx, tx, id); err != nil && !errors.Is(err, store.ErrIndexerNotFound) {
					return err
				}
				continue
			}
			ix, err := store.GetIndexer(ctx, tx, id)
			if errors.Is(err, store.ErrIndexerNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			apply("enable_rss", &ix.EnableRSS)
			apply("enable_automatic_search", &ix.EnableAutomaticSearch)
			apply("enable_interactive_search", &ix.EnableInteractiveSearch)
			if priority > 0 {
				ix.Priority = priority
			}
			if err := store.UpdateIndexer(ctx, tx, ix); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", "/settings/indexers")
	w.WriteHeader(http.StatusOK)
}
