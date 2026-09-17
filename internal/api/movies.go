package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/decision"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

type moviesPageData struct {
	Notice                string
	MinimumAvailabilities []struct{ Value, Label string }
	Active                string
	PageTitle             string
	Movies                []movieCard
	RootFolders           []store.RootFolder
	QualityProfiles       []store.QualityProfile
	HasRootFolder         bool
	HasIndexer            bool
}

func (h *handler) Movies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	movies, err := store.ListMovies(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rootFolders, err := store.ListRootFolders(ctx, h.deps.DB, "movie")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	qualityProfiles, err := store.ListQualityProfiles(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	profiles, err := decision.LoadProfiles(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPage(w, "movies", moviesPageData{
		Active:                "movies",
		PageTitle:             "Movies",
		Movies:                movieCards(movies, time.Now(), profiles),
		RootFolders:           rootFolders,
		QualityProfiles:       qualityProfiles,
		HasRootFolder:         len(rootFolders) > 0,
		HasIndexer:            h.deps.Indexer.Configured(ctx),
		Notice:                libraryNotice(r, "movie", ""),
		MinimumAvailabilities: store.MinimumAvailabilities,
	})
}

// movieSearchResultView is a template-friendly projection of
// tmdb.MovieSearchResult - the template shouldn't need to parse dates.
type movieSearchResultView struct {
	ID    int
	Title string
	Year  string
}

type movieResultsData struct {
	Results         []movieSearchResultView
	RootFolders     []store.RootFolder
	QualityProfiles []store.QualityProfile
	// Err, when non-empty, means the TMDB search itself failed - rendered
	// distinctly from a genuine zero-result search (see
	// partials/movie_results.html) so a real failure (bad/missing token,
	// TMDB unreachable, ...) doesn't silently look like "nothing matched."
	Err string
}

func (h *handler) MovieSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query().Get("q")
	if query == "" {
		h.renderPartial(w, "movie_results", movieResultsData{})
		return
	}

	results, err := h.deps.TMDB.SearchMovies(ctx, query, 0)
	if err != nil {
		h.renderPartial(w, "movie_results", movieResultsData{Err: err.Error()})
		return
	}
	rootFolders, err := store.ListRootFolders(ctx, h.deps.DB, "movie")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	qualityProfiles, err := store.ListQualityProfiles(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	views := make([]movieSearchResultView, 0, len(results))
	for _, r := range results {
		year := ""
		if len(r.ReleaseDate) >= 4 {
			year = r.ReleaseDate[:4]
		}
		views = append(views, movieSearchResultView{ID: r.ID, Title: r.Title, Year: year})
	}

	h.renderPartial(w, "movie_results", movieResultsData{
		Results:         views,
		RootFolders:     rootFolders,
		QualityProfiles: qualityProfiles,
	})
}

func (h *handler) MovieAdd(w http.ResponseWriter, r *http.Request) {
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

	movieID, err := h.deps.Movies.AddByTMDBID(r.Context(), tmdbID, rootFolderID, qualityProfileID)
	if err != nil {
		renderAddError(w, err)
		return
	}
	h.recordItemEvent(r, store.EventAdded, "movie", movieID, 0, 0, "Added from search", "interactive")
	if h.deps.Metadata != nil {
		go h.deps.Metadata.WriteMovie(context.Background(), movieID)
	}

	w.Header().Set("HX-Redirect", "/movies")
	w.WriteHeader(http.StatusOK)
}

// movieFileView is a template-friendly projection of store.MovieFileInfo.
type movieFileView struct {
	RelativePath string
	SizeHuman    string
	DateAdded    string
	Quality      string
	ReleaseGroup string
	MediaInfo    mediaInfoView
	Languages    string
}

type movieDetailPageData struct {
	Active             string
	PageTitle          string
	ScanNotice         string // what the last Refresh (folder scan) found
	Movie              store.MovieDetail
	Ratings            []ratingView
	File               *movieFileView
	QualityProfileName string
	HasIndexer         bool
	MonitoredChip      monitoredChipView
}

// MovieDetail is the movie detail page (GET /movies/{id}) - poster,
// overview, genres/ratings/status, and (via the embedded release-search
// button/target div, unchanged from the old inline card expansion) a
// "Find release" section. See the plan for why this replaces the old
// per-card inline expansion.
func (h *handler) MovieDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	movie, ok := h.movieFromRef(w, r)
	if !ok {
		return
	}

	var file *movieFileView
	if movie.File != nil {
		file = &movieFileView{
			RelativePath: movie.File.RelativePath,
			SizeHuman:    humanizeBytes(movie.File.Size.Int64),
			DateAdded:    movie.File.DateAdded.Format("2006-01-02"),
			Quality:      movie.File.Quality.String(),
			ReleaseGroup: movie.File.Quality.ReleaseGroup,
			MediaInfo:    newMediaInfoView(movie.File.MediaInfo),
			Languages:    fileLanguages(movie.File.MediaInfo, movie.File.RelativePath),
		}
	}

	h.renderPage(w, "movie_detail", movieDetailPageData{
		Active: "movies", PageTitle: movie.Title,
		ScanNotice: scanNotice(r),
		Movie:      movie, Ratings: toRatingViews(movie.Ratings), File: file,
		QualityProfileName: movie.QualityProfileName.String,
		HasIndexer:         h.deps.Indexer.Configured(ctx),
		MonitoredChip: monitoredChipView{
			Monitored: movie.Monitored, ToggleURL: fmt.Sprintf("/movies/%d/monitored", movie.ID),
			LabelOn: "Monitored", LabelOff: "Unmonitored",
		},
	})
}

// MovieRefresh rescans movieID's own folder on disk for a file UMMarr
// doesn't know about yet (internal/sync.ImportService.ScanMovie) - the
// "the file exists but I can't point to it" case: a file placed directly
// in the movie's folder, or left over from a previous Radarr/manual
// setup, that a grab/import never went through UMMarr for. Always
// redirects back to the detail page so the Files section reflects
// whatever the rescan found (or didn't).
func (h *handler) MovieRefresh(w http.ResponseWriter, r *http.Request) {
	movieID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid movie id", http.StatusBadRequest)
		return
	}
	imported, err := h.deps.Import.ScanMovie(r.Context(), movieID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.deps.MediaInfo.Reanalyze(r.Context(), "movie", movieID)
	w.Header().Set("HX-Redirect", "/movies/"+r.PathValue("id")+"?scanned="+strconv.Itoa(boolToInt(imported)))
	w.WriteHeader(http.StatusOK)
}

// MovieMonitoredToggle flips movieID's monitored flag and re-renders the
// clickable chip in place (see monitoredChipView) - no page reload.
func (h *handler) MovieMonitoredToggle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	movieID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid movie id", http.StatusBadRequest)
		return
	}
	movie, found, err := store.GetMovieDetail(ctx, h.deps.DB, movieID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	newState := !movie.Monitored
	if err := store.UpdateMovieMonitored(ctx, h.deps.DB, movieID, newState); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderMonitoredChip(w, monitoredChipView{
		Monitored: newState, ToggleURL: fmt.Sprintf("/movies/%d/monitored", movieID),
		LabelOn: "Monitored", LabelOff: "Unmonitored",
	})
}
