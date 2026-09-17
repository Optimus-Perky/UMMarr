package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
	"github.com/Optimus-Perky/UMMarr/internal/version"
)

// The REST API under /api/v3: Radarr/Sonarr-shaped JSON for the library,
// queue, history, profiles, folders, clients, health and commands, behind
// the same API key Prowlarr uses (X-Api-Key or ?apikey=) or a signed-in
// session. GET /api describes it.

func apiError(w http.ResponseWriter, status int, format string, args ...any) {
	writeJSON(w, status, map[string]string{"message": fmt.Sprintf(format, args...)})
}

func apiPathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		apiError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		apiError(w, http.StatusBadRequest, "bad JSON body: %v", err)
		return false
	}
	return true
}

func nullTimeJSON(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time.UTC().Format("2006-01-02")
}

func nullStringJSON(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return s.String
}

// externalIDs maps library items to a provider's id for a whole table at once.
func externalIDs(ctx context.Context, q store.Queryer, entity, provider, table, column string) map[int64]string {
	out := map[int64]string{}
	rows, err := q.QueryContext(ctx, `SELECT t.id, e.external_id FROM `+table+` t JOIN external_ids e ON e.entity_type = ? AND e.provider = ? AND e.entity_id = t.`+column, entity, provider)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var ext string
		if rows.Scan(&id, &ext) == nil {
			out[id] = ext
		}
	}
	return out
}

// ---- movies

func movieJSON(m store.MovieSummary, tmdbID string, file *store.MovieFileInfo) map[string]any {
	id, _ := strconv.Atoi(tmdbID)
	out := map[string]any{
		"id": m.ID, "title": m.Title, "year": m.Year.Int64, "tmdbId": id, "monitored": m.Monitored, "hasFile": m.HasFile,
		"path": nullStringJSON(m.Path), "rootFolderId": m.RootFolderID, "qualityProfileId": m.QualityProfileID.Int64, "qualityProfileName": m.QualityProfileName,
		"added": m.Added.UTC().Format(time.RFC3339), "sizeOnDisk": m.SizeOnDisk, "minimumAvailability": m.MinimumAvailability,
		"inCinemas": nullTimeJSON(m.InCinemas), "physicalRelease": nullTimeJSON(m.PhysicalRelease), "digitalRelease": nullTimeJSON(m.DigitalRelease),
		"ratings": m.Ratings, "quality": m.FileQuality.Key(),
	}
	if file != nil {
		out["movieFile"] = map[string]any{"relativePath": file.RelativePath, "size": file.Size.Int64, "quality": file.Quality.Key(), "codec": file.Quality.Codec, "releaseGroup": file.Quality.ReleaseGroup, "dateAdded": file.DateAdded.UTC().Format(time.RFC3339)}
	}
	return out
}

func (h *handler) ApiListMovies(w http.ResponseWriter, r *http.Request) {
	movies, err := store.ListMovies(r.Context(), h.deps.DB)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	tmdb := externalIDs(r.Context(), h.deps.DB, "movie", "tmdb", "movies", "movie_metadata_id")
	out := make([]map[string]any, 0, len(movies))
	for _, m := range movies {
		out = append(out, movieJSON(m, tmdb[m.ID], nil))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) ApiGetMovie(w http.ResponseWriter, r *http.Request) {
	if id, ok := apiPathID(w, r); ok {
		h.writeMovie(w, r, id, http.StatusOK)
	}
}

func (h *handler) writeMovie(w http.ResponseWriter, r *http.Request, id int64, status int) {
	d, found, err := store.GetMovieDetail(r.Context(), h.deps.DB, id)
	if err != nil || !found {
		apiError(w, http.StatusNotFound, "movie %d not found", id)
		return
	}
	tmdb := externalIDs(r.Context(), h.deps.DB, "movie", "tmdb", "movies", "movie_metadata_id")
	out := movieJSON(d.MovieSummary, tmdb[d.ID], d.File)
	out["overview"], out["genres"], out["studio"], out["certification"] = nullStringJSON(d.Overview), d.Genres, nullStringJSON(d.Studio), nullStringJSON(d.Certification)
	writeJSON(w, status, out)
}

type addMovieBody struct {
	TMDBID           int   `json:"tmdbId"`
	RootFolderID     int64 `json:"rootFolderId"`
	QualityProfileID int64 `json:"qualityProfileId"`
	Monitored        *bool `json:"monitored"`
	SearchNow        bool  `json:"searchForMovie"`
}

func (h *handler) defaults(ctx context.Context, mediaType string, rootFolderID, profileID int64) (int64, int64, error) {
	if rootFolderID == 0 {
		folders, err := store.ListRootFolders(ctx, h.deps.DB, mediaType)
		if err != nil || len(folders) == 0 {
			return 0, 0, fmt.Errorf("no %s library folder; give rootFolderId", mediaType)
		}
		rootFolderID = folders[0].ID
	}
	if profileID == 0 {
		id, err := store.DefaultQualityProfileID(ctx, h.deps.DB)
		if err != nil {
			return 0, 0, fmt.Errorf("no quality profile; give qualityProfileId")
		}
		profileID = id
	}
	return rootFolderID, profileID, nil
}

func (h *handler) ApiAddMovie(w http.ResponseWriter, r *http.Request) {
	var body addMovieBody
	if !decodeBody(w, r, &body) {
		return
	}
	if body.TMDBID <= 0 || h.deps.Movies == nil {
		apiError(w, http.StatusBadRequest, "tmdbId is required")
		return
	}
	root, profile, err := h.defaults(r.Context(), "movie", body.RootFolderID, body.QualityProfileID)
	if err != nil {
		apiError(w, http.StatusBadRequest, "%v", err)
		return
	}
	movieID, err := h.deps.Movies.AddByTMDBID(r.Context(), body.TMDBID, root, profile)
	if err != nil {
		apiError(w, http.StatusBadGateway, "%v", err)
		return
	}
	if body.Monitored != nil && !*body.Monitored {
		_ = store.UpdateMovieMonitored(r.Context(), h.deps.DB, movieID, false)
	}
	h.recordItemEvent(r, store.EventAdded, "movie", movieID, 0, 0, "Added through the API", "api")
	if body.SearchNow && h.deps.Search != nil {
		go h.deps.Search.SearchMovie(context.Background(), movieID)
	}
	h.writeMovie(w, r, movieID, http.StatusCreated)
}

func (h *handler) ApiUpdateMovie(w http.ResponseWriter, r *http.Request) {
	id, ok := apiPathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Monitored *bool `json:"monitored"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Monitored != nil {
		if err := store.UpdateMovieMonitored(r.Context(), h.deps.DB, id, *body.Monitored); err != nil {
			apiError(w, http.StatusInternalServerError, "%v", err)
			return
		}
	}
	h.ApiGetMovie(w, r)
}

func (h *handler) ApiDeleteMovie(w http.ResponseWriter, r *http.Request) {
	id, ok := apiPathID(w, r)
	if !ok {
		return
	}
	deleteFiles := r.URL.Query().Get("deleteFiles") == "true"
	if r.URL.Query().Get("addImportExclusion") == "true" {
		h.excludeItem(r, "movie", id)
	}
	if h.deps.Import == nil {
		apiError(w, http.StatusServiceUnavailable, "import service isn't available")
		return
	}
	if err := h.deps.Import.DeleteMovie(r.Context(), id, deleteFiles); err != nil && !errors.Is(err, sync.ErrUnsafeDelete) {
		apiError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) ApiLookupMovie(w http.ResponseWriter, r *http.Request) {
	term := strings.TrimSpace(r.URL.Query().Get("term"))
	if term == "" || h.deps.TMDB == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	results, err := h.deps.TMDB.SearchMovies(r.Context(), term, 0)
	if err != nil {
		apiError(w, http.StatusBadGateway, "%v", err)
		return
	}
	out := make([]map[string]any, 0, len(results))
	for _, m := range results {
		out = append(out, map[string]any{"tmdbId": m.ID, "title": m.Title, "year": year(m.ReleaseDate), "overview": m.Overview})
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- series

func seriesJSON(s store.SeriesSummary, tmdbID string) map[string]any {
	id, _ := strconv.Atoi(tmdbID)
	return map[string]any{
		"id": s.ID, "title": s.Title, "tmdbId": id, "monitored": s.Monitored, "status": s.StatusKey(), "path": nullStringJSON(s.Path),
		"rootFolderId": s.RootFolderID, "qualityProfileId": s.QualityProfileID.Int64, "qualityProfileName": s.QualityProfileName,
		"added": s.Added.UTC().Format(time.RFC3339), "episodeCount": s.EpisodeCount, "episodeFileCount": s.EpisodeFileCount, "ratings": s.Ratings,
	}
}

func (h *handler) ApiListSeries(w http.ResponseWriter, r *http.Request) {
	series, err := store.ListSeries(r.Context(), h.deps.DB)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	tmdb := externalIDs(r.Context(), h.deps.DB, "series", "tmdb", "series", "series_metadata_id")
	out := make([]map[string]any, 0, len(series))
	for _, s := range series {
		out = append(out, seriesJSON(s, tmdb[s.ID]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) ApiGetSeries(w http.ResponseWriter, r *http.Request) {
	if id, ok := apiPathID(w, r); ok {
		h.writeSeries(w, r, id, http.StatusOK)
	}
}

func (h *handler) writeSeries(w http.ResponseWriter, r *http.Request, id int64, status int) {
	d, found, err := store.GetSeriesDetail(r.Context(), h.deps.DB, id)
	if err != nil || !found {
		apiError(w, http.StatusNotFound, "series %d not found", id)
		return
	}
	tmdb := externalIDs(r.Context(), h.deps.DB, "series", "tmdb", "series", "series_metadata_id")
	out := seriesJSON(d.SeriesSummary, tmdb[d.ID])
	out["overview"] = nullStringJSON(d.Overview)
	seasons, _ := store.ListSeasonsForSeries(r.Context(), h.deps.DB, id)
	list := make([]map[string]any, 0, len(seasons))
	for _, se := range seasons {
		list = append(list, map[string]any{"seasonNumber": se.SeasonNumber, "monitored": se.Monitored, "totalEpisodeCount": se.Total, "episodeFileCount": se.Downloaded})
	}
	out["seasons"] = list
	writeJSON(w, status, out)
}

func (h *handler) ApiAddSeries(w http.ResponseWriter, r *http.Request) {
	var body addMovieBody
	if !decodeBody(w, r, &body) {
		return
	}
	if body.TMDBID <= 0 || h.deps.Series == nil {
		apiError(w, http.StatusBadRequest, "tmdbId is required")
		return
	}
	root, profile, err := h.defaults(r.Context(), "series", body.RootFolderID, body.QualityProfileID)
	if err != nil {
		apiError(w, http.StatusBadRequest, "%v", err)
		return
	}
	seriesID, err := h.deps.Series.AddByTMDBID(r.Context(), body.TMDBID, root, profile)
	if err != nil {
		apiError(w, http.StatusBadGateway, "%v", err)
		return
	}
	if body.Monitored != nil && !*body.Monitored {
		_ = store.UpdateSeriesMonitored(r.Context(), h.deps.DB, seriesID, false)
	}
	h.recordItemEvent(r, store.EventAdded, "series", 0, seriesID, 0, "Added through the API", "api")
	h.writeSeries(w, r, seriesID, http.StatusCreated)
}

func (h *handler) ApiUpdateSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := apiPathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Monitored *bool `json:"monitored"`
		Seasons   []struct {
			SeasonNumber int  `json:"seasonNumber"`
			Monitored    bool `json:"monitored"`
		} `json:"seasons"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Monitored != nil {
		if err := store.UpdateSeriesMonitored(r.Context(), h.deps.DB, id, *body.Monitored); err != nil {
			apiError(w, http.StatusInternalServerError, "%v", err)
			return
		}
	}
	for _, se := range body.Seasons {
		_ = store.UpdateSeasonMonitored(r.Context(), h.deps.DB, id, se.SeasonNumber, se.Monitored)
	}
	h.ApiGetSeries(w, r)
}

func (h *handler) ApiDeleteSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := apiPathID(w, r)
	if !ok {
		return
	}
	if h.deps.Import == nil {
		apiError(w, http.StatusServiceUnavailable, "import service isn't available")
		return
	}
	if r.URL.Query().Get("addImportExclusion") == "true" {
		h.excludeItem(r, "series", id)
	}
	if err := h.deps.Import.DeleteSeries(r.Context(), id, r.URL.Query().Get("deleteFiles") == "true"); err != nil && !errors.Is(err, sync.ErrUnsafeDelete) {
		apiError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) ApiLookupSeries(w http.ResponseWriter, r *http.Request) {
	term := strings.TrimSpace(r.URL.Query().Get("term"))
	if term == "" || h.deps.TMDB == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	results, err := h.deps.TMDB.SearchSeries(r.Context(), term)
	if err != nil {
		apiError(w, http.StatusBadGateway, "%v", err)
		return
	}
	out := make([]map[string]any, 0, len(results))
	for _, s := range results {
		out = append(out, map[string]any{"tmdbId": s.ID, "title": s.Name, "year": year(s.FirstAirDate), "overview": s.Overview})
	}
	writeJSON(w, http.StatusOK, out)
}

func episodeJSON(seriesID int64, e store.EpisodeDetail) map[string]any {
	return map[string]any{
		"id": e.ID, "seriesId": seriesID, "seasonNumber": e.SeasonNumber, "episodeNumber": e.EpisodeNumber, "title": nullStringJSON(e.Title),
		"airDate": nullTimeJSON(e.AirDate), "monitored": e.Monitored, "hasFile": e.HasFile, "size": e.FileSize.Int64, "quality": e.Quality.Key(),
	}
}

func (h *handler) ApiListEpisodes(w http.ResponseWriter, r *http.Request) {
	seriesID, err := strconv.ParseInt(r.URL.Query().Get("seriesId"), 10, 64)
	if err != nil || seriesID <= 0 {
		apiError(w, http.StatusBadRequest, "seriesId is required")
		return
	}
	episodes, err := store.ListEpisodesForSeries(r.Context(), h.deps.DB, seriesID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	out := make([]map[string]any, 0, len(episodes))
	for _, e := range episodes {
		out = append(out, episodeJSON(seriesID, e))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) ApiUpdateEpisode(w http.ResponseWriter, r *http.Request) {
	id, ok := apiPathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Monitored *bool `json:"monitored"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Monitored != nil {
		if err := store.UpdateEpisodeMonitored(r.Context(), h.deps.DB, id, *body.Monitored); err != nil {
			apiError(w, http.StatusInternalServerError, "%v", err)
			return
		}
	}
	info, found, err := store.GetEpisodeInfo(r.Context(), h.deps.DB, id)
	if err != nil || !found {
		apiError(w, http.StatusNotFound, "episode %d not found", id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": info.ID, "seriesId": info.SeriesID, "seasonNumber": info.SeasonNumber, "episodeNumber": info.EpisodeNumber, "monitored": info.Monitored, "hasFile": info.File != nil})
}

// ---- music

func (h *handler) ApiListArtists(w http.ResponseWriter, r *http.Request) {
	artists, err := store.ListArtists(r.Context(), h.deps.DB)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	mb := externalIDs(r.Context(), h.deps.DB, "artist", "musicbrainz", "artists", "artist_metadata_id")
	out := make([]map[string]any, 0, len(artists))
	for _, a := range artists {
		out = append(out, map[string]any{"id": a.ID, "artistName": a.Name, "foreignArtistId": mb[a.ID], "monitored": a.Monitored, "path": nullStringJSON(a.Path), "rootFolderId": a.RootFolderID, "qualityProfileId": a.QualityProfileID.Int64, "added": a.Added.UTC().Format(time.RFC3339)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) ApiAddArtist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MBID             string `json:"foreignArtistId"`
		RootFolderID     int64  `json:"rootFolderId"`
		QualityProfileID int64  `json:"qualityProfileId"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.MBID == "" || h.deps.Music == nil {
		apiError(w, http.StatusBadRequest, "foreignArtistId (MusicBrainz id) is required")
		return
	}
	root, profile, err := h.defaults(r.Context(), "music", body.RootFolderID, body.QualityProfileID)
	if err != nil {
		apiError(w, http.StatusBadRequest, "%v", err)
		return
	}
	id, err := h.deps.Music.AddArtistByMBID(r.Context(), body.MBID, root, profile)
	if err != nil {
		apiError(w, http.StatusBadGateway, "%v", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "foreignArtistId": body.MBID})
}

func albumJSON(a store.AlbumSummary, mbid string) map[string]any {
	return map[string]any{
		"id": a.ID, "title": a.Title, "artistName": a.ArtistName, "foreignAlbumId": mbid, "year": a.Year.Int64, "monitored": a.Monitored,
		"path": nullStringJSON(a.Path), "rootFolderId": a.RootFolderID, "added": a.Added.UTC().Format(time.RFC3339),
	}
}

func (h *handler) ApiListAlbums(w http.ResponseWriter, r *http.Request) {
	albums, err := store.ListAlbums(r.Context(), h.deps.DB)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	mb := externalIDs(r.Context(), h.deps.DB, "album", "musicbrainz", "albums", "id")
	out := make([]map[string]any, 0, len(albums))
	for _, a := range albums {
		out = append(out, albumJSON(a, mb[a.ID]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) ApiGetAlbum(w http.ResponseWriter, r *http.Request) {
	if id, ok := apiPathID(w, r); ok {
		h.writeAlbum(w, r, id, http.StatusOK)
	}
}

func (h *handler) writeAlbum(w http.ResponseWriter, r *http.Request, id int64, status int) {
	d, found, err := store.GetAlbumDetail(r.Context(), h.deps.DB, id)
	if err != nil || !found {
		apiError(w, http.StatusNotFound, "album %d not found", id)
		return
	}
	mb := externalIDs(r.Context(), h.deps.DB, "album", "musicbrainz", "albums", "id")
	out := albumJSON(d.AlbumSummary, mb[d.ID])
	out["albumType"], out["overview"], out["genres"], out["artistId"] = d.AlbumType, nullStringJSON(d.Overview), d.Genres, d.ArtistID.Int64
	tracks, _ := store.ListTracksForAlbum(r.Context(), h.deps.DB, id)
	list := make([]map[string]any, 0, len(tracks))
	for _, t := range tracks {
		list = append(list, map[string]any{"id": t.ID, "mediumNumber": t.MediumNumber, "trackNumber": t.TrackNumber, "title": t.Title, "durationMs": t.DurationMs.Int64, "hasFile": t.HasFile, "size": t.FileSize.Int64, "quality": t.Quality.Key()})
	}
	out["tracks"] = list
	writeJSON(w, status, out)
}

func (h *handler) ApiAddAlbum(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MBID     string `json:"foreignAlbumId"`
		ArtistID int64  `json:"artistId"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.MBID == "" || body.ArtistID <= 0 || h.deps.Music == nil {
		apiError(w, http.StatusBadRequest, "foreignAlbumId (MusicBrainz release group id) and artistId are required")
		return
	}
	id, err := h.deps.Music.AddAlbumByMBID(r.Context(), body.MBID, body.ArtistID)
	if err != nil && id == 0 {
		apiError(w, http.StatusBadGateway, "%v", err)
		return
	}
	h.recordItemEvent(r, store.EventAdded, "music", 0, 0, id, "Added through the API", "api")
	h.writeAlbum(w, r, id, http.StatusCreated)
}

func (h *handler) ApiUpdateAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := apiPathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Monitored *bool `json:"monitored"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Monitored != nil {
		if err := store.UpdateAlbumMonitored(r.Context(), h.deps.DB, id, *body.Monitored); err != nil {
			apiError(w, http.StatusInternalServerError, "%v", err)
			return
		}
	}
	h.ApiGetAlbum(w, r)
}

func (h *handler) ApiDeleteAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := apiPathID(w, r)
	if !ok {
		return
	}
	if h.deps.Import == nil {
		apiError(w, http.StatusServiceUnavailable, "import service isn't available")
		return
	}
	if err := h.deps.Import.DeleteAlbum(r.Context(), id, r.URL.Query().Get("deleteFiles") == "true"); err != nil && !errors.Is(err, sync.ErrUnsafeDelete) {
		apiError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) ApiLookupArtist(w http.ResponseWriter, r *http.Request) {
	term := strings.TrimSpace(r.URL.Query().Get("term"))
	if term == "" || h.deps.MusicBrainz == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	results, err := h.deps.MusicBrainz.SearchArtist(r.Context(), term)
	if err != nil {
		apiError(w, http.StatusBadGateway, "%v", err)
		return
	}
	out := make([]map[string]any, 0, len(results))
	for _, a := range results {
		out = append(out, map[string]any{"foreignArtistId": a.ID, "artistName": a.Name, "disambiguation": a.Disambiguation, "type": a.Type})
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- queue, history, profiles, folders, clients, health, commands

func grabJSON(g store.Grab) map[string]any {
	return map[string]any{
		"id": g.ID, "title": g.ReleaseTitle, "indexer": g.Indexer, "protocol": g.Protocol, "size": g.Size.Int64, "status": g.Status, "statusMessage": nullStringJSON(g.StatusMessage),
		"downloadClient": g.DownloadClient, "downloadId": nullStringJSON(g.DownloadClientID), "grabbedBy": g.GrabbedBy,
		"movieId": g.MovieID.Int64, "seriesId": g.SeriesID.Int64, "seasonNumber": g.SeasonNumber.Int64, "episodeNumber": g.EpisodeNumber.Int64, "albumId": g.AlbumID.Int64,
		"added": g.Added.UTC().Format(time.RFC3339),
	}
}

func (h *handler) ApiQueue(w http.ResponseWriter, r *http.Request) {
	grabs, err := store.ListGrabs(r.Context(), h.deps.DB)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	out := make([]map[string]any, 0)
	for _, g := range grabs {
		if r.URL.Query().Get("all") != "true" && (g.Status == "imported" || g.Status == "removed") {
			continue
		}
		out = append(out, grabJSON(g))
	}
	writeJSON(w, http.StatusOK, map[string]any{"records": out, "totalRecords": len(out)})
}

func (h *handler) ApiHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(q.Get("pageSize"))
	if size <= 0 || size > 1000 {
		size = 50
	}
	events, total, err := store.ListHistory(r.Context(), h.deps.DB, store.HistoryFilter{Event: q.Get("eventType"), MediaType: q.Get("mediaType"), Search: q.Get("q"), Limit: size, Offset: (page - 1) * size})
	if err != nil {
		apiError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	records := make([]map[string]any, 0, len(events))
	for _, e := range events {
		records = append(records, map[string]any{"id": e.ID, "eventType": e.Event, "mediaType": e.MediaType, "movieId": e.MovieID.Int64, "seriesId": e.SeriesID.Int64, "albumId": e.AlbumID.Int64,
			"title": e.Title, "detail": e.Detail, "source": e.Source, "quality": e.Quality, "date": e.Added.UTC().Format(time.RFC3339)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"page": page, "pageSize": size, "totalRecords": total, "records": records})
}

func (h *handler) ApiQualityProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := store.ListQualityProfiles(r.Context(), h.deps.DB)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	out := make([]map[string]any, 0, len(profiles))
	for _, p := range profiles {
		items, _ := store.GetQualityProfileItems(r.Context(), h.deps.DB, p.ID)
		out = append(out, map[string]any{"id": p.ID, "name": p.Name, "isDefault": p.IsDefault, "upgradeAllowed": p.UpgradeAllowed, "cutoff": p.Cutoff, "items": items})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) ApiRootFolders(w http.ResponseWriter, r *http.Request) {
	out := make([]map[string]any, 0)
	for _, mediaType := range []string{"movie", "series", "music"} {
		folders, err := store.ListRootFolders(r.Context(), h.deps.DB, mediaType)
		if err != nil {
			continue
		}
		for _, f := range folders {
			out = append(out, map[string]any{"id": f.ID, "path": f.Path, "mediaType": f.MediaType})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) ApiDownloadClients(w http.ResponseWriter, r *http.Request) {
	clients, err := store.ListDownloadClients(r.Context(), h.deps.DB)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	out := make([]map[string]any, 0, len(clients))
	for _, c := range clients {
		out = append(out, map[string]any{"id": c.ID, "name": c.Name, "implementation": c.Implementation, "protocol": c.Protocol(), "enable": c.Enabled, "priority": c.Priority, "host": c.BaseURL, "category": c.Category})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) ApiHealth(w http.ResponseWriter, r *http.Request) {
	out := make([]map[string]any, 0)
	if h.deps.Health != nil {
		issues, _ := h.deps.Health.Last()
		for _, i := range issues {
			out = append(out, map[string]any{"type": i.Level, "message": i.Message, "wikiUrl": "", "fix": i.Fix})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// apiCommands maps Sonarr/Radarr command names to tasks and searches.
var apiCommands = map[string]string{
	"RssSync": "RSS sync", "RefreshMonitoredDownloads": "Refresh download queue", "Backup": "Backup", "CheckHealth": "Health check",
	"RescanFolders": "Library scan", "LibraryScan": "Library scan", "ApplicationUpdateCheck": "Update check",
}

func (h *handler) ApiCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string  `json:"name"`
		MovieIDs []int64 `json:"movieIds"`
		SeriesID int64   `json:"seriesId"`
		AlbumIDs []int64 `json:"albumIds"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	started := map[string]any{"name": body.Name, "status": "started", "queued": time.Now().UTC().Format(time.RFC3339)}
	switch body.Name {
	case "MoviesSearch":
		if h.deps.Search == nil {
			apiError(w, http.StatusServiceUnavailable, "search isn't available")
			return
		}
		for _, id := range body.MovieIDs {
			go h.deps.Search.SearchMovie(context.Background(), id)
		}
	case "SeriesSearch":
		if h.deps.Search == nil || body.SeriesID <= 0 {
			apiError(w, http.StatusBadRequest, "seriesId is required")
			return
		}
		go h.deps.Search.SearchSeries(context.Background(), body.SeriesID, nil)
	case "AlbumSearch":
		if h.deps.Search == nil {
			apiError(w, http.StatusServiceUnavailable, "search isn't available")
			return
		}
		for _, id := range body.AlbumIDs {
			go h.deps.Search.SearchAlbum(context.Background(), id)
		}
	case "MissingMoviesSearch", "MissingEpisodeSearch", "MissingAlbumSearch", "CutoffUnmetMoviesSearch", "CutoffUnmetEpisodeSearch":
		if h.deps.Search == nil {
			apiError(w, http.StatusServiceUnavailable, "search isn't available")
			return
		}
		media := map[string]string{"MissingMoviesSearch": "movie", "CutoffUnmetMoviesSearch": "movie", "MissingEpisodeSearch": "series", "CutoffUnmetEpisodeSearch": "series", "MissingAlbumSearch": "music"}[body.Name]
		mode := sync.SearchMissing
		if strings.HasPrefix(body.Name, "CutoffUnmet") {
			mode = sync.SearchCutoff
		}
		if !h.deps.Search.StartMissingSearch(media, mode) {
			started["status"] = "already running"
		}
	default:
		task, ok := apiCommands[body.Name]
		if !ok || h.deps.Tasks == nil {
			apiError(w, http.StatusBadRequest, "unknown command %q", body.Name)
			return
		}
		if err := h.deps.Tasks.RunNow(r.Context(), task); err != nil {
			started["status"] = err.Error()
		}
	}
	writeJSON(w, http.StatusCreated, started)
}

func (h *handler) ApiCommands(w http.ResponseWriter, r *http.Request) {
	out := make([]map[string]any, 0)
	if h.deps.Tasks != nil {
		for _, st := range h.deps.Tasks.Statuses() {
			status := "completed"
			switch {
			case st.Running:
				status = "started"
			case st.LastError != "":
				status = "failed"
			case st.LastRun.IsZero():
				status = "queued"
			}
			out = append(out, map[string]any{"name": st.Name, "status": status, "lastExecution": st.LastRun.UTC().Format(time.RFC3339), "nextExecution": st.NextRun.UTC().Format(time.RFC3339), "duration": st.LastDuration.String(), "message": st.LastError})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ApiDocs is GET /api: what the API offers.
func (h *handler) ApiDocs(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, "api_docs", map[string]any{"Active": "", "PageTitle": "API", "Version": version.Commit})
}

// excludeItem puts a movie or series on the import-list exclusion list
// before it's deleted, so no list adds it back.
func (h *handler) excludeItem(r *http.Request, mediaType string, id int64) {
	ctx := r.Context()
	var tmdb, title string
	switch mediaType {
	case "movie":
		tmdb = externalIDs(ctx, h.deps.DB, "movie", "tmdb", "movies", "movie_metadata_id")[id]
		if d, found, err := store.GetMovieDetail(ctx, h.deps.DB, id); err == nil && found {
			title = d.Title
		}
	case "series":
		tmdb = externalIDs(ctx, h.deps.DB, "series", "tmdb", "series", "series_metadata_id")[id]
		if d, found, err := store.GetSeriesDetail(ctx, h.deps.DB, id); err == nil && found {
			title = d.Title
		}
	}
	if n, err := strconv.Atoi(tmdb); err == nil && n > 0 {
		_ = store.AddExclusion(ctx, h.deps.DB, store.Exclusion{MediaType: mediaType, TMDBID: n, Title: title})
	}
}

// ApiQueueDelete is Sonarr's DELETE /api/v3/queue/{id}: removeFromClient
// (default true), blocklist, and skipRedownload.
func (h *handler) ApiQueueDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid queue id")
		return
	}
	g, found, err := store.GetGrab(r.Context(), h.deps.DB, id)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	if !found {
		apiError(w, http.StatusNotFound, "queue item %d not found", id)
		return
	}
	q := r.URL.Query()
	blocklist := sync.BlocklistNone
	if q.Get("blocklist") == "true" {
		blocklist = sync.BlocklistAndSearch
		if q.Get("skipRedownload") == "true" {
			blocklist = sync.BlocklistOnly
		}
	}
	if _, err := h.deps.Download.RemoveGrab(r.Context(), g, q.Get("removeFromClient") != "false", blocklist); err != nil {
		apiError(w, http.StatusBadGateway, "%v", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *handler) ApiBlocklist(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(q.Get("pageSize"))
	if size <= 0 || size > 1000 {
		size = 50
	}
	entries, total, err := store.ListBlocklist(r.Context(), h.deps.DB, size, (page-1)*size)
	if err != nil {
		apiError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	records := make([]map[string]any, 0, len(entries))
	for _, b := range entries {
		records = append(records, map[string]any{"id": b.ID, "movieId": b.MovieID.Int64, "seriesId": b.SeriesID.Int64, "albumId": b.AlbumID.Int64,
			"seasonNumber": b.SeasonNumber.Int64, "episodeNumber": b.EpisodeNumber.Int64, "sourceTitle": b.SourceTitle, "quality": b.Quality,
			"protocol": b.Protocol, "indexer": b.Indexer, "torrentInfoHash": b.InfoHash.String, "message": b.Message, "date": b.Added.UTC().Format(time.RFC3339)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"page": page, "pageSize": size, "totalRecords": total, "records": records})
}

func (h *handler) ApiBlocklistDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		apiError(w, http.StatusBadRequest, "invalid blocklist id")
		return
	}
	if err := store.DeleteBlocklist(r.Context(), h.deps.DB, id); err != nil {
		apiError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	w.WriteHeader(http.StatusOK)
}
