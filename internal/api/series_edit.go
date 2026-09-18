package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
	"github.com/Optimus-Perky/UMMarr/internal/titleutil"
)

// The Sonarr series-page toolbar's dialogs: Edit, Series Monitoring,
// Manage Episodes and Delete, plus Refresh & Scan.

type seriesEditData struct {
	Series          store.SeriesDetail
	Settings        store.SeriesSettings
	Tags            string
	QualityProfiles []store.QualityProfile
	SeriesTypes     []string
	Error           string
}

// SeriesEditForm renders the Edit dialog.
func (h *handler) SeriesEditForm(w http.ResponseWriter, r *http.Request) {
	series, ok := h.seriesNoRedirect(w, r)
	if !ok {
		return
	}
	h.renderSeriesEdit(w, r, series, "")
}

func (h *handler) renderSeriesEdit(w http.ResponseWriter, r *http.Request, series store.SeriesDetail, errMsg string) {
	ctx := r.Context()
	settings, err := store.GetSeriesSettings(ctx, h.deps.DB, series.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	profiles, _ := store.ListQualityProfilesOfKind(ctx, h.deps.DB, "series")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	h.renderPartial(w, "series_edit", seriesEditData{Series: series, Settings: settings, Tags: strings.Join(settings.Tags, ", "), QualityProfiles: profiles, SeriesTypes: store.SeriesTypes, Error: errMsg})
}

// SeriesEditSave saves the Edit dialog and reloads the page.
func (h *handler) SeriesEditSave(w http.ResponseWriter, r *http.Request) {
	series, ok := h.seriesNoRedirect(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	profileID, _ := strconv.ParseInt(r.FormValue("quality_profile_id"), 10, 64)
	path := strings.TrimSpace(r.FormValue("path"))
	settings := store.SeriesSettings{
		Monitored: r.FormValue("monitored") == "on", MonitorNewItems: r.FormValue("monitor_new_items"), SeasonFolder: r.FormValue("season_folder") == "on",
		QualityProfileID: profileID, SeriesType: r.FormValue("series_type"), Path: series.Path.String, Tags: strings.Split(r.FormValue("tags"), ","),
	}
	if profileID == 0 {
		h.renderSeriesEdit(w, r, series, "Pick a quality profile.")
		return
	}
	if path == "" || !filepath.IsAbs(path) {
		h.renderSeriesEdit(w, r, series, "The path must be an absolute folder, e.g. /data/TV/Current/2 Broke Girls.")
		return
	}
	if filepath.Clean(path) != filepath.Clean(series.Path.String) {
		if err := h.deps.Import.MoveSeries(r.Context(), series.ID, path, r.FormValue("move_files") == "on"); err != nil {
			h.renderSeriesEdit(w, r, series, err.Error())
			return
		}
	}
	settings.Path = filepath.Clean(path)
	if err := store.UpdateSeriesSettings(r.Context(), h.deps.DB, series.ID, settings); err != nil {
		h.renderSeriesEdit(w, r, series, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", seriesPage(series))
	w.WriteHeader(http.StatusOK)
}

type seriesMonitorData struct {
	Series  store.SeriesDetail
	Options []store.MonitorOption
}

// SeriesMonitorForm renders the Series Monitoring dialog.
func (h *handler) SeriesMonitorForm(w http.ResponseWriter, r *http.Request) {
	series, ok := h.seriesNoRedirect(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	h.renderPartial(w, "series_monitor", seriesMonitorData{Series: series, Options: store.MonitorOptions})
}

// SeriesMonitorApply applies a monitoring option to every season and episode.
func (h *handler) SeriesMonitorApply(w http.ResponseWriter, r *http.Request) {
	series, ok := h.seriesNoRedirect(w, r)
	if !ok {
		return
	}
	if err := store.ApplyMonitorOption(r.Context(), h.deps.DB, series.ID, r.FormValue("monitor"), time.Now()); err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", seriesPage(series))
	w.WriteHeader(http.StatusOK)
}

type manageEpisodesData struct {
	Series       store.SeriesDetail
	Files        []episodeFileView
	Qualities    []string
	ReleaseTypes []struct{ Value, Label string }
	EpisodesJSON template.JS // {"1":[{"n":1,"t":"Pilot"}], ...} for the Select Season / Episode(s) choosers
}

type episodeFileView struct {
	store.EpisodeFileDetail
	SizeHuman string
	Episodes  string // "1,2" for the form
}

// SeriesManageEpisodesForm renders Manage Episodes: Sonarr's episode file
// editor - the series' files with a checkbox each, bulk edits from the
// Select menu, applied by Import.
func (h *handler) SeriesManageEpisodesForm(w http.ResponseWriter, r *http.Request) {
	series, ok := h.seriesNoRedirect(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	files, err := store.ListEpisodeFileDetails(ctx, h.deps.DB, series.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	views := make([]episodeFileView, 0, len(files))
	for _, f := range files {
		nums := make([]string, 0, len(f.EpisodeNumbers))
		for _, n := range f.EpisodeNumbers {
			nums = append(nums, strconv.Itoa(n))
		}
		views = append(views, episodeFileView{EpisodeFileDetail: f, SizeHuman: humanizeBytes(f.Size), Episodes: strings.Join(nums, ",")})
	}
	episodes, _ := store.ListEpisodesForSeries(ctx, h.deps.DB, series.ID)
	type ep struct {
		N int    `json:"n"`
		T string `json:"t"`
	}
	bySeason := map[string][]ep{}
	for _, e := range episodes {
		k := strconv.Itoa(e.SeasonNumber)
		bySeason[k] = append(bySeason[k], ep{e.EpisodeNumber, e.Title.String})
	}
	encoded, _ := json.Marshal(bySeason)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	h.renderPartial(w, "series_manage_episodes", manageEpisodesData{Series: series, Files: views, Qualities: releaseparse.AllQualities, ReleaseTypes: store.ReleaseTypes, EpisodesJSON: template.JS(encoded)})
}

// SeriesManageEpisodesImport applies the dialog's pending edits: per file
// f<id>_quality, f<id>_group, f<id>_languages, f<id>_release_type, and
// f<id>_season + f<id>_episodes to re-map it.
func (h *handler) SeriesManageEpisodesImport(w http.ResponseWriter, r *http.Request) {
	series, ok := h.seriesNoRedirect(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	files, err := store.ListEpisodeFileDetails(ctx, h.deps.DB, series.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	changed := 0
	err = store.WithTx(ctx, h.deps.DB, func(tx *sql.Tx) error {
		for _, f := range files {
			prefix := "f" + strconv.FormatInt(f.ID, 10) + "_"
			field := func(name string) *string {
				if v, ok := r.Form[prefix+name]; ok && len(v) > 0 {
					return &v[0]
				}
				return nil
			}
			edit := store.EpisodeFileEdit{Quality: field("quality"), ReleaseGroup: field("group"), Languages: field("languages"), ReleaseType: field("release_type")}
			touched := edit.Quality != nil || edit.ReleaseGroup != nil || edit.Languages != nil || edit.ReleaseType != nil
			if touched {
				if err := store.UpdateEpisodeFiles(ctx, tx, f.FileIDs, edit); err != nil {
					return fmt.Errorf("%s: %w", filepath.Base(f.RelativePath), err)
				}
			}
			season, episodes := field("season"), field("episodes")
			if season != nil || episodes != nil {
				seasonNumber := f.SeasonNumber
				if season != nil {
					seasonNumber, _ = strconv.Atoi(*season)
				}
				numbers := f.EpisodeNumbers
				if episodes != nil {
					numbers = nil
					for _, p := range strings.Split(*episodes, ",") {
						if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
							numbers = append(numbers, n)
						}
					}
				}
				if err := store.RemapEpisodeFile(ctx, tx, series.ID, f.FileIDs, seasonNumber, numbers); err != nil {
					return fmt.Errorf("%s: %w", filepath.Base(f.RelativePath), err)
				}
				touched = true
			}
			if touched {
				changed++
			}
		}
		return nil
	})
	if err != nil {
		renderInlineError(w, err.Error())
		return
	}
	if changed > 0 && h.deps.Events != nil {
		h.deps.Events.Record(ctx, store.Event{Event: store.EventRenamed, MediaType: "series", SeriesID: sql.NullInt64{Int64: series.ID, Valid: true}, Title: series.Title, Detail: fmt.Sprintf("%d file(s) edited in Manage Episodes", changed), Source: "manage episodes"})
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("%s?edited=%d", seriesPage(series), changed))
	w.WriteHeader(http.StatusOK)
}

// SeriesDeleteEpisodeFiles deletes the ticked files from disk and the library.
func (h *handler) SeriesDeleteEpisodeFiles(w http.ResponseWriter, r *http.Request) {
	series, ok := h.seriesNoRedirect(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var ids []int64
	for _, v := range r.Form["file_id"] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		renderInlineError(w, "Tick at least one file.")
		return
	}
	removed, err := h.deps.Import.DeleteEpisodeFiles(r.Context(), series.ID, ids)
	if err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("%s?deleted=%d", seriesPage(series), removed))
	w.WriteHeader(http.StatusOK)
}

// SeriesDeleteForm renders the Delete dialog (delete files, add exclusion).
func (h *handler) SeriesDeleteForm(w http.ResponseWriter, r *http.Request) {
	series, ok := h.seriesNoRedirect(w, r)
	if !ok {
		return
	}
	files, _ := store.ListEpisodeFileDetails(r.Context(), h.deps.DB, series.ID)
	size, _ := store.SeriesSizeOnDisk(r.Context(), h.deps.DB, series.ID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	h.renderPartial(w, "series_delete", struct {
		Series    store.SeriesDetail
		FileCount int
		SizeHuman string
	}{series, len(files), humanizeBytes(size)})
}

// SeriesDelete removes the series; with delete_files its folder goes too,
// with add_exclusion it can't come back through an import list.
func (h *handler) SeriesDelete(w http.ResponseWriter, r *http.Request) {
	seriesID, ok := pathID(w, r, "id", "series")
	if !ok {
		return
	}
	_ = r.ParseForm()
	if r.FormValue("add_exclusion") == "on" {
		h.excludeItem(r, "series", seriesID)
	}
	err := h.deps.Import.DeleteSeries(r.Context(), seriesID, r.FormValue("delete_files") == "on")
	if errors.Is(err, sync.ErrUnsafeDelete) {
		w.Header().Set("HX-Redirect", "/tv?notice=files-kept")
		w.WriteHeader(http.StatusOK)
		return
	}
	if err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", "/tv")
	w.WriteHeader(http.StatusOK)
}

// SeriesRefresh is Sonarr's Refresh & Scan: re-fetch the series from TMDB
// (new episodes, posters), then rescan its folder for files.
func (h *handler) SeriesRefresh(w http.ResponseWriter, r *http.Request) {
	seriesID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid series id", http.StatusBadRequest)
		return
	}
	refreshed := ""
	if h.deps.Series != nil {
		refreshed = "1"
		if err := h.deps.Series.Refresh(r.Context(), seriesID); err != nil {
			refreshed = "0"
		}
	}
	imported, err := h.deps.Import.ScanSeries(r.Context(), seriesID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.deps.MediaInfo.Reanalyze(r.Context(), "series", seriesID)
	redirect := "/tv/" + r.PathValue("id") + "?scanned=" + strconv.Itoa(imported)
	if refreshed != "" {
		redirect += "&refreshed=" + refreshed
	}
	w.Header().Set("HX-Redirect", redirect)
	w.WriteHeader(http.StatusOK)
}

// seriesNoRedirect resolves {id} (number or slug) for the toolbar's
// dialogs and saves, which must not bounce to the title address the way the
// page itself does.
func (h *handler) seriesNoRedirect(w http.ResponseWriter, r *http.Request) (store.SeriesDetail, bool) {
	ref := r.PathValue("id")
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		var found bool
		if id, found, err = store.FindSeriesIDBySlug(r.Context(), h.deps.DB, ref); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return store.SeriesDetail{}, false
		} else if !found {
			http.NotFound(w, r)
			return store.SeriesDetail{}, false
		}
	}
	series, found, err := store.GetSeriesDetail(r.Context(), h.deps.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return store.SeriesDetail{}, false
	}
	if !found {
		http.NotFound(w, r)
		return store.SeriesDetail{}, false
	}
	return series, true
}

// seriesPage is the series' title address.
func seriesPage(series store.SeriesDetail) string { return "/tv/" + titleutil.Slug(series.Title) }
