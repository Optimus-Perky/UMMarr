package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// Radarr's and Sonarr's mass editor (Select on the Movies and TV pages) and
// Sonarr's Season Pass.

type libraryNoun struct{ one, many string }

func (n libraryNoun) plural(count int) string {
	if count == 1 {
		return n.one
	}
	return n.many
}

// libraryNouns is what a kind is called and the page it lives on.
func libraryNouns(kind string) (libraryNoun, string) {
	switch kind {
	case "series":
		return libraryNoun{"series", "series"}, "/tv"
	case "music":
		return libraryNoun{"artist", "artists"}, "/music"
	}
	return libraryNoun{"movie", "movies"}, "/movies"
}

// libraryNotice is what the Movies or TV page says after the editor
// redirects back to it.
func libraryNotice(r *http.Request, kind, fallback string) string {
	noun, _ := libraryNouns(kind)
	q := r.URL.Query()
	if v := q.Get("edited"); v != "" {
		n, _ := strconv.Atoi(v)
		return fmt.Sprintf("%d %s updated.", n, noun.plural(n))
	}
	if v := q.Get("deleted"); v != "" {
		n, _ := strconv.Atoi(v)
		kept, _ := strconv.Atoi(q.Get("kept"))
		text := fmt.Sprintf("%d %s removed.", n, noun.plural(n))
		if kept > 0 {
			text += fmt.Sprintf(" Files were kept for %d, because their folders aren't inside a library folder.", kept)
		}
		return text
	}
	return fallback
}

func selectedIDs(r *http.Request) []int64 {
	var ids []int64
	seen := map[int64]bool{}
	for _, v := range r.Form["id"] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// noChangeBool reads a No Change / Yes / No select.
func noChangeBool(v string) *bool {
	switch v {
	case "true":
		b := true
		return &b
	case "false":
		b := false
		return &b
	}
	return nil
}

func libraryEditFromForm(r *http.Request) (store.LibraryEdit, error) {
	e := store.LibraryEdit{Monitored: noChangeBool(r.FormValue("monitored")), SeasonFolder: noChangeBool(r.FormValue("season_folder"))}
	if v := r.FormValue("quality_profile_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return e, errors.New("pick a quality profile")
		}
		e.QualityProfileID = &id
	}
	if v := r.FormValue("minimum_availability"); v != "" {
		e.MinimumAvailability = &v
	}
	if v := r.FormValue("series_type"); v != "" {
		e.SeriesType = &v
	}
	tags := strings.TrimSpace(r.FormValue("tags"))
	if mode := r.FormValue("tag_mode"); tags != "" || mode == "replace" {
		if mode == "" {
			mode = "add"
		}
		e.TagMode, e.Tags = mode, strings.Split(tags, ",")
	}
	return e, nil
}

func firstFew(list []string, n int) string {
	if len(list) > n {
		return strings.Join(list[:n], "; ") + fmt.Sprintf("; and %d more", len(list)-n)
	}
	return strings.Join(list, "; ")
}

// MoviesEditorSave is the mass editor's Edit for movies.
func (h *handler) MoviesEditorSave(w http.ResponseWriter, r *http.Request) {
	h.libraryEditorSave(w, r, "movie")
}

// SeriesEditorSave is the mass editor's Edit for series.
func (h *handler) SeriesEditorSave(w http.ResponseWriter, r *http.Request) {
	h.libraryEditorSave(w, r, "series")
}

func (h *handler) libraryEditorSave(w http.ResponseWriter, r *http.Request, kind string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	noun, page := libraryNouns(kind)
	ids := selectedIDs(r)
	if len(ids) == 0 {
		renderInlineError(w, "Select at least one "+noun.one+".")
		return
	}
	e, err := libraryEditFromForm(r)
	if err != nil {
		renderInlineError(w, err.Error())
		return
	}
	ctx := r.Context()
	err = store.WithTx(ctx, h.deps.DB, func(tx *sql.Tx) error {
		if kind == "movie" {
			return store.EditMovies(ctx, tx, ids, e)
		}
		return store.EditSeries(ctx, tx, ids, e)
	})
	if err != nil {
		renderInlineError(w, err.Error())
		return
	}
	if v := r.FormValue("root_folder_id"); v != "" {
		rootID, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			renderInlineError(w, "Pick a library folder.")
			return
		}
		moveFiles := r.FormValue("move_files") == "on"
		var failures []string
		for _, id := range ids {
			if err := h.deps.Import.ChangeRootFolder(ctx, kind, id, rootID, moveFiles); err != nil {
				failures = append(failures, err.Error())
			}
		}
		if len(failures) > 0 {
			renderInlineError(w, fmt.Sprintf("Saved, but %d of %d %s couldn't change library folder: %s", len(failures), len(ids), noun.plural(len(ids)), firstFew(failures, 3)))
			return
		}
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("%s?edited=%d", page, len(ids)))
	w.WriteHeader(http.StatusOK)
}

// MoviesEditorDelete is the mass editor's Delete for movies.
func (h *handler) MoviesEditorDelete(w http.ResponseWriter, r *http.Request) {
	h.libraryEditorDelete(w, r, "movie")
}

// SeriesEditorDelete is the mass editor's Delete for series.
func (h *handler) SeriesEditorDelete(w http.ResponseWriter, r *http.Request) {
	h.libraryEditorDelete(w, r, "series")
}

func (h *handler) libraryEditorDelete(w http.ResponseWriter, r *http.Request, kind string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	noun, page := libraryNouns(kind)
	ids := selectedIDs(r)
	if len(ids) == 0 {
		renderInlineError(w, "Select at least one "+noun.one+".")
		return
	}
	ctx := r.Context()
	deleteFiles, exclude := r.FormValue("delete_files") == "on", r.FormValue("add_exclusion") == "on"
	deleted, kept := 0, 0
	var failures []string
	for _, id := range ids {
		if exclude {
			h.excludeItem(r, kind, id)
		}
		var err error
		if kind == "movie" {
			err = h.deps.Import.DeleteMovie(ctx, id, deleteFiles)
		} else {
			err = h.deps.Import.DeleteSeries(ctx, id, deleteFiles)
		}
		switch {
		case errors.Is(err, sync.ErrUnsafeDelete):
			deleted++
			kept++
		case err != nil:
			failures = append(failures, err.Error())
		default:
			deleted++
		}
	}
	if len(failures) > 0 {
		renderInlineError(w, fmt.Sprintf("Removed %d; %d failed: %s", deleted, len(failures), firstFew(failures, 3)))
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("%s?deleted=%d&kept=%d", page, deleted, kept))
	w.WriteHeader(http.StatusOK)
}

// LibraryEditorSearch is the mass editor's Search: an automatic search for
// every selected movie or series, run in the background.
func (h *handler) LibraryEditorSearch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	kind := "movie"
	if strings.HasPrefix(r.URL.Path, "/tv/") {
		kind = "series"
	}
	noun, _ := libraryNouns(kind)
	ids := selectedIDs(r)
	if len(ids) == 0 {
		renderInlineError(w, "Select at least one "+noun.one+".")
		return
	}
	if h.deps.Search == nil {
		renderInlineError(w, "Automatic search isn't available: add an indexer and a download client first.")
		return
	}
	search := h.deps.Search
	go func() {
		ctx := context.Background()
		for _, id := range ids {
			var err error
			if kind == "movie" {
				_, err = search.SearchMovie(ctx, id)
			} else {
				_, err = search.SearchSeries(ctx, id, nil)
			}
			if err != nil {
				log.Printf("mass editor search %s %d: %v", kind, id, err)
			}
		}
	}()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<p class="indexer-test ok">Searching %d %s in the background. Grabs show under Activity and History.</p>`, len(ids), noun.plural(len(ids)))
}

// SeriesEditorMonitor is the mass editor's Monitoring for series.
func (h *handler) SeriesEditorMonitor(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ids := selectedIDs(r)
	if len(ids) == 0 {
		renderInlineError(w, "Select at least one series.")
		return
	}
	for _, id := range ids {
		if err := store.ApplyMonitorOption(r.Context(), h.deps.DB, id, r.FormValue("monitor"), time.Now()); err != nil {
			renderInlineError(w, err.Error())
			return
		}
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/tv?edited=%d", len(ids)))
	w.WriteHeader(http.StatusOK)
}

type seasonPassChip struct {
	SeriesID int64
	store.SeasonPassSeason
}

type seasonPassRow struct {
	store.SeasonPassSeries
	MonitoredChip monitoredChipView
	Chips         []seasonPassChip
}

type seasonPassPageData struct {
	Active, PageTitle, Notice string
	Rows                      []seasonPassRow
	MonitorOptions            []store.MonitorOption
}

// SeasonPass is Sonarr's Season Pass: every series with a toggle per season.
func (h *handler) SeasonPass(w http.ResponseWriter, r *http.Request) {
	list, err := store.ListSeasonPass(r.Context(), h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]seasonPassRow, 0, len(list))
	for _, s := range list {
		row := seasonPassRow{SeasonPassSeries: s, MonitoredChip: monitoredChipView{
			Monitored: s.Monitored, ToggleURL: fmt.Sprintf("/tv/%d/monitored", s.ID), LabelOn: "Monitored", LabelOff: "Unmonitored",
		}}
		for _, se := range s.Seasons {
			row.Chips = append(row.Chips, seasonPassChip{SeriesID: s.ID, SeasonPassSeason: se})
		}
		rows = append(rows, row)
	}
	notice := ""
	if v := r.URL.Query().Get("saved"); v != "" {
		n, _ := strconv.Atoi(v)
		notice = fmt.Sprintf("Season Pass saved for %d series.", n)
	}
	h.renderPage(w, "season_pass", seasonPassPageData{Active: "tv", PageTitle: "Season Pass", Notice: notice, Rows: rows, MonitorOptions: store.MonitorOptions})
}

// SeasonPassToggle flips one season's monitored flag and re-renders its chip.
func (h *handler) SeasonPassToggle(w http.ResponseWriter, r *http.Request) {
	seriesID, err1 := strconv.ParseInt(r.URL.Query().Get("series"), 10, 64)
	season, err2 := strconv.Atoi(r.URL.Query().Get("season"))
	if err1 != nil || err2 != nil {
		http.Error(w, "invalid series or season", http.StatusBadRequest)
		return
	}
	seasons, err := store.ListSeasonsForSeries(r.Context(), h.deps.DB, seriesID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, s := range seasons {
		if s.SeasonNumber != season {
			continue
		}
		if err := store.UpdateSeasonMonitored(r.Context(), h.deps.DB, seriesID, season, !s.Monitored); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.renderPartial(w, "season_pass_chip", seasonPassChip{SeriesID: seriesID, SeasonPassSeason: store.SeasonPassSeason{
			Number: season, Monitored: !s.Monitored, Total: s.Total, Downloaded: s.Downloaded,
		}})
		return
	}
	http.NotFound(w, r)
}

// SeasonPassSave applies Season Pass's Monitored and Monitor choices to the
// ticked series.
func (h *handler) SeasonPassSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ids := selectedIDs(r)
	if len(ids) == 0 {
		renderInlineError(w, "Tick at least one series.")
		return
	}
	monitored, option := noChangeBool(r.FormValue("monitored")), r.FormValue("monitor")
	if monitored == nil && option == "" {
		renderInlineError(w, "Choose what to change: Monitored, Monitor or both.")
		return
	}
	ctx := r.Context()
	for _, id := range ids {
		if monitored != nil {
			if err := store.UpdateSeriesMonitored(ctx, h.deps.DB, id, *monitored); err != nil {
				renderInlineError(w, err.Error())
				return
			}
		}
		if option != "" {
			if err := store.ApplyMonitorOption(ctx, h.deps.DB, id, option, time.Now()); err != nil {
				renderInlineError(w, err.Error())
				return
			}
		}
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/tv/seasonpass?saved=%d", len(ids)))
	w.WriteHeader(http.StatusOK)
}

// availabilityLabel is how Radarr names a minimum availability.
func availabilityLabel(value string) string {
	for _, a := range store.MinimumAvailabilities {
		if a.Value == value {
			return a.Label
		}
	}
	if value == "tba" {
		return "TBA"
	}
	return value
}
