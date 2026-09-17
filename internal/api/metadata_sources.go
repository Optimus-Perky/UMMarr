package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Settings -> Metadata -> Sources, and each item's Metadata dialog: where
// the details come from, and in what order providers are asked.

type metadataProviderView struct {
	ID                          int64
	Key, Name, Supplies, Status string
	Configured, Enabled         bool
	First, Last                 bool
}

type metadataSourceView struct {
	CanAdd                 []store.MetadataImplementation
	MediaType, Label, Note string
	Providers              []metadataProviderView
}

var providerNames = map[string]string{"tmdb": "TMDB", "omdb": "OMDb", "tvmaze": "TVmaze", "musicbrainz": "MusicBrainz", "tvdb": "TheTVDB", "imdb": "IMDb"}

func providerName(key string) string {
	if n, ok := providerNames[key]; ok {
		return n
	}
	return key
}

var mediaLabels = map[string]string{"movie": "Movies", "series": "TV", "music": "Music"}

func (h *handler) metadataSourceViews(ctx context.Context) ([]metadataSourceView, error) {
	providers, err := store.ListMetadataProviders(ctx, h.deps.DB)
	if err != nil {
		return nil, err
	}
	byMedia := map[string][]store.MetadataProvider{}
	for _, p := range providers {
		byMedia[p.MediaType] = append(byMedia[p.MediaType], p)
	}
	var out []metadataSourceView
	for _, mediaType := range store.MetadataMediaTypes {
		v := metadataSourceView{MediaType: mediaType, Label: mediaLabels[mediaType]}
		switch mediaType {
		case "series":
			v.Note = "The first enabled provider also decides which episodes each season has. TVmaze sometimes splits a double-length episode in two, so putting it first can add episodes nobody has."
		case "movie":
			v.Note = "Ratings are collected from every provider whatever the order."
		}
		rows := byMedia[mediaType]
		for i, p := range rows {
			status, ok := h.providerStatus(p)
			impl, _ := store.FindMetadataImplementation(p.Implementation)
			v.Providers = append(v.Providers, metadataProviderView{ID: p.ID, Key: p.Implementation, Name: p.Name, Supplies: impl.Supplies,
				Status: status, Configured: ok, Enabled: p.Enabled, First: i == 0, Last: i == len(rows)-1})
		}
		for _, impl := range store.MetadataImplementations {
			if slices.Contains(impl.MediaTypes, mediaType) {
				v.CanAdd = append(v.CanAdd, impl)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// providerStatus says whether a provider is ready to be asked.
func (h *handler) providerStatus(p store.MetadataProvider) (string, bool) {
	impl, known := store.FindMetadataImplementation(p.Implementation)
	if !known {
		return "UMMarr has no such provider", false
	}
	if !p.Enabled {
		return "Disabled", false
	}
	if !impl.NeedsKey {
		return "No key needed", true
	}
	if p.APIKey != "" {
		return "Key set", true
	}
	switch p.Implementation {
	case "tmdb":
		if h.deps.TMDB != nil {
			return "Key from the environment", true
		}
		return "Needs a key", false
	case "omdb":
		if h.deps.OMDb != nil {
			return "Key from the environment", true
		}
		return "Needs a key", false
	}
	return "Needs a key", false
}

// MoveMetadataSource moves a provider up or down its media type's order.
func (h *handler) MoveMetadataSource(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid provider id", http.StatusBadRequest)
		return
	}
	if err := store.MoveMetadataProvider(r.Context(), h.deps.DB, id, r.FormValue("direction") == "up"); err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", "/settings/metadata")
	w.WriteHeader(http.StatusOK)
}

type metadataProviderFormData struct {
	Provider       store.MetadataProvider
	Implementation store.MetadataImplementation
	MediaLabel     string
	IsNew          bool
	KeySet         bool
	Error          string
}

// NewMetadataProviderForm is the Add dialog for one implementation.
func (h *handler) NewMetadataProviderForm(w http.ResponseWriter, r *http.Request) {
	mediaType, key := r.FormValue("media_type"), r.FormValue("implementation")
	impl, ok := store.FindMetadataImplementation(key)
	if !ok || !store.ValidMetadataMediaType(mediaType) {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}
	h.renderPartial(w, "metadata_provider_form", metadataProviderFormData{IsNew: true, Implementation: impl, MediaLabel: mediaLabels[mediaType],
		Provider: store.MetadataProvider{MediaType: mediaType, Implementation: key, Name: impl.Name, Enabled: true, Extra: map[string]string{}}})
}

// EditMetadataProviderForm edits a saved provider.
func (h *handler) EditMetadataProviderForm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "metadata provider")
	if !ok {
		return
	}
	p, err := store.GetMetadataProvider(r.Context(), h.deps.DB, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	impl, _ := store.FindMetadataImplementation(p.Implementation)
	h.renderPartial(w, "metadata_provider_form", metadataProviderFormData{Provider: p, Implementation: impl,
		MediaLabel: mediaLabels[p.MediaType], KeySet: p.APIKey != ""})
}

// SaveMetadataProvider adds or updates a provider.
func (h *handler) SaveMetadataProvider(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p := store.MetadataProvider{
		MediaType: r.FormValue("media_type"), Implementation: r.FormValue("implementation"),
		Name: strings.TrimSpace(r.FormValue("name")), Enabled: r.FormValue("enabled") == "on",
		APIKey: strings.TrimSpace(r.FormValue("api_key")), Extra: map[string]string{},
	}
	if id, err := strconv.ParseInt(r.FormValue("id"), 10, 64); err == nil {
		p.ID = id
	}
	impl, known := store.FindMetadataImplementation(p.Implementation)
	if !known {
		http.Error(w, "unknown provider", http.StatusBadRequest)
		return
	}
	for _, f := range impl.Fields {
		if v := strings.TrimSpace(r.FormValue("extra_" + f.Key)); v != "" {
			p.Extra[f.Key] = v
		}
	}
	if p.Name == "" {
		p.Name = impl.Name
	}
	if _, err := store.SaveMetadataProvider(r.Context(), h.deps.DB, p); err != nil {
		h.renderPartial(w, "metadata_provider_form", metadataProviderFormData{Provider: p, Implementation: impl,
			MediaLabel: mediaLabels[p.MediaType], IsNew: p.ID == 0, Error: err.Error()})
		return
	}
	w.Header().Set("HX-Redirect", "/settings/metadata")
	w.WriteHeader(http.StatusOK)
}

// DeleteMetadataProvider removes a provider from a media type's list.
func (h *handler) DeleteMetadataProvider(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "metadata provider")
	if !ok {
		return
	}
	if err := store.DeleteMetadataProvider(r.Context(), h.deps.DB, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/metadata")
	w.WriteHeader(http.StatusOK)
}

// fieldLabels names provenance fields for people.
var fieldLabels = map[string]string{
	"title": "Title", "original_title": "Original title", "overview": "Overview", "genres": "Genres", "images": "Images",
	"runtime": "Runtime", "release_date": "Release date", "status": "Status", "studio": "Studio", "collection_title": "Collection",
	"network": "Network", "first_aired": "First aired", "last_aired": "Last aired", "air_date": "Air date", "name": "Name",
	"ratings": "Ratings", "certification": "Certification", "year": "Year", "album_type": "Album type", "disambiguation": "Disambiguation",
}

func fieldLabel(f string) string {
	if l, ok := fieldLabels[f]; ok {
		return l
	}
	return strings.ReplaceAll(f, "_", " ")
}

type metadataFieldView struct {
	Field, Source, Fetched string
}

type metadataDialogData struct {
	Title, Noun        string
	EntityType         string
	ItemID             int64
	MatchName          string // what it's matched to now
	MatchProvider      string // and where
	RefreshURL         string
	RefreshLabel       string
	FixMatchURL        string
	PosterURL          string   // the poster in use
	Posters            []string // every poster on offer
	PosterChosen       bool     // it was picked by hand
	Fields             []metadataFieldView
	EpisodeFields      []metadataFieldView // for a series: how many episodes took each field from where
	IDs                []metadataFieldView
	Order              string
	RefreshExplanation string
	Notice             string
}

func (h *handler) metadataDialog(w http.ResponseWriter, r *http.Request, entityType string, entityID int64, data metadataDialogData, mediaType string) {
	ctx := r.Context()
	data.EntityType, data.ItemID = entityType, entityID
	prov, err := store.ListFieldProvenance(ctx, h.deps.DB, entityType, metadataEntityID(ctx, h.deps.DB, entityType, entityID))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, p := range prov {
		data.Fields = append(data.Fields, metadataFieldView{Field: fieldLabel(p.Field), Source: providerName(p.Provider), Fetched: p.FetchedAt.Local().Format("2 Jan 2006 15:04")})
	}
	ids, _ := store.ExternalIDsFor(ctx, h.deps.DB, entityType, metadataEntityID(ctx, h.deps.DB, entityType, entityID))
	keys := make([]string, 0, len(ids))
	for k := range ids {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		data.IDs = append(data.IDs, metadataFieldView{Field: providerName(k), Source: ids[k]})
	}
	switch {
	case ids["tmdb"] != "":
		data.MatchProvider, data.MatchName = "TMDB", ids["tmdb"]
	case ids["musicbrainz"] != "":
		data.MatchProvider, data.MatchName = "MusicBrainz", ids["musicbrainz"]
	}
	if orders, err := store.GetMetadataSourceOrders(ctx, h.deps.DB); err == nil {
		names := make([]string, 0, len(orders[mediaType]))
		for _, p := range orders[mediaType] {
			names = append(names, providerName(p))
		}
		data.Order = strings.Join(names, " → ")
	}
	chosen := store.PosterOverride(ctx, h.deps.DB, entityType, entityID)
	data.PosterChosen = chosen != ""
	data.Posters = h.posterChoices(ctx, entityType, entityID, ids)
	if chosen != "" && !slices.Contains(data.Posters, chosen) {
		data.Posters = append([]string{chosen}, data.Posters...)
	}
	data.PosterURL = chosen
	if data.PosterURL == "" && len(data.Posters) > 0 {
		data.PosterURL = data.Posters[0]
	}
	h.renderPartial(w, "metadata_dialog", data)
}

// metadataEntityID maps a library item to the metadata row provenance and
// external ids are recorded against.
func metadataEntityID(ctx context.Context, db *sql.DB, entityType string, id int64) int64 {
	var column, table string
	switch entityType {
	case "movie":
		column, table = "movie_metadata_id", "movies"
	case "series":
		column, table = "series_metadata_id", "series"
	case "artist":
		column, table = "artist_metadata_id", "artists"
	default:
		return id // albums are their own metadata row
	}
	var metadataID int64
	if db.QueryRowContext(ctx, fmt.Sprintf(`SELECT %s FROM %s WHERE id = ?`, column, table), id).Scan(&metadataID) != nil {
		return id
	}
	return metadataID
}

// posterChoices are the posters on offer: what the providers already gave,
// plus every poster TMDB has for the item.
func (h *handler) posterChoices(ctx context.Context, entityType string, entityID int64, ids map[string]string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(urls ...string) {
		for _, u := range urls {
			if u != "" && !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
	}
	add(storedImages(ctx, h.deps.DB, entityType, metadataEntityID(ctx, h.deps.DB, entityType, entityID))...)
	if h.deps.TMDB != nil && ids["tmdb"] != "" {
		if tmdbID, err := strconv.Atoi(ids["tmdb"]); err == nil {
			var posters []string
			var err error
			switch entityType {
			case "movie":
				posters, err = h.deps.TMDB.MoviePosters(ctx, tmdbID)
			case "series":
				posters, err = h.deps.TMDB.SeriesPosters(ctx, tmdbID)
			}
			if err == nil {
				add(posters...)
			}
		}
	}
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

// storedImages reads the images the providers gave for a metadata row.
func storedImages(ctx context.Context, db *sql.DB, entityType string, metadataID int64) []string {
	table := map[string]string{"movie": "movie_metadata", "series": "series_metadata", "artist": "artist_metadata", "album": "albums"}[entityType]
	if table == "" {
		return nil
	}
	var raw string
	if db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COALESCE(images, '[]') FROM %s WHERE id = ?`, table), metadataID).Scan(&raw) != nil {
		return nil
	}
	var urls []string
	_ = json.Unmarshal([]byte(raw), &urls)
	return urls
}

// ChoosePoster records the poster picked in the dialog, as Plex's poster
// picker does, and it survives refreshes.
func (h *handler) ChoosePoster(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	entityType := r.PathValue("type")
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || !slices.Contains([]string{"movie", "series", "artist", "album"}, entityType) {
		http.Error(w, "invalid item", http.StatusBadRequest)
		return
	}
	if err := store.SetPosterOverride(r.Context(), h.deps.DB, entityType, id, r.FormValue("url")); err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
}

func (h *handler) MovieMetadata(w http.ResponseWriter, r *http.Request) {
	movieID, ok := pathID(w, r, "id", "movie")
	if !ok {
		return
	}
	detail, found, err := store.GetMovieDetail(r.Context(), h.deps.DB, movieID)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	h.metadataDialog(w, r, "movie", movieID, metadataDialogData{Title: detail.Title, Noun: "movie",
		RefreshURL: fmt.Sprintf("/movies/%d/refresh-metadata", movieID), RefreshLabel: "Refresh metadata",
		RefreshExplanation: "Fetches the movie from its providers again, in the order above.",
		FixMatchURL:        fmt.Sprintf("/movies/%d/fix-match", movieID)}, "movie")
}

func (h *handler) SeriesMetadata(w http.ResponseWriter, r *http.Request) {
	series, ok := h.seriesNoRedirect(w, r)
	if !ok {
		return
	}
	data := metadataDialogData{Title: series.Title, Noun: "series",
		RefreshURL: fmt.Sprintf("/tv/%d/refresh", series.ID), RefreshLabel: "Refresh & Scan",
		RefreshExplanation: "Fetches the series and its episodes from the providers again, in the order above, then scans its folder.",
		FixMatchURL:        fmt.Sprintf("/tv/%d/fix-match", series.ID)}
	if counts, err := store.EpisodeProvenanceCounts(r.Context(), h.deps.DB, series.ID); err == nil {
		for _, c := range counts {
			data.EpisodeFields = append(data.EpisodeFields, metadataFieldView{Field: fieldLabel(c.Field), Source: providerName(c.Provider), Fetched: fmt.Sprintf("%d episodes", c.Count)})
		}
	}
	h.metadataDialog(w, r, "series", series.ID, data, "series")
}

func (h *handler) AlbumMetadata(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	var title string
	if err := h.deps.DB.QueryRowContext(r.Context(), `SELECT am.name || ' - ' || al.title FROM albums al JOIN artist_metadata am ON am.id = al.artist_metadata_id WHERE al.id = ?`, albumID).Scan(&title); err != nil {
		http.NotFound(w, r)
		return
	}
	h.metadataDialog(w, r, "album", albumID, metadataDialogData{Title: title, Noun: "album",
		RefreshURL: fmt.Sprintf("/music/albums/%d/refresh-metadata", albumID), RefreshLabel: "Refresh metadata",
		RefreshExplanation: "Fetches the album's release group from MusicBrainz again. Its files stay where they are.",
		FixMatchURL:        fmt.Sprintf("/music/albums/%d/fix-match", albumID)}, "music")
}

// MovieRefreshMetadata re-fetches a movie's details.
func (h *handler) MovieRefreshMetadata(w http.ResponseWriter, r *http.Request) {
	movieID, ok := pathID(w, r, "id", "movie")
	if !ok {
		return
	}
	if h.deps.Movies == nil {
		renderInlineError(w, "Movie metadata isn't available - TMDB isn't configured.")
		return
	}
	if err := h.deps.Movies.Refresh(r.Context(), movieID); err != nil {
		renderInlineError(w, "Refresh failed: "+err.Error())
		return
	}
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
}

// AlbumRefreshMetadata re-fetches an album's details.
func (h *handler) AlbumRefreshMetadata(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	if h.deps.Music == nil {
		renderInlineError(w, "Music metadata isn't available.")
		return
	}
	if err := h.deps.Music.RefreshAlbum(r.Context(), albumID); err != nil {
		renderInlineError(w, "Refresh failed: "+err.Error())
		return
	}
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
}
