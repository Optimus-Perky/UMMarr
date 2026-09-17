package api

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/decision"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// releaseResultView is a template-friendly projection of one judged release,
// shared across movies/tv/music - GrabURL carries the one difference (which
// media type's grab endpoint the form posts to). AgeMinutes/Size/Seeders are
// exposed as their raw sortable values too (Age/SizeHuman are just the
// human-readable strings) - see partials/release_results.html's
// data-value attributes and static/sortable-table.js, which sort on
// these rather than the formatted text.
type releaseResultView struct {
	GUID, Title, Indexer, Protocol string
	IndexerID                      int64
	Size                           int64
	SizeHuman                      string
	Age                            string
	AgeMinutes                     int
	Seeders, Leechers              int
	InfoURL                        string // the indexer's own page for this release, if it gave one
	Categories                     string // e.g. "Movies/HD, Movies/UHD"
	Quality                        string // parsed from Title via internal/releaseparse - display only, not persisted
	ReleaseGroup                   string
	KeywordScore                   int      // sum of matching Settings -> Preferred Words scores; 0 means none matched
	FormatScore                    int      // sum of the matching custom formats' scores in the item's quality profile
	CustomFormats                  []string // the formats it matches
	TotalScore                     int      // KeywordScore + FormatScore, what ranks releases
	Grabbable                      bool     // an enabled download client takes this protocol
	// Rejections are why UMMarr wouldn't grab this release on its own. It can
	// still be grabbed by hand, as in Radarr.
	Rejections []string
	Languages  string   // guessed from the name, English when it says nothing
	Flags      []string // indexer flags, e.g. Freeleech
	PeersClass string   // colours the Peers badge
	SeasonPack bool     // a whole-season pack, from the name
}

type releaseResultsData struct {
	dialogInfo
	// DefaultFilter is the Filter option the dialog opens on: a season search
	// starts on season packs, as Sonarr does.
	DefaultFilter string
	Results       []releaseResultView
	GrabURL       string
	// IndexerErrors are the indexers that failed; the rest still show
	// their results.
	IndexerErrors []sync.IndexerError
	// Err, when non-empty, means there was nothing to search with at all -
	// rendered distinctly from a genuine zero-result search.
	Err string
}

// onlyQualityAllowed drops releases whose quality the item's profile
// doesn't want at all. asked for TV search specifically to only show
// results matching the (series') quality profile, rather than showing
// every release with a rejection badge as the other rejection reasons do -
// so this filters, it doesn't just annotate.
func onlyQualityAllowed(decisions []decision.Decision) []decision.Decision {
	kept := make([]decision.Decision, 0, len(decisions))
	for _, d := range decisions {
		if d.QualityAllowed {
			kept = append(kept, d)
		}
	}
	return kept
}

func toReleaseResultViews(decisions []decision.Decision, protocols map[string]bool) []releaseResultView {
	views := make([]releaseResultView, 0, len(decisions))
	for _, d := range decisions {
		r := d.Release
		age, minutes := humanizeAge(r.PublishDate, time.Now())
		views = append(views, releaseResultView{
			GUID: r.GUID, Title: r.Title, Indexer: r.Indexer, IndexerID: r.IndexerID, Protocol: r.Protocol,
			Size: r.Size, SizeHuman: humanizeBytes(r.Size), Age: age, AgeMinutes: minutes,
			Seeders: max(r.Seeders, 0), Leechers: max(r.Leechers(), 0), InfoURL: r.InfoURL,
			Categories: categoryNames(r.Categories), Grabbable: protocols[r.Protocol],
			Quality: d.Quality.String(), ReleaseGroup: d.Quality.ReleaseGroup, KeywordScore: d.KeywordScore, FormatScore: d.CustomFormatScore, CustomFormats: d.CustomFormats, TotalScore: d.KeywordScore + d.CustomFormatScore, Rejections: d.Rejections,
			Languages: strings.Join(releaseparse.Languages(r.Title), ", "), Flags: flagNames(r.Flags), PeersClass: peersClass(r.Protocol, r.Seeders),
			SeasonPack: isSeasonPack(r.Title),
		})
	}
	return views
}

// categoryNames joins a release's standard category names (e.g.
// "Movies/HD, Movies/UHD") into one display string. Indexer-specific
// categories (100000 and up) have no standard name and are left out.
func categoryNames(ids []int) string {
	names := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if name := newznab.CategoryName(id); name != "" && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

// humanizeAge turns a release's publish date into Radarr's age text ("5
// hours", "3 days") plus its age in minutes for sorting. An unknown date
// gives ("", 0) - a missing age isn't worth failing the render over.
func humanizeAge(published, now time.Time) (string, int) {
	if published.IsZero() {
		return "", 0
	}
	age := now.Sub(published)
	if age < 0 {
		age = 0
	}
	minutes := int(age.Minutes())
	plural := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit
		}
		return fmt.Sprintf("%d %ss", n, unit)
	}
	switch {
	case age < time.Hour:
		return plural(minutes, "minute"), minutes
	case age < 24*time.Hour:
		return plural(int(age.Hours()), "hour"), minutes
	default:
		return plural(int(age.Hours()/24), "day"), minutes
	}
}

func humanizeBytes(size int64) string {
	if size <= 0 {
		return ""
	}
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

// renderReleaseResults judges a search's releases with judge and renders
// them, approved releases first.
func (h *handler) renderReleaseResults(w http.ResponseWriter, r *http.Request, dialog dialogInfo, grabURL string, result sync.SearchResult, judge func(*decision.Engine) []decision.Decision) {
	h.renderReleaseResultsFiltered(w, r, dialog, "all", grabURL, result, judge)
}

func (h *handler) renderReleaseResultsFiltered(w http.ResponseWriter, r *http.Request, dialog dialogInfo, defaultFilter, grabURL string, result sync.SearchResult, judge func(*decision.Engine) []decision.Decision) {
	data := releaseResultsData{dialogInfo: dialog, DefaultFilter: defaultFilter, GrabURL: grabURL, IndexerErrors: result.Errors}
	if result.Searched == 0 && len(result.Errors) == 0 {
		data.Err = sync.ErrNoIndexers.Error()
	}
	engine, err := decision.Load(r.Context(), h.deps.DB, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.Results = toReleaseResultViews(judge(engine), h.deps.Download.EnabledProtocols(r.Context()))
	h.renderPartial(w, "release_dialog", data)
}

var errReleaseExpired = fmt.Errorf("this search result has expired - search again and grab it from the new results")

// releaseFromForm finds the release a Grab button refers to among recent
// search results. The form carries only the indexer and GUID, so download
// links (which often include an indexer's API key) never reach the browser.
func (h *handler) releaseFromForm(r *http.Request) (newznab.Release, error) {
	indexerID, _ := strconv.ParseInt(r.FormValue("indexer_id"), 10, 64)
	release, ok := h.deps.Indexer.CachedRelease(indexerID, r.FormValue("guid"))
	if !ok {
		return newznab.Release{}, errReleaseExpired
	}
	return release, nil
}

func pathID(w http.ResponseWriter, r *http.Request, name, what string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		http.Error(w, "invalid "+what+" id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func (h *handler) MovieReleases(w http.ResponseWriter, r *http.Request) {
	movieID, ok := pathID(w, r, "id", "movie")
	if !ok {
		return
	}
	movie, err := store.GetWantedMovie(r.Context(), h.deps.DB, movieID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	result := h.deps.Indexer.SearchMovie(r.Context(), sync.PurposeInteractive, sync.MovieCriteria{
		Title: movie.Title, Year: movie.Year, TMDbID: movie.TMDbID, IMDbID: movie.IMDbID,
	})
	h.renderReleaseResults(w, r, h.movieDialog(r.Context(), movie), fmt.Sprintf("/movies/%d/grab", movieID), result, func(e *decision.Engine) []decision.Decision {
		return e.Movie(movie, result.Releases)
	})
}

func (h *handler) MovieGrab(w http.ResponseWriter, r *http.Request) {
	movieID, ok := pathID(w, r, "id", "movie")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	release, err := h.releaseFromForm(r)
	if err != nil {
		renderGrabError(w, err)
		return
	}

	if _, err := h.deps.Download.GrabMovie(r.Context(), movieID, release, sync.PurposeInteractive); err != nil {
		renderGrabError(w, err)
		return
	}
	renderGrabbed(w, release, h.deps.Download.ClientNameFor(r.Context(), release.Protocol), "")
}

func (h *handler) SeriesReleases(w http.ResponseWriter, r *http.Request) {
	seriesID, ok := pathID(w, r, "id", "series")
	if !ok {
		return
	}
	series, err := store.GetWantedSeries(r.Context(), h.deps.DB, seriesID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	criteria := sync.SeriesCriteria{Title: series.Title, TVDBID: series.TVDBID}
	if s := r.URL.Query().Get("season"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			criteria.Season = &n
		}
	}

	result := h.deps.Indexer.SearchSeries(r.Context(), sync.PurposeInteractive, criteria)
	defaultFilter := "all"
	if criteria.Season != nil {
		defaultFilter = "season-pack"
	}
	h.renderReleaseResultsFiltered(w, r, h.seriesDialog(r.Context(), series, criteria.Season), defaultFilter, fmt.Sprintf("/tv/%d/grab", seriesID), result, func(e *decision.Engine) []decision.Decision {
		return onlyQualityAllowed(e.Series(series, decision.SeriesScope{Season: criteria.Season}, result.Releases))
	})
}

func (h *handler) SeriesGrab(w http.ResponseWriter, r *http.Request) {
	seriesID, ok := pathID(w, r, "id", "series")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var seasonNumber *int
	if s := r.FormValue("season"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			seasonNumber = &n
		}
	}
	release, err := h.releaseFromForm(r)
	if err != nil {
		renderGrabError(w, err)
		return
	}

	if _, err := h.deps.Download.GrabSeries(r.Context(), seriesID, seasonNumber, release, sync.PurposeInteractive); err != nil {
		renderGrabError(w, err)
		return
	}
	renderGrabbed(w, release, h.deps.Download.ClientNameFor(r.Context(), release.Protocol), "")
}

// episodeFromPath reads the series and the episode being searched or grabbed.
func (h *handler) episodeFromPath(w http.ResponseWriter, r *http.Request) (store.WantedSeries, store.WantedEpisode, bool) {
	seriesID, ok := pathID(w, r, "id", "series")
	if !ok {
		return store.WantedSeries{}, store.WantedEpisode{}, false
	}
	episodeID, ok := pathID(w, r, "episodeId", "episode")
	if !ok {
		return store.WantedSeries{}, store.WantedEpisode{}, false
	}
	series, err := store.GetWantedSeries(r.Context(), h.deps.DB, seriesID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return store.WantedSeries{}, store.WantedEpisode{}, false
	}
	for _, ep := range series.Episodes {
		if ep.ID == episodeID {
			return series, ep, true
		}
	}
	http.Error(w, "episode not found", http.StatusNotFound)
	return store.WantedSeries{}, store.WantedEpisode{}, false
}

// EpisodeReleases searches for releases matching one specific episode -
// the series detail page's per-episode "Find release" button.
func (h *handler) EpisodeReleases(w http.ResponseWriter, r *http.Request) {
	series, episode, ok := h.episodeFromPath(w, r)
	if !ok {
		return
	}
	criteria := sync.SeriesCriteria{Title: series.Title, TVDBID: series.TVDBID, Season: &episode.SeasonNumber, Episode: &episode.EpisodeNumber}

	result := h.deps.Indexer.SearchSeries(r.Context(), sync.PurposeInteractive, criteria)
	grabURL := fmt.Sprintf("/tv/%d/episodes/%d/grab", series.ID, episode.ID)
	h.renderReleaseResults(w, r, h.episodeDialog(r.Context(), episode), grabURL, result, func(e *decision.Engine) []decision.Decision {
		return onlyQualityAllowed(e.Series(series, decision.SeriesScope{Season: criteria.Season, Episode: criteria.Episode}, result.Releases))
	})
}

// EpisodeGrab grabs a release for one specific episode - the resulting
// grab's season/episode numbers are read from the episode row itself
// (not client-supplied form fields), so DownloadService.GrabEpisode
// always records the episode actually being grabbed.
func (h *handler) EpisodeGrab(w http.ResponseWriter, r *http.Request) {
	series, episode, ok := h.episodeFromPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	release, err := h.releaseFromForm(r)
	if err != nil {
		renderGrabError(w, err)
		return
	}

	if _, err := h.deps.Download.GrabEpisode(r.Context(), series.ID, episode.SeasonNumber, episode.EpisodeNumber, release, sync.PurposeInteractive); err != nil {
		renderGrabError(w, err)
		return
	}
	renderGrabbed(w, release, h.deps.Download.ClientNameFor(r.Context(), release.Protocol), downloadingChip(episode.ID))
}

func (h *handler) AlbumReleases(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	album, err := store.GetWantedAlbum(r.Context(), h.deps.DB, albumID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	result := h.deps.Indexer.SearchAlbum(r.Context(), sync.PurposeInteractive, sync.AlbumCriteria{Artist: album.Artist, Album: album.Title})
	h.renderReleaseResults(w, r, h.albumDialog(r.Context(), album), fmt.Sprintf("/music/albums/%d/grab", albumID), result, func(e *decision.Engine) []decision.Decision {
		return e.Album(album, result.Releases)
	})
}

func (h *handler) AlbumGrab(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	release, err := h.releaseFromForm(r)
	if err != nil {
		renderGrabError(w, err)
		return
	}

	if _, err := h.deps.Download.GrabAlbum(r.Context(), albumID, release, sync.PurposeInteractive); err != nil {
		renderGrabError(w, err)
		return
	}
	renderGrabbed(w, release, h.deps.Download.ClientNameFor(r.Context(), release.Protocol), "")
}

// TrackReleases searches for releases matching one specific track - the
// album detail page's per-track "Find release" button.
func (h *handler) TrackReleases(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	trackID, ok := pathID(w, r, "trackId", "track")
	if !ok {
		return
	}
	album, err := store.GetWantedAlbum(r.Context(), h.deps.DB, albumID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	// The track can belong to a release other than the one the album's
	// tracks are listed from, so it's read on its own.
	track := store.WantedTrack{ID: trackID}
	err = h.deps.DB.QueryRowContext(r.Context(), `
		SELECT t.title, t.track_file_id IS NOT NULL
		FROM tracks t
		JOIN album_releases ar ON ar.id = t.album_release_id
		WHERE t.id = ? AND ar.album_id = ?
	`, trackID, albumID).Scan(&track.Title, &track.HasFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	result := h.deps.Indexer.SearchTrack(r.Context(), sync.PurposeInteractive, album.Artist, track.Title)
	grabURL := fmt.Sprintf("/music/albums/%d/tracks/%d/grab", albumID, trackID)
	h.renderReleaseResults(w, r, h.trackDialog(r.Context(), album, track), grabURL, result, func(e *decision.Engine) []decision.Decision {
		return e.Track(album, track, result.Releases)
	})
}

// TrackGrab grabs a release for one specific track under albumID.
func (h *handler) TrackGrab(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	trackID, ok := pathID(w, r, "trackId", "track")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	release, err := h.releaseFromForm(r)
	if err != nil {
		renderGrabError(w, err)
		return
	}

	if _, err := h.deps.Download.GrabTrack(r.Context(), albumID, trackID, release, sync.PurposeInteractive); err != nil {
		renderGrabError(w, err)
		return
	}
	renderGrabbed(w, release, h.deps.Download.ClientNameFor(r.Context(), release.Protocol), "")
}

// renderGrabbed replaces the grabbed result's row (the Grab form targets its
// own row) with a line saying so, keeping the user on the page. extra is
// swapped out-of-band, e.g. an episode's Status chip.
func renderGrabbed(w http.ResponseWriter, release newznab.Release, clientName, extra string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<tr><td colspan="15"><span class="indexer-test ok">Grabbed %s from %s and sent to %s. <a href="/activity">Activity →</a></span></td></tr>%s`,
		html.EscapeString(release.Title), html.EscapeString(release.Indexer), html.EscapeString(clientName), extra)
}

// downloadingChip is an episode's Status cell as it looks once grabbed.
func downloadingChip(episodeID int64) string {
	return fmt.Sprintf(`<td data-col="status" id="episode-status-%d" hx-swap-oob="true"><span class="chip chip-info">Downloading</span></td>`, episodeID)
}

// isSeasonPack reports whether a release name is a whole-season pack.
func isSeasonPack(title string) bool {
	info, ok := releaseparse.ParseEpisode(title)
	return ok && info.FullSeason
}

// renderGrabError answers a failed Grab with a row for the results table,
// in place of the release's row - the same shape as renderGrabbed - with
// 422 so htmx (static/htmx-errors.js) shows it.
func renderGrabError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	fmt.Fprintf(w, `<tr><td colspan="15"><p class="notice grab-error">Couldn't grab: %s</p></td></tr>`, html.EscapeString(err.Error()))
}
