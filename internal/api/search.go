package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

var errSearchUnavailable = errors.New("automatic search isn't available")

type searchReportView struct {
	Err     string
	Report  sync.SearchReport
	Reasons string
}

func (h *handler) renderSearchReport(w http.ResponseWriter, report sync.SearchReport, err error) {
	view := searchReportView{Report: report, Reasons: strings.Join(report.TopRejections, "; ")}
	if err != nil {
		view.Err = err.Error()
	}
	h.renderPartial(w, "search_report", view)
}

// MovieAutoSearch is Radarr's Search Movie: UMMarr searches, and grabs the best
// release it would accept.
func (h *handler) MovieAutoSearch(w http.ResponseWriter, r *http.Request) {
	movieID, ok := pathID(w, r, "id", "movie")
	if !ok {
		return
	}
	if h.deps.Search == nil {
		h.renderSearchReport(w, sync.SearchReport{}, errSearchUnavailable)
		return
	}
	report, err := h.deps.Search.SearchMovie(r.Context(), movieID)
	h.renderSearchReport(w, report, err)
}

// SeriesAutoSearch searches every season with missing monitored episodes.
func (h *handler) SeriesAutoSearch(w http.ResponseWriter, r *http.Request) {
	seriesID, ok := pathID(w, r, "id", "series")
	if !ok {
		return
	}
	if h.deps.Search == nil {
		h.renderSearchReport(w, sync.SearchReport{}, errSearchUnavailable)
		return
	}
	report, err := h.deps.Search.SearchSeries(r.Context(), seriesID, nil)
	h.renderSearchReport(w, report, err)
}

// SeasonAutoSearch searches one season.
func (h *handler) SeasonAutoSearch(w http.ResponseWriter, r *http.Request) {
	seriesID, ok := pathID(w, r, "id", "series")
	if !ok {
		return
	}
	season, err := strconv.Atoi(r.PathValue("season"))
	if err != nil {
		http.Error(w, "invalid season", http.StatusBadRequest)
		return
	}
	if h.deps.Search == nil {
		h.renderSearchReport(w, sync.SearchReport{}, errSearchUnavailable)
		return
	}
	report, err := h.deps.Search.SearchSeries(r.Context(), seriesID, &season)
	h.renderSearchReport(w, report, err)
}

// EpisodeAutoSearch searches one episode.
func (h *handler) EpisodeAutoSearch(w http.ResponseWriter, r *http.Request) {
	seriesID, ok := pathID(w, r, "id", "series")
	if !ok {
		return
	}
	episodeID, ok := pathID(w, r, "episodeId", "episode")
	if !ok {
		return
	}
	if h.deps.Search == nil {
		h.renderSearchReport(w, sync.SearchReport{}, errSearchUnavailable)
		return
	}
	report, err := h.deps.Search.SearchEpisode(r.Context(), seriesID, episodeID)
	h.renderSearchReport(w, report, err)
}

// AlbumAutoSearch is Lidarr's album search.
func (h *handler) AlbumAutoSearch(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	if h.deps.Search == nil {
		h.renderSearchReport(w, sync.SearchReport{}, errSearchUnavailable)
		return
	}
	report, err := h.deps.Search.SearchAlbum(r.Context(), albumID)
	h.renderSearchReport(w, report, err)
}

type missingSearchView struct {
	sync.MissingSearch
	Path      string // "movies", "tv" or "music"
	Action    string // "search-missing" or "search-cutoff": the status URL's last segment
	Available bool
	Started   bool // this request started it
}

// missingMediaType maps the page a Search all missing button is on to its
// media type.
func missingMediaType(r *http.Request) (mediaType, path string) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/tv/"):
		return newznab.MediaSeries, "tv"
	case strings.HasPrefix(r.URL.Path, "/music/"):
		return newznab.MediaMusic, "music"
	}
	return newznab.MediaMovie, "movies"
}

func (h *handler) missingSearchView(r *http.Request) missingSearchView {
	mediaType, path := missingMediaType(r)
	mode := searchMode(r)
	view := missingSearchView{MissingSearch: sync.MissingSearch{MediaType: mediaType, Mode: mode}, Path: path, Action: "search-" + mode, Available: h.deps.Search != nil}
	if h.deps.Search != nil {
		view.MissingSearch = h.deps.Search.MissingSearchStatus(mediaType, mode)
	}
	return view
}

// StartMissingSearch starts Search all missing for the page's media type.
func (h *handler) StartMissingSearch(w http.ResponseWriter, r *http.Request) {
	mediaType, _ := missingMediaType(r)
	started := false
	if h.deps.Search != nil {
		started = h.deps.Search.StartMissingSearch(mediaType, searchMode(r))
	}
	view := h.missingSearchView(r)
	view.Started = started
	h.renderPartial(w, "missing_search_status", view)
}

// MissingSearchStatus is polled while Search all missing runs.
func (h *handler) MissingSearchStatus(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, "missing_search_status", h.missingSearchView(r))
}

// RunRSSSync runs RSS sync now, as Radarr's System -> Tasks -> RSS Sync does.
func (h *handler) RunRSSSync(w http.ResponseWriter, r *http.Request) {
	if h.deps.Search == nil {
		h.renderSearchReport(w, sync.SearchReport{}, errSearchUnavailable)
		return
	}
	settings, err := store.GetIndexerSettings(r.Context(), h.deps.DB)
	if err == nil && settings.RSSSyncInterval <= 0 {
		err = errors.New("RSS sync is off - set an RSS Sync Interval first")
	}
	if err != nil {
		h.renderSearchReport(w, sync.SearchReport{}, err)
		return
	}
	report, err := h.deps.Search.RSSSync(r.Context())
	h.renderSearchReport(w, report, err)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// scanNotice is what a detail page says after Refresh (folder scan) sends
// it back with ?scanned=N: how many files were picked up, or that the
// folder was checked and nothing new was found.
func scanNotice(r *http.Request) string {
	if v := r.URL.Query().Get("renamed"); v != "" {
		n, _ := strconv.Atoi(v)
		if n == 1 {
			return "Organized: 1 file renamed."
		}
		return fmt.Sprintf("Organized: %d files renamed.", n)
	}
	if v := r.URL.Query().Get("edited"); v != "" {
		if v == "1" {
			return "1 file edited."
		}
		return v + " files edited."
	}
	if v := r.URL.Query().Get("deleted"); v != "" {
		if v == "1" {
			return "1 file deleted."
		}
		return v + " files deleted."
	}
	prefix := ""
	switch r.URL.Query().Get("refreshed") {
	case "1":
		prefix = "Details refreshed from TMDB. "
	case "0":
		prefix = "Details couldn't be refreshed from TMDB (no TMDB match - use Fix Match). "
	}
	v := r.URL.Query().Get("scanned")
	if v == "" {
		return ""
	}
	n, err := strconv.Atoi(v)
	switch {
	case err != nil:
		return ""
	case n == 0:
		return prefix + "Folder scanned: nothing new found. Files already tracked were checked, renamed to match the templates if needed, and any that had gone were cleared."
	case n == 1:
		return prefix + "Folder scanned: 1 file picked up."
	default:
		return prefix + fmt.Sprintf("Folder scanned: %d files picked up.", n)
	}
}

// searchMode tells Search all missing from Search cutoff unmet by the route.
func searchMode(r *http.Request) string {
	if strings.HasSuffix(r.URL.Path, "/search-cutoff") {
		return sync.SearchCutoff
	}
	return sync.SearchMissing
}
