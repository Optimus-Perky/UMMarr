package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// Activity -> Manual Import, as in Sonarr and Radarr.

type manualImportChoice struct {
	Token, Label string // Token ends "[m12]", "[s3]" or "[a40]"
}

type manualImportTrack struct {
	ID       int64
	Label    string
	Selected bool
}

type manualImportRow struct {
	Index      int
	Path       string
	Size       string
	Item       string // a choice token, or "" when unknown
	Season     string
	Episodes   string
	IsAlbum    bool
	Tracks     []manualImportTrack
	Quality    string
	Rejections []string
}

type manualImportData struct {
	Folder    string
	OnlyFile  string
	GrabID    int64
	GrabTitle string
	Error     string
	Rows      []manualImportRow
	Choices   []manualImportChoice
	Qualities []string
	Results   []sync.ManualImportResult
}

func movieToken(id int64) string  { return fmt.Sprintf("[m%d]", id) }
func seriesToken(id int64) string { return fmt.Sprintf("[s%d]", id) }
func albumToken(id int64) string  { return fmt.Sprintf("[a%d]", id) }

var choiceToken = regexp.MustCompile(`\[([msa])(\d+)\]\s*$`)

// manualImportChoices is every movie, series and album, for the item box.
func (h *handler) manualImportChoices(ctx context.Context) ([]manualImportChoice, map[string]string, error) {
	labels := map[string]string{}
	var out []manualImportChoice
	add := func(token, label string) {
		out = append(out, manualImportChoice{Token: label + " " + token, Label: label})
		labels[token] = label + " " + token
	}
	movies, err := store.ListMovies(ctx, h.deps.DB)
	if err != nil {
		return nil, nil, err
	}
	for _, m := range movies {
		label := "Movie: " + m.Title
		if m.Year.Valid {
			label += fmt.Sprintf(" (%d)", m.Year.Int64)
		}
		add(movieToken(m.ID), label)
	}
	series, err := store.ListSeries(ctx, h.deps.DB)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range series {
		add(seriesToken(s.ID), "Series: "+s.Title)
	}
	albums, err := store.ListAlbums(ctx, h.deps.DB)
	if err != nil {
		return nil, nil, err
	}
	for _, a := range albums {
		add(albumToken(a.ID), "Album: "+a.ArtistName+" - "+a.Title)
	}
	return out, labels, nil
}

func (h *handler) albumTracks(ctx context.Context, albumID, selected int64) []manualImportTrack {
	_, tracks, err := store.FindImportRelease(ctx, h.deps.DB, albumID)
	if err != nil {
		return nil
	}
	out := make([]manualImportTrack, 0, len(tracks))
	for i, t := range tracks {
		out = append(out, manualImportTrack{ID: t.ID, Label: fmt.Sprintf("%d. %s", i+1, t.Title), Selected: t.ID == selected})
	}
	return out
}

func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

// manualImportGrab reads ?grab= and finds the grab's download on disk.
func (h *handler) manualImportGrab(r *http.Request, data *manualImportData) *store.Grab {
	raw := r.FormValue("grab")
	if raw == "" {
		return nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		data.Error = "That isn't a queue item."
		return nil
	}
	g, found, err := store.GetGrab(r.Context(), h.deps.DB, id)
	if err != nil || !found {
		data.Error = "That queue item no longer exists."
		return nil
	}
	data.GrabID, data.GrabTitle = g.ID, g.ReleaseTitle
	if data.Folder == "" && h.deps.Download != nil {
		folder, only, err := h.deps.Download.GrabFolder(r.Context(), g)
		if err != nil {
			data.Error = "Couldn't find the download: " + err.Error()
			return &g
		}
		data.Folder, data.OnlyFile = folder, only
	}
	return &g
}

// ManualImport shows the folder's files with UMMarr's guesses.
func (h *handler) ManualImport(w http.ResponseWriter, r *http.Request) {
	data := manualImportData{Folder: strings.TrimSpace(r.FormValue("path")), OnlyFile: r.FormValue("file")}
	grab := h.manualImportGrab(r, &data)
	page := activityPageData{Active: "activity", PageTitle: "Manual Import", Tab: "manual-import", Tabs: activityTabs, ManualImport: &data}
	if data.Folder == "" || data.Error != "" || h.deps.Import == nil {
		h.renderPage(w, "activity", page)
		return
	}
	items, err := h.deps.Import.ManualImportScan(r.Context(), data.Folder, grab)
	if err != nil {
		data.Error = err.Error()
		h.renderPage(w, "activity", page)
		return
	}
	choices, labels, err := h.manualImportChoices(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.Choices, data.Qualities = choices, releaseparse.AllQualities
	for _, it := range items {
		if data.OnlyFile != "" && it.Path != data.OnlyFile {
			continue
		}
		row := manualImportRow{Index: len(data.Rows), Path: it.Path, Size: importer.FormatBytes(it.Size), Quality: it.Quality, Rejections: it.Rejections}
		switch it.Kind {
		case sync.ManualMovie:
			row.Item = labels[movieToken(it.MovieID)]
		case sync.ManualSeries:
			row.Item = labels[seriesToken(it.SeriesID)]
			if len(it.Episodes) > 0 {
				row.Season, row.Episodes = strconv.Itoa(it.Season), joinInts(it.Episodes)
			}
		case sync.ManualTrack:
			row.Item, row.IsAlbum = labels[albumToken(it.AlbumID)], true
			row.Tracks = h.albumTracks(r.Context(), it.AlbumID, it.TrackID)
		}
		data.Rows = append(data.Rows, row)
	}
	h.renderPage(w, "activity", page)
}

// ManualImportTracks is the track list for the album chosen on a row.
func (h *handler) ManualImportTracks(w http.ResponseWriter, r *http.Request) {
	index, _ := strconv.Atoi(r.FormValue("index"))
	m := choiceToken.FindStringSubmatch(r.FormValue(fmt.Sprintf("item_%d", index)))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if m == nil || m[1] != "a" {
		fmt.Fprint(w, "")
		return
	}
	albumID, _ := strconv.ParseInt(m[2], 10, 64)
	h.renderPartial(w, "manual_import_tracks", manualImportRow{Index: index, IsAlbum: true, Tracks: h.albumTracks(r.Context(), albumID, 0)})
}

// ManualImportSubmit imports the ticked rows.
func (h *handler) ManualImportSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data := manualImportData{Folder: strings.TrimSpace(r.FormValue("path"))}
	grab := h.manualImportGrab(r, &data)
	page := activityPageData{Active: "activity", PageTitle: "Manual Import", Tab: "manual-import", Tabs: activityTabs, ManualImport: &data}
	if data.Folder == "" || h.deps.Import == nil {
		data.Error = "Choose a folder first."
		h.renderPage(w, "activity", page)
		return
	}
	mode := sync.ImportCopy
	if r.FormValue("mode") == sync.ImportMove {
		mode = sync.ImportMove
	}
	var items []sync.ManualImportItem
	for _, raw := range r.Form["selected"] {
		i, err := strconv.Atoi(raw)
		if err != nil {
			continue
		}
		field := func(name string) string { return strings.TrimSpace(r.FormValue(fmt.Sprintf("%s_%d", name, i))) }
		item := sync.ManualImportItem{Path: field("path"), Quality: field("quality")}
		if m := choiceToken.FindStringSubmatch(field("item")); m != nil {
			id, _ := strconv.ParseInt(m[2], 10, 64)
			switch m[1] {
			case "m":
				item.Kind, item.MovieID = sync.ManualMovie, id
			case "s":
				item.Kind, item.SeriesID = sync.ManualSeries, id
				item.Season, _ = strconv.Atoi(field("season"))
				for _, e := range strings.FieldsFunc(field("episodes"), func(r rune) bool { return r == ',' || r == ' ' }) {
					if n, err := strconv.Atoi(strings.TrimLeft(strings.ToUpper(e), "E")); err == nil {
						item.Episodes = append(item.Episodes, n)
					}
				}
			case "a":
				item.Kind, item.AlbumID = sync.ManualTrack, id
				item.TrackID, _ = strconv.ParseInt(field("track"), 10, 64)
			}
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		data.Error = "Tick at least one file to import."
		h.renderPage(w, "activity", page)
		return
	}
	data.Results = h.deps.Import.ManualImport(r.Context(), data.Folder, mode, items, grab)
	h.renderPage(w, "activity", page)
}
