package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// seriesAlbums groups Various Artists albums that share a
// compilation_series (or "" for VA albums with no series link yet).
type seriesAlbums struct {
	Name   string
	Albums []store.AlbumSummary
}

// artistAlbums is one artist's section of the Albums library grid.
// Non-VA artists render Albums directly; the Various Artists entry
// (if present) renders via Series instead, matching the approved
// mockup's compilation-series tree.
type artistAlbums struct {
	ArtistName string
	Albums     []store.AlbumSummary
	Series     []seriesAlbums
}

const variousArtistsName = "Various Artists"

func groupAlbums(albums []store.AlbumSummary) []artistAlbums {
	order := []string{}
	byArtist := map[string][]store.AlbumSummary{}
	for _, a := range albums {
		if _, ok := byArtist[a.ArtistName]; !ok {
			order = append(order, a.ArtistName)
		}
		byArtist[a.ArtistName] = append(byArtist[a.ArtistName], a)
	}

	groups := make([]artistAlbums, 0, len(order))
	for _, name := range order {
		artistAlb := byArtist[name]
		if name != variousArtistsName {
			groups = append(groups, artistAlbums{ArtistName: name, Albums: artistAlb})
			continue
		}

		seriesOrder := []string{}
		bySeries := map[string][]store.AlbumSummary{}
		for _, a := range artistAlb {
			if _, ok := bySeries[a.CompilationSeriesName]; !ok {
				seriesOrder = append(seriesOrder, a.CompilationSeriesName)
			}
			bySeries[a.CompilationSeriesName] = append(bySeries[a.CompilationSeriesName], a)
		}
		series := make([]seriesAlbums, 0, len(seriesOrder))
		for _, seriesName := range seriesOrder {
			series = append(series, seriesAlbums{Name: seriesName, Albums: bySeries[seriesName]})
		}
		groups = append(groups, artistAlbums{ArtistName: name, Series: series})
	}
	return groups
}

type musicPageData struct {
	Active          string
	PageTitle       string
	Artists         []store.ArtistSummary
	Albums          []store.AlbumSummary
	ArtistGroups    []artistAlbums
	RootFolders     []store.RootFolder
	QualityProfiles []store.QualityProfile
	HasRootFolder   bool
	HasIndexer      bool
}

func (h *handler) Music(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	artists, err := store.ListArtists(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	albums, err := store.ListAlbums(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rootFolders, err := store.ListRootFolders(ctx, h.deps.DB, "music")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	qualityProfiles, err := store.ListQualityProfiles(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	groups := groupAlbums(albums)
	sortMusic(artists, groups)
	h.renderPage(w, "music", musicPageData{
		Active:          "music",
		PageTitle:       "Music",
		Artists:         artists,
		Albums:          albums,
		ArtistGroups:    groups,
		RootFolders:     rootFolders,
		QualityProfiles: qualityProfiles,
		HasRootFolder:   len(rootFolders) > 0,
		HasIndexer:      h.deps.Indexer.Configured(ctx),
	})
}

type artistSearchResultView struct {
	ID             string
	Name           string
	Disambiguation string
}

type artistResultsData struct {
	Results         []artistSearchResultView
	RootFolders     []store.RootFolder
	QualityProfiles []store.QualityProfile
	// Err, when non-empty, means the MusicBrainz search itself failed - see
	// movieResultsData.Err in movies.go for why this matters.
	Err string
}

func (h *handler) MusicSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := r.URL.Query().Get("q")
	if query == "" {
		h.renderPartial(w, "artist_results", artistResultsData{})
		return
	}

	results, err := h.deps.MusicBrainz.SearchArtist(ctx, query)
	if err != nil {
		h.renderPartial(w, "artist_results", artistResultsData{Err: err.Error()})
		return
	}
	rootFolders, err := store.ListRootFolders(ctx, h.deps.DB, "music")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	qualityProfiles, err := store.ListQualityProfiles(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	views := make([]artistSearchResultView, 0, len(results))
	for _, a := range results {
		views = append(views, artistSearchResultView{ID: a.ID, Name: a.Name, Disambiguation: a.Disambiguation})
	}

	h.renderPartial(w, "artist_results", artistResultsData{
		Results:         views,
		RootFolders:     rootFolders,
		QualityProfiles: qualityProfiles,
	})
}

func (h *handler) MusicArtistAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	mbid := r.FormValue("mbid")
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

	if _, err := h.deps.Music.AddArtistByMBID(r.Context(), mbid, rootFolderID, qualityProfileID); err != nil {
		renderAddError(w, err)
		return
	}

	w.Header().Set("HX-Redirect", "/music")
	w.WriteHeader(http.StatusOK)
}

type albumSearchResultView struct {
	ID          string
	Title       string
	PrimaryType string
}

type albumResultsData struct {
	ArtistID   int64
	ArtistName string
	Results    []albumSearchResultView
}

// MusicArtistAlbums browses one already-tracked artist's MusicBrainz
// discography, so the user can pick specific albums to add - AddAlbumByMBID
// requires the artist to already exist in the library (it looks up
// artist_metadata_id from the store artist row), so {id} here is the
// store artists.id, not a MusicBrainz MBID.
func (h *handler) MusicArtistAlbums(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	artistID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid artist id", http.StatusBadRequest)
		return
	}

	var artistMetadataID int64
	var artistName string
	if err := h.deps.DB.QueryRowContext(ctx, `SELECT a.artist_metadata_id, m.name FROM artists a JOIN artist_metadata m ON m.id = a.artist_metadata_id WHERE a.id = ?`, artistID).Scan(&artistMetadataID, &artistName); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	mbid, found, err := store.GetExternalID(ctx, h.deps.DB, "artist", artistMetadataID, "musicbrainz")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		h.renderPartial(w, "album_results", albumResultsData{ArtistID: artistID, ArtistName: artistName})
		return
	}

	releaseGroups, err := h.deps.MusicBrainz.GetArtistReleaseGroups(ctx, mbid)
	if err != nil {
		h.renderPartial(w, "album_results", albumResultsData{ArtistID: artistID, ArtistName: artistName})
		return
	}

	views := make([]albumSearchResultView, 0, len(releaseGroups))
	for _, rg := range releaseGroups {
		views = append(views, albumSearchResultView{ID: rg.ID, Title: rg.Title, PrimaryType: rg.PrimaryType})
	}

	h.renderPartial(w, "album_results", albumResultsData{ArtistID: artistID, ArtistName: artistName, Results: views})
}

func (h *handler) MusicAlbumAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	releaseGroupMBID := r.FormValue("release_group_mbid")
	artistID, err := strconv.ParseInt(r.FormValue("artist_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid artist_id", http.StatusBadRequest)
		return
	}

	albumID, err := h.deps.Music.AddAlbumByMBID(r.Context(), releaseGroupMBID, artistID)
	if err != nil {
		renderAddError(w, err)
		return
	}
	h.recordItemEvent(r, store.EventAdded, "music", 0, 0, albumID, "Added from the artist's albums", "interactive")

	w.Header().Set("HX-Redirect", "/music")
	w.WriteHeader(http.StatusOK)
}

// trackView is a template-friendly projection of store.TrackDetail.
type trackView struct {
	ID           int64
	TrackNumber  string
	Title        string
	Duration     string
	HasFile      bool
	FileStatus   string
	Quality      string
	ReleaseGroup string
	Audio        string
}

type albumDetailPageData struct {
	Active             string
	PageTitle          string
	Album              store.AlbumDetail
	Ratings            []ratingView
	Tracks             []trackView
	QualityProfileName string
	HasIndexer         bool
	MonitoredChip      monitoredChipView
}

// AlbumDetail is the album detail page (GET /music/albums/{id}) - poster/
// overview/genres/ratings like the movie/series pages, plus a track list
// with per-track file status. Track-level search/grab stays out of scope
// (albums are matched positionally, not per-track - see
// ListTracksForAlbum/FindImportRelease's documented limitation);
// album-level "Find release" is reused unchanged from the old card.
func (h *handler) AlbumDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	album, ok := h.albumFromRef(w, r)
	if !ok {
		return
	}
	albumID := album.ID

	tracks, err := store.ListTracksForAlbum(ctx, h.deps.DB, albumID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	views := make([]trackView, 0, len(tracks))
	for _, t := range tracks {
		status := "Missing"
		if t.HasFile {
			status = "Downloaded"
		}
		duration := ""
		if t.DurationMs.Valid {
			duration = humanizeDuration(t.DurationMs.Int64)
		}
		views = append(views, trackView{
			ID: t.ID, TrackNumber: t.TrackNumber, Title: t.Title, Duration: duration,
			HasFile: t.HasFile, FileStatus: status,
			Quality: t.Quality.String(), ReleaseGroup: t.Quality.ReleaseGroup, Audio: t.MediaInfo.TrackSummary(),
		})
	}

	h.renderPage(w, "album_detail", albumDetailPageData{
		Active: "music", PageTitle: album.Title,
		Album: album, Ratings: toRatingViews(album.Ratings), Tracks: views,
		QualityProfileName: album.QualityProfileName.String,
		HasIndexer:         h.deps.Indexer.Configured(ctx),
		MonitoredChip: monitoredChipView{
			Monitored: album.Monitored, ToggleURL: fmt.Sprintf("/music/albums/%d/monitored", albumID),
			LabelOn: "Monitored", LabelOff: "Unmonitored",
		},
	})
}

// AlbumMonitoredToggle flips albumID's monitored flag and re-renders the
// clickable chip in place - see MovieMonitoredToggle.
func (h *handler) AlbumMonitoredToggle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	albumID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid album id", http.StatusBadRequest)
		return
	}
	album, found, err := store.GetAlbumDetail(ctx, h.deps.DB, albumID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	newState := !album.Monitored
	if err := store.UpdateAlbumMonitored(ctx, h.deps.DB, albumID, newState); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderMonitoredChip(w, monitoredChipView{
		Monitored: newState, ToggleURL: fmt.Sprintf("/music/albums/%d/monitored", albumID),
		LabelOn: "Monitored", LabelOff: "Unmonitored",
	})
}

// byTitle orders names the way Radarr sorts: a leading The, A or An ignored.
func byTitle(a, b string) bool {
	x, _ := sortKeys(a)
	y, _ := sortKeys(b)
	if x == y {
		return a < b
	}
	return x < y
}

func sortMusic(artists []store.ArtistSummary, groups []artistAlbums) {
	sort.SliceStable(artists, func(i, j int) bool { return byTitle(artists[i].Name, artists[j].Name) })
	sort.SliceStable(groups, func(i, j int) bool { return byTitle(groups[i].ArtistName, groups[j].ArtistName) })
	for _, g := range groups {
		sort.SliceStable(g.Albums, func(i, j int) bool { return byTitle(g.Albums[i].Title, g.Albums[j].Title) })
	}
}
