package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// The artist page, with the toolbar movies and series already have.

type artistAlbumView struct {
	store.AlbumSummary
	HasFiles bool
	Tracks   string // "9 / 12"
}

type artistPageData struct {
	Active      string
	PageTitle   string
	Artist      store.ArtistDetail
	Albums      []artistAlbumView
	SizeHuman   string
	HasIndexer  bool
	Scanned     string
	Profiles    []store.QualityProfile
	RootFolders []store.RootFolder
}

func (h *handler) artistFromPath(w http.ResponseWriter, r *http.Request) (store.ArtistDetail, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid artist id", http.StatusBadRequest)
		return store.ArtistDetail{}, false
	}
	artist, found, err := store.GetArtistDetail(r.Context(), h.deps.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return store.ArtistDetail{}, false
	}
	if !found {
		http.NotFound(w, r)
		return store.ArtistDetail{}, false
	}
	return artist, true
}

// ArtistDetail is the artist page.
func (h *handler) ArtistDetail(w http.ResponseWriter, r *http.Request) {
	artist, ok := h.artistFromPath(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	albums, err := store.ListAlbumsForArtist(ctx, h.deps.DB, artist.ArtistMetadataID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := artistPageData{Active: "music", PageTitle: artist.Name, Artist: artist, SizeHuman: humanizeBytes(artist.SizeOnDisk)}
	for _, album := range albums {
		view := artistAlbumView{AlbumSummary: album}
		if wanted, err := store.GetWantedAlbum(ctx, h.deps.DB, album.ID); err == nil {
			view.HasFiles = wanted.FileCount() > 0
			view.Tracks = fmt.Sprintf("%d / %d", wanted.FileCount(), len(wanted.Tracks))
		}
		data.Albums = append(data.Albums, view)
	}
	data.HasIndexer = h.deps.Indexer.Configured(ctx)
	data.Profiles, _ = store.ListQualityProfiles(ctx, h.deps.DB)
	data.RootFolders, _ = store.ListRootFolders(ctx, h.deps.DB, "music")
	if n := r.URL.Query().Get("scanned"); n != "" {
		data.Scanned = n
	}
	h.renderPage(w, "artist_detail", data)
}

// ArtistRefresh re-fetches the artist and scans its albums' folders.
func (h *handler) ArtistRefresh(w http.ResponseWriter, r *http.Request) {
	artist, ok := h.artistFromPath(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var problems []string
	if h.deps.Music != nil {
		if err := h.deps.Music.RefreshArtist(ctx, artist.ID); err != nil {
			problems = append(problems, err.Error())
		}
	}
	imported := 0
	if h.deps.Import != nil {
		var err error
		if imported, err = h.deps.Import.ScanArtist(ctx, artist.ID); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		renderInlineError(w, "Refresh failed: "+strings.Join(problems, "; "))
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/music/artists/%d?scanned=%d", artist.ID, imported))
	w.WriteHeader(http.StatusOK)
}

// ArtistSearch searches every monitored album of the artist that has no files.
func (h *handler) ArtistSearch(w http.ResponseWriter, r *http.Request) {
	artist, ok := h.artistFromPath(w, r)
	if !ok {
		return
	}
	if h.deps.Search == nil {
		h.renderSearchReport(w, sync.SearchReport{}, errSearchUnavailable)
		return
	}
	report, err := h.deps.Search.SearchArtist(r.Context(), artist.ID)
	h.renderSearchReport(w, report, err)
}

// ArtistMonitoredToggle switches an artist's monitoring.
func (h *handler) ArtistMonitoredToggle(w http.ResponseWriter, r *http.Request) {
	artist, ok := h.artistFromPath(w, r)
	if !ok {
		return
	}
	monitored := r.FormValue("monitored") != "false"
	if err := store.UpdateArtistMonitored(r.Context(), h.deps.DB, artist.ID, monitored); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	artist.Monitored = monitored
	h.renderPartial(w, "artist_monitored", artist)
}

type artistEditData struct {
	Artist      store.ArtistDetail
	Profiles    []store.QualityProfile
	RootFolders []store.RootFolder
	Error       string
}

// ArtistEditForm is the artist Edit dialog.
func (h *handler) ArtistEditForm(w http.ResponseWriter, r *http.Request) {
	artist, ok := h.artistFromPath(w, r)
	if !ok {
		return
	}
	profiles, _ := store.ListQualityProfiles(r.Context(), h.deps.DB)
	roots, _ := store.ListRootFolders(r.Context(), h.deps.DB, "music")
	h.renderPartial(w, "artist_edit", artistEditData{Artist: artist, Profiles: profiles, RootFolders: roots})
}

// ArtistEditSave saves it.
func (h *handler) ArtistEditSave(w http.ResponseWriter, r *http.Request) {
	artist, ok := h.artistFromPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	monitored := r.FormValue("monitored") == "on"
	edit := store.ArtistEdit{Monitored: &monitored, MonitorAlbums: r.FormValue("monitor_albums") == "on"}
	if v := r.FormValue("quality_profile_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			renderInlineError(w, "Pick a quality profile.")
			return
		}
		edit.QualityProfileID = &id
	}
	if path := strings.TrimSpace(r.FormValue("path")); path != "" && path != artist.Path.String {
		edit.Path = &path
	}
	if err := store.EditArtists(r.Context(), h.deps.DB, []int64{artist.ID}, edit); err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/music/artists/%d", artist.ID))
	w.WriteHeader(http.StatusOK)
}

// ArtistDeleteForm asks before removing an artist.
func (h *handler) ArtistDeleteForm(w http.ResponseWriter, r *http.Request) {
	artist, ok := h.artistFromPath(w, r)
	if !ok {
		return
	}
	h.renderPartial(w, "artist_delete", artist)
}

// ArtistDelete removes an artist, with its files when asked.
func (h *handler) ArtistDelete(w http.ResponseWriter, r *http.Request) {
	artist, ok := h.artistFromPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	deleteFiles := r.FormValue("delete_files") == "on"
	if err := h.deps.Import.DeleteArtist(r.Context(), artist.ID, deleteFiles); err != nil {
		if deleteFiles && strings.Contains(err.Error(), sync.ErrUnsafeDelete.Error()) {
			renderInlineError(w, "The artist was removed, but its folder is outside the library folders so it was left alone.")
			return
		}
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", "/music")
	w.WriteHeader(http.StatusOK)
}

// ArtistMetadata is the artist's Metadata dialog.
func (h *handler) ArtistMetadata(w http.ResponseWriter, r *http.Request) {
	artist, ok := h.artistFromPath(w, r)
	if !ok {
		return
	}
	h.metadataDialog(w, r, "artist", artist.ID, metadataDialogData{Title: artist.Name, Noun: "artist",
		RefreshURL: fmt.Sprintf("/music/artists/%d/refresh", artist.ID), RefreshLabel: "Refresh & Scan",
		RefreshExplanation: "Fetches the artist from MusicBrainz again and scans its albums' folders for files.",
		FixMatchURL:        fmt.Sprintf("/music/artists/%d/fix-match", artist.ID)}, "music")
}

// MusicEditorSave is the Music page's mass editor.
func (h *handler) MusicEditorSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ids := selectedIDs(r)
	if len(ids) == 0 {
		renderInlineError(w, "Select at least one artist.")
		return
	}
	var edit store.ArtistEdit
	switch r.FormValue("monitored") {
	case "yes":
		yes := true
		edit.Monitored = &yes
	case "no":
		no := false
		edit.Monitored = &no
	}
	edit.MonitorAlbums = r.FormValue("monitor_albums") == "on"
	if v := r.FormValue("quality_profile_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			renderInlineError(w, "Pick a quality profile.")
			return
		}
		edit.QualityProfileID = &id
	}
	if edit.Monitored == nil && edit.QualityProfileID == nil {
		renderInlineError(w, "Choose something to change.")
		return
	}
	if err := store.EditArtists(r.Context(), h.deps.DB, ids, edit); err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/music?edited=%d", len(ids)))
	w.WriteHeader(http.StatusOK)
}

// MusicEditorDelete removes every selected artist.
func (h *handler) MusicEditorDelete(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ids := selectedIDs(r)
	if len(ids) == 0 {
		renderInlineError(w, "Select at least one artist.")
		return
	}
	deleteFiles := r.FormValue("delete_files") == "on"
	deleted := 0
	var failures []string
	for _, id := range ids {
		if err := h.deps.Import.DeleteArtist(r.Context(), id, deleteFiles); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		deleted++
	}
	if len(failures) > 0 {
		renderInlineError(w, fmt.Sprintf("Removed %d; %d failed: %s", deleted, len(failures), firstFew(failures, 3)))
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/music?deleted=%d", deleted))
	w.WriteHeader(http.StatusOK)
}

// MusicEditorSearch searches every selected artist, in the background.
func (h *handler) MusicEditorSearch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ids := selectedIDs(r)
	if len(ids) == 0 {
		renderInlineError(w, "Select at least one artist.")
		return
	}
	if h.deps.Search == nil {
		renderInlineError(w, errSearchUnavailable.Error())
		return
	}
	search := h.deps.Search
	go func() {
		ctx := context.Background()
		for _, id := range ids {
			if _, err := search.SearchArtist(ctx, id); err != nil {
				log.Printf("mass editor search artist %d: %v", id, err)
			}
		}
	}()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<p class="notice">Searching %d artist(s) in the background - grabs show up in Activity.</p>`, len(ids))
}
