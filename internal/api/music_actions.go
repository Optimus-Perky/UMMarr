package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// The artist and album page actions that TV already had: Organize & Rename,
// a monitoring dialog, and Edit/Delete for an album.

func (h *handler) renameFileIDs(r *http.Request) []int64 {
	var ids []int64
	for _, v := range r.Form["file_id"] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// musicNamingPattern is the album folder + track file templates, shown at
// the top of the rename dialog so it's clear what the new names follow.
func (h *handler) musicNamingPattern(r *http.Request) string {
	naming, err := store.GetNamingConfig(r.Context(), h.deps.DB, "music")
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(naming.AlbumFolderFormat.String, "/") + "/" + naming.TrackFileFormat.String
}

// ArtistRenamePreview is Lidarr's Organize & Rename for a whole artist.
func (h *handler) ArtistRenamePreview(w http.ResponseWriter, r *http.Request) {
	artist, ok := h.artistFromPath(w, r)
	if !ok {
		return
	}
	data := renamePreviewData{Action: fmt.Sprintf("/music/artists/%d/rename", artist.ID), Pattern: h.musicNamingPattern(r)}
	path, items, err := h.deps.Import.ArtistRenamePreview(r.Context(), artist.ID)
	if err != nil {
		data.Err = err.Error()
	}
	data.Root, data.Items = path, items
	h.renderPartial(w, "rename_preview", data)
}

// ArtistRenameFiles renames the ticked files.
func (h *handler) ArtistRenameFiles(w http.ResponseWriter, r *http.Request) {
	artist, ok := h.artistFromPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	renamed, problems, err := h.deps.Import.RenameArtistFiles(r.Context(), artist.ID, h.renameFileIDs(r))
	if err != nil {
		renderInlineError(w, err.Error())
		return
	}
	if len(problems) > 0 {
		renderInlineError(w, fmt.Sprintf("%d renamed. Couldn't rename: %s", renamed, strings.Join(problems, "; ")))
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/music/artists/%d?renamed=%d", artist.ID, renamed))
	w.WriteHeader(http.StatusOK)
}

// AlbumRenamePreview is the same for one album.
func (h *handler) AlbumRenamePreview(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	data := renamePreviewData{Action: fmt.Sprintf("/music/albums/%d/rename", albumID), Pattern: h.musicNamingPattern(r)}
	path, items, err := h.deps.Import.AlbumRenamePreview(r.Context(), albumID)
	if err != nil {
		data.Err = err.Error()
	}
	data.Root, data.Items = path, items
	h.renderPartial(w, "rename_preview", data)
}

// AlbumRenameFiles renames the ticked files of one album.
func (h *handler) AlbumRenameFiles(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	renamed, problems, err := h.deps.Import.RenameAlbumFiles(r.Context(), albumID, h.renameFileIDs(r))
	if err != nil {
		renderInlineError(w, err.Error())
		return
	}
	if len(problems) > 0 {
		renderInlineError(w, fmt.Sprintf("%d renamed. Couldn't rename: %s", renamed, strings.Join(problems, "; ")))
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/music/albums/%d?renamed=%d", albumID, renamed))
	w.WriteHeader(http.StatusOK)
}

type artistMonitorData struct {
	Artist  store.ArtistDetail
	Options []store.MonitorOption
}

// ArtistMonitorForm is Lidarr's artist Monitor dialog.
func (h *handler) ArtistMonitorForm(w http.ResponseWriter, r *http.Request) {
	artist, ok := h.artistFromPath(w, r)
	if !ok {
		return
	}
	h.renderPartial(w, "artist_monitor", artistMonitorData{Artist: artist, Options: store.AlbumMonitorOptions})
}

// ArtistMonitorApply sets which of the artist's albums are monitored.
func (h *handler) ArtistMonitorApply(w http.ResponseWriter, r *http.Request) {
	artist, ok := h.artistFromPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := store.ApplyAlbumMonitorOption(r.Context(), h.deps.DB, artist.ID, r.FormValue("monitor")); err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/music/artists/%d", artist.ID))
	w.WriteHeader(http.StatusOK)
}

type albumEditData struct {
	Album    store.AlbumDetail
	Profiles []store.QualityProfile
	Error    string
}

// AlbumEditForm is the album Edit dialog: monitoring and quality profile,
// which an album inherits from its artist until it's set here.
func (h *handler) AlbumEditForm(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	album, found, err := store.GetAlbumDetail(r.Context(), h.deps.DB, albumID)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	profiles, _ := store.ListQualityProfiles(r.Context(), h.deps.DB)
	h.renderPartial(w, "album_edit", albumEditData{Album: album, Profiles: profiles})
}

// AlbumEditSave saves it.
func (h *handler) AlbumEditSave(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	monitored := r.FormValue("monitored") == "on"
	if err := store.UpdateAlbumMonitored(r.Context(), h.deps.DB, albumID, monitored); err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/music/albums/%d", albumID))
	w.WriteHeader(http.StatusOK)
}

// AlbumDeleteForm asks before removing an album.
func (h *handler) AlbumDeleteForm(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	album, found, err := store.GetAlbumDetail(r.Context(), h.deps.DB, albumID)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	h.renderPartial(w, "album_delete", album)
}

// AlbumDelete removes an album, with its files when asked.
func (h *handler) AlbumDelete(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	album, found, err := store.GetAlbumDetail(r.Context(), h.deps.DB, albumID)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	artistPage := "/music"
	if album.ArtistID.Valid {
		artistPage = fmt.Sprintf("/music/artists/%d", album.ArtistID.Int64)
	}
	deleteFiles := r.FormValue("delete_files") == "on"
	if err := h.deps.Import.DeleteAlbum(r.Context(), albumID, deleteFiles); err != nil {
		if deleteFiles && strings.Contains(err.Error(), sync.ErrUnsafeDelete.Error()) {
			renderInlineError(w, "The album was removed, but its folder is outside the library folders so it was left alone.")
			return
		}
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", artistPage)
	w.WriteHeader(http.StatusOK)
}
