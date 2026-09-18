package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/decision"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

type seriesPageData struct {
	MonitorOptions  []store.MonitorOption
	SeriesTypes     []string
	Active          string
	Notice          string
	PageTitle       string
	Series          []seriesCard
	RootFolders     []store.RootFolder
	QualityProfiles []store.QualityProfile
	HasRootFolder   bool
	HasIndexer      bool
}

func (h *handler) Series(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	series, err := store.ListSeries(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rootFolders, err := store.ListRootFolders(ctx, h.deps.DB, "series")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	qualityProfiles, err := store.ListQualityProfilesOfKind(ctx, h.deps.DB, "series")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	profiles, err := decision.LoadProfiles(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	qualities, err := store.EpisodeFileQualitiesBySeries(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPage(w, "tv", seriesPageData{
		Active:          "tv",
		PageTitle:       "TV Series",
		Notice:          libraryNotice(r, "series", tvNotice(r)),
		MonitorOptions:  store.MonitorOptions,
		SeriesTypes:     store.SeriesTypes,
		Series:          seriesCards(series, profiles, qualities),
		RootFolders:     rootFolders,
		QualityProfiles: qualityProfiles,
		HasRootFolder:   len(rootFolders) > 0,
		HasIndexer:      h.deps.Indexer.Configured(ctx),
	})
}

type seriesSearchResultView struct {
	ID    int
	Title string
	Year  string
}

type seriesResultsData struct {
	Results         []seriesSearchResultView
	RootFolders     []store.RootFolder
	QualityProfiles []store.QualityProfile
	// Err, when non-empty, means the TMDB search itself failed - see
	// movieResultsData.Err in movies.go for why this matters.
	Err string
}

func (h *handler) SeriesSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query().Get("q")
	if query == "" {
		h.renderPartial(w, "series_results", seriesResultsData{})
		return
	}

	results, err := h.deps.TMDB.SearchSeries(ctx, query)
	if err != nil {
		h.renderPartial(w, "series_results", seriesResultsData{Err: err.Error()})
		return
	}
	rootFolders, err := store.ListRootFolders(ctx, h.deps.DB, "series")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	qualityProfiles, err := store.ListQualityProfilesOfKind(ctx, h.deps.DB, "series")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	views := make([]seriesSearchResultView, 0, len(results))
	for _, r := range results {
		year := ""
		if len(r.FirstAirDate) >= 4 {
			year = r.FirstAirDate[:4]
		}
		views = append(views, seriesSearchResultView{ID: r.ID, Title: r.Name, Year: year})
	}

	h.renderPartial(w, "series_results", seriesResultsData{
		Results:         views,
		RootFolders:     rootFolders,
		QualityProfiles: qualityProfiles,
	})
}

func (h *handler) SeriesAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tmdbID, err := strconv.Atoi(r.FormValue("tmdb_id"))
	if err != nil {
		http.Error(w, "invalid tmdb_id", http.StatusBadRequest)
		return
	}
	rootFolderID, err := strconv.ParseInt(r.FormValue("root_folder_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid root_folder_id", http.StatusBadRequest)
		return
	}
	qualityProfileID, err := strconv.ParseInt(r.FormValue("quality_profile_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid quality_profile_id", http.StatusBadRequest)
		return
	}

	seriesID, err := h.deps.Series.AddByTMDBID(r.Context(), tmdbID, rootFolderID, qualityProfileID)
	if err != nil {
		renderAddError(w, err)
		return
	}
	h.recordItemEvent(r, store.EventAdded, "series", 0, seriesID, 0, "Added from search", "interactive")
	if h.deps.Metadata != nil {
		go h.deps.Metadata.WriteSeries(context.Background(), seriesID)
	}

	w.Header().Set("HX-Redirect", "/tv")
	w.WriteHeader(http.StatusOK)
}

// episodeView is a template-friendly projection of store.EpisodeDetail.
// SeriesID/HasIndexer are constant per page but carried on every row (not
// just read from an outer template scope) so "episode_row" is a
// self-contained partial - both the full page render and the status-poll
// endpoint (SeriesEpisodeStatuses) use the exact same template with the
// exact same data shape.
type episodeView struct {
	ID            int64
	SeriesID      int64
	HasIndexer    bool
	EpisodeNumber int
	Title         string
	AirDate       string
	Monitored     bool
	HasFile       bool
	FileStatus    string
	SizeHuman     string
	SizeBytes     int64
	Quality       string
	ReleaseGroup  string
	MediaInfo     mediaInfoView
	MonitoredChip monitoredChipView
	// OOB marks this row for an htmx out-of-band swap (see
	// SeriesEpisodeStatuses) rather than the plain in-page render - it
	// replaces just the matching #episode-row-<ID> element wherever it
	// already is in the DOM, leaving everything else (collapsed/expanded
	// seasons, scroll position, an open per-episode search-results row)
	// untouched.
	OOB bool
}

// episodeViewsForSeries builds the template-ready view for each of
// episodes (already fetched by the caller - both SeriesDetail and
// SeriesEpisodeStatuses need their own copy anyway, one for a full page,
// one for a fresh status poll), in the same order they're given.
func episodeViewsForSeries(ctx context.Context, q store.Queryer, seriesID int64, episodes []store.EpisodeDetail, hasIndexer, oob bool) ([]episodeView, error) {
	grabStatuses, err := store.EpisodeGrabStatuses(ctx, q, seriesID)
	if err != nil {
		return nil, err
	}
	views := make([]episodeView, len(episodes))
	for i, e := range episodes {
		title := ""
		if e.Title.Valid {
			title = e.Title.String
		}
		airDate := ""
		if e.AirDate.Valid {
			airDate = e.AirDate.Time.Format("2006-01-02")
		}
		status := episodeStatus(e.HasFile, grabStatuses[e.ID], e.AirDate, time.Now())
		sizeHuman := ""
		if e.HasFile {
			sizeHuman = humanizeBytes(e.FileSize.Int64)
		}
		views[i] = episodeView{
			ID: e.ID, SeriesID: seriesID, HasIndexer: hasIndexer, EpisodeNumber: e.EpisodeNumber, Title: title, AirDate: airDate,
			Monitored: e.Monitored, HasFile: e.HasFile, FileStatus: status, SizeHuman: sizeHuman, SizeBytes: e.FileSize.Int64,
			Quality: e.Quality.String(), ReleaseGroup: e.Quality.ReleaseGroup, OOB: oob, MediaInfo: newMediaInfoView(e.MediaInfo),
			MonitoredChip: monitoredChipView{
				Monitored: e.Monitored, ToggleURL: fmt.Sprintf("/tv/%d/episodes/%d/monitored", seriesID, e.ID),
				LabelOn: "Monitored", LabelOff: "Unmonitored", Icon: true,
			},
		}
	}
	return views, nil
}

// SeriesEpisodeStatuses is a lightweight poll (GET /tv/{id}/episodes/status)
// the series detail page hits every few seconds so an episode's row
// (status/quality/size) updates itself live as a grab is picked up by the
// completion webhook or the fallback poller. Episodes should appear as
// they are picked up, rather than needing a page refresh, which collapses
// everything that was expanded. Renders
// every row via htmx out-of-band swaps (see episodeView.OOB /
// "episode_row"'s hx-swap-oob) instead of returning a full page or even a
// full episode table, so collapsed/expanded seasons, scroll position, and
// any open per-episode search-results row are all left alone - only rows
// whose actual content differs end up visually changing at all (htmx still
// swaps unchanged ones, but identical HTML is a no-op to the user).
func (h *handler) SeriesEpisodeStatuses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	seriesID, ok := pathID(w, r, "id", "series")
	if !ok {
		return
	}
	episodes, err := store.ListEpisodesForSeries(ctx, h.deps.DB, seriesID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	views, err := episodeViewsForSeries(ctx, h.deps.DB, seriesID, episodes, h.deps.Indexer.Configured(ctx), true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	seasonSummaries, err := store.ListSeasonsForSeries(ctx, h.deps.DB, seriesID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sizeBySeason := make(map[int]int64, len(seasonSummaries))
	viewsBySeason := make(map[int][]episodeView, len(seasonSummaries))
	for i, e := range episodes {
		viewsBySeason[e.SeasonNumber] = append(viewsBySeason[e.SeasonNumber], views[i])
		if views[i].HasFile {
			sizeBySeason[e.SeasonNumber] += views[i].SizeBytes
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	for _, v := range views {
		h.renderPartial(w, "episode_row", v)
	}
	// The season header's Downloaded/Total/size badge isn't part of any
	// one episode's row, so it needs its own oob swap alongside them -
	// otherwise it goes stale exactly like the whole page used to (see
	// SeriesEpisodeStatuses' doc comment): every row says Imported while
	// the header above them still reads whatever count was true on the
	// last full page load.
	for _, s := range seasonSummaries {
		h.renderPartial(w, "season_stats", seasonView{
			SeasonNumber: s.SeasonNumber, Total: s.Total, Downloaded: s.Downloaded,
			Complete: s.Total > 0 && s.Downloaded == s.Total, StatsClass: seasonStatsClass(viewsBySeason[s.SeasonNumber]),
			SizeHuman: humanizeBytes(sizeBySeason[s.SeasonNumber]), OOB: true,
		})
	}
}

// seasonView groups a series detail page's episodes under their season,
// with per-season counts/size for the season header row.
type seasonView struct {
	SeasonNumber  int
	Monitored     bool
	Total         int
	Downloaded    int
	Complete      bool   // Total > 0 && Downloaded == Total
	StatsClass    string // the count badge's colour: good, info (downloading) or warn
	SizeHuman     string
	Episodes      []episodeView
	MonitoredChip monitoredChipView
	// OOB marks this season's stats badge (Downloaded/Total, SizeHuman) for
	// an htmx out-of-band swap - see episodeView.OOB and
	// SeriesEpisodeStatuses, which "season_stats" is shared with.
	OOB bool
}

type seriesDetailPageData struct {
	Active             string
	PageTitle          string
	ScanNotice         string // what the last Refresh (folder scan) found
	Series             store.SeriesDetail
	Ratings            []ratingView
	Seasons            []seasonView
	QualityProfileName string
	HasIndexer         bool
	MonitoredChip      monitoredChipView
	SizeHuman          string
	YearRange          string
	Rating             string
	Links              []externalLink
}

// SeriesDetail is the series detail page (GET /tv/{id}) - poster/overview/
// genres/ratings/status like the movie page, plus a season-by-season
// episode list with per-episode file status. Per-episode search/grab is
// deliberately not built this pass (see the plan) - season-level and
// whole-series search/grab already exist and are surfaced here instead.
func (h *handler) SeriesDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	series, ok := h.seriesFromRef(w, r)
	if !ok {
		return
	}
	seriesID := series.ID

	seasonSummaries, err := store.ListSeasonsForSeries(ctx, h.deps.DB, seriesID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	episodes, err := store.ListEpisodesForSeries(ctx, h.deps.DB, seriesID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sizeOnDisk, _ := store.SeriesSizeOnDisk(ctx, h.deps.DB, seriesID)
	views, err := episodeViewsForSeries(ctx, h.deps.DB, seriesID, episodes, h.deps.Indexer.Configured(ctx), false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	episodesBySeason := make(map[int][]episodeView, len(seasonSummaries))
	sizeBySeason := make(map[int]int64, len(seasonSummaries))
	for i, e := range episodes {
		episodesBySeason[e.SeasonNumber] = append(episodesBySeason[e.SeasonNumber], views[i])
		if views[i].HasFile {
			sizeBySeason[e.SeasonNumber] += views[i].SizeBytes
		}
	}

	// Newest first - seasons and the episodes within them - as Sonarr lists
	// them; the page can flip the order.
	for _, eps := range episodesBySeason {
		for i, j := 0, len(eps)-1; i < j; i, j = i+1, j-1 {
			eps[i], eps[j] = eps[j], eps[i]
		}
	}
	seasons := make([]seasonView, 0, len(seasonSummaries))
	for i, j := 0, len(seasonSummaries)-1; i < j; i, j = i+1, j-1 {
		seasonSummaries[i], seasonSummaries[j] = seasonSummaries[j], seasonSummaries[i]
	}
	for _, s := range seasonSummaries {
		seasons = append(seasons, seasonView{
			SeasonNumber: s.SeasonNumber, Monitored: s.Monitored,
			Total: s.Total, Downloaded: s.Downloaded, Complete: s.Total > 0 && s.Downloaded == s.Total,
			StatsClass: seasonStatsClass(episodesBySeason[s.SeasonNumber]),
			SizeHuman:  humanizeBytes(sizeBySeason[s.SeasonNumber]),
			Episodes:   episodesBySeason[s.SeasonNumber],
			MonitoredChip: monitoredChipView{
				Monitored: s.Monitored, ToggleURL: fmt.Sprintf("/tv/%d/seasons/%d/monitored", seriesID, s.SeasonNumber),
				LabelOn: "Monitored", LabelOff: "Unmonitored", Icon: true,
			},
		})
	}

	h.renderPage(w, "series_detail", seriesDetailPageData{
		Active: "tv", PageTitle: series.Title,
		ScanNotice: scanNotice(r),
		SizeHuman:  humanizeBytes(sizeOnDisk), YearRange: yearRange(series), Rating: ratingPercent(toRatingViews(series.Ratings)), Links: h.seriesLinks(r, series.MetadataID),
		Series: series, Ratings: toRatingViews(series.Ratings), Seasons: seasons,
		QualityProfileName: series.QualityProfileName.String,
		HasIndexer:         h.deps.Indexer.Configured(ctx),
		MonitoredChip: monitoredChipView{
			Monitored: series.Monitored, ToggleURL: fmt.Sprintf("/tv/%d/monitored", seriesID),
			LabelOn: "Monitored", LabelOff: "Unmonitored",
		},
	})
}

// SeriesMonitoredToggle flips seriesID's monitored flag and re-renders
// the clickable chip in place - see MovieMonitoredToggle.
func (h *handler) SeriesMonitoredToggle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	seriesID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid series id", http.StatusBadRequest)
		return
	}
	series, found, err := store.GetSeriesDetail(ctx, h.deps.DB, seriesID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	newState := !series.Monitored
	if err := store.UpdateSeriesMonitored(ctx, h.deps.DB, seriesID, newState); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderMonitoredChip(w, monitoredChipView{
		Monitored: newState, ToggleURL: fmt.Sprintf("/tv/%d/monitored", seriesID),
		LabelOn: "Monitored", LabelOff: "Unmonitored",
	})
}

// SeasonMonitoredToggle flips one season's monitored flag, identified by
// (seriesID, seasonNumber) - seasons have no store-level single-argument
// lookup, so the new state is derived from the CURRENT form value the
// chip itself POSTs back (see monitored_chip.html - a toggle always
// flips from whatever it's currently rendering) rather than re-reading
// from the DB first.
func (h *handler) SeasonMonitoredToggle(w http.ResponseWriter, r *http.Request) {
	seriesID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid series id", http.StatusBadRequest)
		return
	}
	seasonNumber, err := strconv.Atoi(r.PathValue("season"))
	if err != nil {
		http.Error(w, "invalid season number", http.StatusBadRequest)
		return
	}

	seasons, err := store.ListSeasonsForSeries(r.Context(), h.deps.DB, seriesID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var current store.SeasonSummary
	found := false
	for _, s := range seasons {
		if s.SeasonNumber == seasonNumber {
			current, found = s, true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	newState := !current.Monitored
	if err := store.UpdateSeasonMonitored(r.Context(), h.deps.DB, seriesID, seasonNumber, newState); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderMonitoredChip(w, monitoredChipView{
		Monitored: newState, ToggleURL: fmt.Sprintf("/tv/%d/seasons/%d/monitored", seriesID, seasonNumber),
		LabelOn: "Monitored", LabelOff: "Unmonitored", Icon: true,
	})
}

// EpisodeMonitoredToggle flips one episode's monitored flag.
func (h *handler) EpisodeMonitoredToggle(w http.ResponseWriter, r *http.Request) {
	seriesID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid series id", http.StatusBadRequest)
		return
	}
	episodeID, err := strconv.ParseInt(r.PathValue("episodeId"), 10, 64)
	if err != nil {
		http.Error(w, "invalid episode id", http.StatusBadRequest)
		return
	}

	var monitored bool
	err = h.deps.DB.QueryRowContext(r.Context(), `SELECT monitored FROM episodes WHERE id = ? AND series_id = ?`, episodeID, seriesID).Scan(&monitored)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	newState := !monitored
	if err := store.UpdateEpisodeMonitored(r.Context(), h.deps.DB, episodeID, newState); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderMonitoredChip(w, monitoredChipView{
		Monitored: newState, ToggleURL: fmt.Sprintf("/tv/%d/episodes/%d/monitored", seriesID, episodeID),
		LabelOn: "Monitored", LabelOff: "Unmonitored", Icon: true,
	})
}

// episodeStatus is where an episode is in the pipeline, as the Status
// column shows it: Missing, Downloading (grabbed, in the download client),
// Needs extraction, Import failed, or Imported (in the library).
// episodeStatus is what an episode's Status cell says. An episode whose air
// date is still ahead isn't missing, it's Unreleased. An episode with no air
// date at all stays Missing - that is usually metadata that never filled in,
// not a future episode.
func episodeStatus(hasFile bool, grabStatus string, airDate sql.NullTime, now time.Time) string {
	if hasFile {
		return "Imported"
	}
	switch grabStatus {
	case "grabbed", "downloading":
		return "Downloading"
	case "needs_extraction":
		return "Needs extraction"
	case "import_failed":
		return "Import failed"
	}
	if airDate.Valid && airDate.Time.After(now) {
		return "Unreleased"
	}
	return "Missing"
}

// seasonStatsClass colours a season's count badge by what is actually
// outstanding: green when nothing is missing (episodes that haven't aired
// don't count against it), the Downloading colour when the only gap is a
// download in flight, and the warning colour when something aired and
// isn't here.
func seasonStatsClass(episodes []episodeView) string {
	missing, downloading := 0, 0
	for _, e := range episodes {
		switch {
		case e.HasFile, e.FileStatus == "Unreleased":
		case e.FileStatus == "Downloading":
			downloading++
		default:
			missing++
		}
	}
	switch {
	case missing > 0:
		return "chip-warn"
	case downloading > 0:
		return "chip-info"
	}
	return "chip-good"
}
