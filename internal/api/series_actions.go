package api

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// externalLink is one entry in the header's Links menu.
type externalLink struct {
	Label string
	URL   string
}

// seriesLinks builds the Links menu from the ids the metadata providers gave
// the series, the way Sonarr's header does.
func (h *handler) seriesLinks(r *http.Request, metadataID int64) []externalLink {
	id := func(provider string) string {
		v, _, _ := store.GetExternalID(r.Context(), h.deps.DB, "series", metadataID, provider)
		return v
	}
	var links []externalLink
	if v := id("tvdb"); v != "" {
		links = append(links, externalLink{"The TVDB", "https://thetvdb.com/dereferrer/series/" + v}, externalLink{"Trakt", "https://trakt.tv/search/tvdb/" + v + "?id_type=show"})
	}
	if v := id("tvmaze"); v != "" {
		links = append(links, externalLink{"TV Maze", "https://www.tvmaze.com/shows/" + v})
	}
	if v := id("imdb"); v != "" {
		links = append(links, externalLink{"IMDb", "https://www.imdb.com/title/" + v + "/"})
	}
	if v := id("tmdb"); v != "" {
		links = append(links, externalLink{"TMDb", "https://www.themoviedb.org/tv/" + v})
	}
	return links
}

// yearRange is Sonarr's "2021-2022" from first and last air dates.
func yearRange(d store.SeriesDetail) string {
	if !d.FirstAired.Valid {
		if d.Year.Valid {
			return strconv.FormatInt(d.Year.Int64, 10)
		}
		return ""
	}
	first := d.FirstAired.Time.Year()
	if d.StatusKey() == store.SeriesEnded && d.LastAired.Valid && d.LastAired.Time.Year() != first {
		return fmt.Sprintf("%d-%d", first, d.LastAired.Time.Year())
	}
	if d.StatusKey() != store.SeriesEnded {
		return fmt.Sprintf("%d-", first)
	}
	return strconv.Itoa(first)
}

// ratingPercent picks the first rating and shows it Sonarr's way, as a percentage.
func ratingPercent(ratings []ratingView) string {
	for _, r := range ratings {
		if r.Value > 0 {
			if r.Value <= 10 {
				return fmt.Sprintf("%d%%", int(r.Value*10+0.5))
			}
			return fmt.Sprintf("%d%%", int(r.Value+0.5))
		}
	}
	return ""
}

type renamePreviewData struct {
	SeriesID   int64
	SeriesPath string
	Pattern    string
	Items      []sync.RenameItem
	Err        string
}

// SeriesRenamePreview is Sonarr's Organize & Rename: what would change.
func (h *handler) SeriesRenamePreview(w http.ResponseWriter, r *http.Request) {
	seriesID, ok := pathID(w, r, "id", "series")
	if !ok {
		return
	}
	data := renamePreviewData{SeriesID: seriesID}
	naming, err := store.GetNamingConfig(r.Context(), h.deps.DB, "series")
	if err == nil {
		data.Pattern = strings.TrimSuffix(naming.SeasonFolderFormat.String, "/") + "/" + naming.EpisodeFileFormat.String
	}
	path, items, err := h.deps.Import.RenamePreview(r.Context(), seriesID)
	if err != nil {
		data.Err = err.Error()
	}
	data.SeriesPath, data.Items = path, items
	h.renderPartial(w, "rename_preview", data)
}

// SeriesRenameFiles is Sonarr's Organize button: rename the ticked files.
func (h *handler) SeriesRenameFiles(w http.ResponseWriter, r *http.Request) {
	seriesID, ok := pathID(w, r, "id", "series")
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
	renamed, problems, err := h.deps.Import.RenameFiles(r.Context(), seriesID, ids)
	if err != nil {
		renderInlineError(w, err.Error())
		return
	}
	if len(problems) > 0 {
		renderInlineError(w, fmt.Sprintf("%d renamed. Couldn't rename: %s", renamed, strings.Join(problems, "; ")))
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/tv/%d?renamed=%d", seriesID, renamed))
	w.WriteHeader(http.StatusOK)
}

// SeriesDelete removes the series and its files, as Sonarr's Delete with
// "delete files" does. If the folder isn't inside a library folder, the

// tvNotice is what the TV page says after a redirect with ?notice=.
func tvNotice(r *http.Request) string {
	if r.URL.Query().Get("notice") == "files-kept" {
		return "The series was removed from UMMarr, but its folder isn't inside a library folder, so its files were left alone."
	}
	return ""
}

// EpisodeDetails opens an episode's dialog on its Details tab, as clicking
// an episode in Sonarr does. The Search tab loads the search on demand.
func (h *handler) EpisodeDetails(w http.ResponseWriter, r *http.Request) {
	seriesID, ok := pathID(w, r, "id", "series")
	if !ok {
		return
	}
	episodeID, ok := pathID(w, r, "episodeId", "episode")
	if !ok {
		return
	}
	info, found, err := store.GetEpisodeInfo(r.Context(), h.deps.DB, episodeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found || info.SeriesID != seriesID {
		http.NotFound(w, r)
		return
	}
	statuses, _ := store.EpisodeGrabStatuses(r.Context(), h.deps.DB, seriesID)
	dialog := h.episodeInfoDialog(r.Context(), info, statuses[episodeID] != "")
	dialog.ActiveTab = "details"
	dialog.SearchURL = fmt.Sprintf("/tv/%d/episodes/%d/releases", seriesID, episodeID)
	h.renderPartial(w, "release_dialog", releaseResultsData{dialogInfo: dialog})
}

// SeasonEpisodeNames looks up names for one season's episodes, or the whole
// series when no season is given: Settings → Metadata's providers are asked
// in order, and only names, summaries, air dates and runtimes change.
func (h *handler) SeasonEpisodeNames(w http.ResponseWriter, r *http.Request) {
	series, ok := h.seriesNoRedirect(w, r)
	if !ok {
		return
	}
	if h.deps.Series == nil {
		renderInlineError(w, "TMDB isn't configured, so names can't be looked up.")
		return
	}
	var season *int
	if raw := r.PathValue("season"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			http.Error(w, "invalid season", http.StatusBadRequest)
			return
		}
		season = &n
	}
	report, err := h.deps.Series.SearchEpisodeNames(r.Context(), series.ID, season)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err != nil {
		fmt.Fprintf(w, `<p class="indexer-test failed">%s</p>`, html.EscapeString(err.Error()))
		return
	}
	fmt.Fprintf(w, `<p class="indexer-test">%s</p>`, html.EscapeString(report.Summary()))
}
