package api

import (
	"context"
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
	path, items, skipped, err := h.deps.Import.ArtistRenamePreview(r.Context(), artist.ID)
	if err != nil {
		data.Err = err.Error()
	}
	data.Root, data.Items, data.Skipped = path, items, skipped
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
	path, items, skipped, err := h.deps.Import.AlbumRenamePreview(r.Context(), albumID)
	if err != nil {
		data.Err = err.Error()
	}
	data.Root, data.Items, data.Skipped = path, items, skipped
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
	profiles, _ := store.ListQualityProfilesOfKind(r.Context(), h.deps.DB, "music")
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

// trackViews builds an album's track rows. oob marks them for out-of-band
// swaps, which is what the status poll below sends.
func (h *handler) trackViews(ctx context.Context, albumID int64, oob bool) ([]trackView, error) {
	tracks, err := store.ListTracksForAlbum(ctx, h.deps.DB, albumID)
	if err != nil {
		return nil, err
	}
	hasIndexer := h.deps.Indexer.Configured(ctx)
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
			ID: t.ID, AlbumID: albumID, TrackNumber: t.TrackNumber, Title: t.Title, Duration: duration,
			HasFile: t.HasFile, FileStatus: status,
			Quality: t.Quality.String(), ReleaseGroup: t.Quality.ReleaseGroup, Audio: t.MediaInfo.TrackSummary(),
			HasIndexer: hasIndexer, OOB: oob,
		})
	}
	return views, nil
}

// AlbumTrackStatuses is the album page's live poll, the music counterpart
// of SeriesEpisodeStatuses: each track row comes back as an out-of-band
// swap, so a track that finishes downloading turns from Missing to
// Downloaded without reloading the page and losing an open search result.
func (h *handler) AlbumTrackStatuses(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	views, err := h.trackViews(r.Context(), albumID, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	for _, v := range views {
		h.renderPartial(w, "track_row", v)
	}
}

type trackFileView struct {
	store.TrackFileDetail
	SizeHuman string
}

type manageTracksData struct {
	Album  store.AlbumDetail
	Files  []trackFileView
	Tracks []store.TrackDetail
	// Unmatched are audio files in the album folder that no track claims -
	// what a scan left alone because nothing identified them.
	Unmatched []unmatchedFileView
}

type unmatchedFileView struct {
	Path      string
	SizeHuman string
}

// AlbumManageTracksForm is Manage Track Files: every file the album has,
// with the track it's attached to. Unlike Manage Episodes there's no
// quality editor - see store/track_file_edit.go for why.
func (h *handler) AlbumManageTracksForm(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	ctx := r.Context()
	album, found, err := store.GetAlbumDetail(ctx, h.deps.DB, albumID)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	files, err := store.ListTrackFileDetails(ctx, h.deps.DB, albumID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	views := make([]trackFileView, 0, len(files))
	for _, f := range files {
		views = append(views, trackFileView{TrackFileDetail: f, SizeHuman: humanizeBytes(f.Size)})
	}
	tracks, err := store.ListTracksForAlbum(ctx, h.deps.DB, albumID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := manageTracksData{Album: album, Files: views, Tracks: tracks}
	if h.deps.Import != nil {
		if loose, err := h.deps.Import.UnmatchedAlbumFiles(ctx, albumID); err == nil {
			for _, f := range loose {
				data.Unmatched = append(data.Unmatched, unmatchedFileView{Path: f.Path, SizeHuman: humanizeBytes(f.Size)})
			}
		}
	}
	h.renderPartial(w, "album_manage_tracks", data)
}

// AlbumAttachTrackFile matches one of the album folder's unclaimed files to
// a track by hand, leaving the file where it is on disk.
func (h *handler) AlbumAttachTrackFile(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	trackID, err := strconv.ParseInt(r.FormValue("track_id"), 10, 64)
	if err != nil {
		renderInlineError(w, "Pick a track.")
		return
	}
	if err := h.deps.Import.AttachAlbumFile(r.Context(), albumID, trackID, r.FormValue("path")); err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/music/albums/%d?matched=1", albumID))
	w.WriteHeader(http.StatusOK)
}

// AlbumManageTracksApply re-maps files whose track select was changed.
func (h *handler) AlbumManageTracksApply(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	files, err := store.ListTrackFileDetails(ctx, h.deps.DB, albumID)
	if err != nil {
		renderInlineError(w, err.Error())
		return
	}
	remapped := 0
	for _, f := range files {
		v := r.FormValue(fmt.Sprintf("f%d_track", f.ID))
		if v == "" {
			continue
		}
		trackID, err := strconv.ParseInt(v, 10, 64)
		if err != nil || trackID == f.TrackID {
			continue
		}
		if err := store.RemapTrackFile(ctx, h.deps.DB, albumID, f.ID, trackID); err != nil {
			renderInlineError(w, err.Error())
			return
		}
		remapped++
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/music/albums/%d?remapped=%d", albumID, remapped))
	w.WriteHeader(http.StatusOK)
}

// AlbumDeleteTrackFiles removes the ticked files.
func (h *handler) AlbumDeleteTrackFiles(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ids := h.renameFileIDs(r)
	if len(ids) == 0 {
		renderInlineError(w, "Tick at least one file.")
		return
	}
	removed, err := h.deps.Import.DeleteTrackFiles(r.Context(), albumID, ids)
	if err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/music/albums/%d?deleted=%d", albumID, removed))
	w.WriteHeader(http.StatusOK)
}

type albumPassRow struct {
	store.AlbumPassArtist
	MonitoredChip monitoredChipView
}

type albumPassPageData struct {
	Active         string
	PageTitle      string
	Notice         string
	Rows           []albumPassRow
	MonitorOptions []store.MonitorOption
}

// AlbumPass is the music counterpart of Season Pass: every artist with a
// chip per album, so a library's album monitoring can be set from one page.
func (h *handler) AlbumPass(w http.ResponseWriter, r *http.Request) {
	list, err := store.ListAlbumPass(r.Context(), h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]albumPassRow, 0, len(list))
	for _, a := range list {
		rows = append(rows, albumPassRow{AlbumPassArtist: a, MonitoredChip: monitoredChipView{
			Monitored: a.Monitored, ToggleURL: fmt.Sprintf("/music/artists/%d/monitored", a.ID),
			LabelOn: "Monitored", LabelOff: "Unmonitored",
		}})
	}
	notice := ""
	if v := r.URL.Query().Get("saved"); v != "" {
		n, _ := strconv.Atoi(v)
		notice = fmt.Sprintf("Album Pass saved for %d artist(s).", n)
	}
	h.renderPage(w, "album_pass", albumPassPageData{Active: "music", PageTitle: "Album Pass", Notice: notice, Rows: rows, MonitorOptions: store.AlbumMonitorOptions})
}

// AlbumPassToggle flips one album's monitored flag and re-renders its chip.
func (h *handler) AlbumPassToggle(w http.ResponseWriter, r *http.Request) {
	albumID, err := strconv.ParseInt(r.URL.Query().Get("album"), 10, 64)
	artistID, err2 := strconv.ParseInt(r.URL.Query().Get("artist"), 10, 64)
	if err != nil || err2 != nil {
		http.Error(w, "invalid artist or album", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	album, found, err := store.GetAlbumDetail(ctx, h.deps.DB, albumID)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	monitored := !album.Monitored
	if err := store.UpdateAlbumMonitored(ctx, h.deps.DB, albumID, monitored); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	year := 0
	if album.Year.Valid {
		year = int(album.Year.Int64)
	}
	h.renderPartial(w, "album_pass_chip", store.AlbumPassAlbum{
		ID: albumID, ArtistID: artistID, Title: album.Title, Year: year, Monitored: monitored,
	})
}

// AlbumPassSave applies the bar at the bottom to every ticked artist.
func (h *handler) AlbumPassSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ids := selectedIDs(r)
	if len(ids) == 0 {
		renderInlineError(w, "Tick at least one artist.")
		return
	}
	ctx := r.Context()
	for _, id := range ids {
		switch r.FormValue("monitored") {
		case "true", "false":
			if err := store.UpdateArtistMonitored(ctx, h.deps.DB, id, r.FormValue("monitored") == "true"); err != nil {
				renderInlineError(w, err.Error())
				return
			}
		}
		if option := r.FormValue("monitor"); option != "" {
			if err := store.ApplyAlbumMonitorOption(ctx, h.deps.DB, id, option); err != nil {
				renderInlineError(w, err.Error())
				return
			}
		}
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/music/albumpass?saved=%d", len(ids)))
	w.WriteHeader(http.StatusOK)
}

type rematchData struct {
	Album  store.AlbumDetail
	Report sync.RematchReport
	Err    string
}

// AlbumRematchPreview shows where an album's files say they belong, for a
// library matched by position before UMMarr read tags.
func (h *handler) AlbumRematchPreview(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	album, found, err := store.GetAlbumDetail(r.Context(), h.deps.DB, albumID)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	data := rematchData{Album: album}
	report, err := h.deps.Import.RematchPreview(r.Context(), albumID)
	if err != nil {
		data.Err = err.Error()
	}
	data.Report = report
	h.renderPartial(w, "album_rematch", data)
}

// AlbumRematchApply moves the ticked files onto the tracks their tags name.
func (h *handler) AlbumRematchApply(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ids := h.renameFileIDs(r)
	if len(ids) == 0 {
		renderInlineError(w, "Tick at least one file.")
		return
	}
	moved, err := h.deps.Import.ApplyRematch(r.Context(), albumID, ids)
	if err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/music/albums/%d?rematched=%d", albumID, moved))
	w.WriteHeader(http.StatusOK)
}

type releaseChoicesData struct {
	Album   store.AlbumDetail
	Choices []sync.ReleaseChoice
	Err     string
}

// AlbumReleasePicker lists the release group's releases so the right
// edition can be chosen - the 13-track UK CD rather than the 16-track
// Japanese one UMMarr happened to pick.
func (h *handler) AlbumReleasePicker(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	album, found, err := store.GetAlbumDetail(r.Context(), h.deps.DB, albumID)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	data := releaseChoicesData{Album: album}
	if h.deps.Music == nil {
		data.Err = "MusicBrainz isn't configured."
		h.renderPartial(w, "album_releases", data)
		return
	}
	choices, err := h.deps.Music.ReleaseChoices(r.Context(), albumID)
	if err != nil {
		data.Err = err.Error()
	}
	data.Choices = choices
	h.renderPartial(w, "album_releases", data)
}

// AlbumChooseRelease switches the album to the chosen release.
func (h *handler) AlbumChooseRelease(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := h.deps.Music.ChooseRelease(r.Context(), albumID, r.FormValue("release")); err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", fmt.Sprintf("/music/albums/%d?release=1", albumID))
	w.WriteHeader(http.StatusOK)
}
