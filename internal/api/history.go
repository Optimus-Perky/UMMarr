package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// History is Sonarr's History page: every event, newest first, filterable.

type historyEventView struct {
	store.Event
	When     string
	Label    string // the event, worded
	Chip     string // chip class
	ItemLink string // the item's page, when it still exists
}

type historyPageData struct {
	Active, PageTitle  string
	Events             []historyEventView
	Total, Page, Pages int
	PrevPage, NextPage int
	Event, MediaType   string
	Search             string
	Kinds              []string
	SeriesID           int64
	Subject            string
}

var eventLabels = map[string][2]string{
	store.EventGrabbed: {"Grabbed", "chip-info"}, store.EventImported: {"Imported", "chip-good"}, store.EventUpgraded: {"Upgraded", "chip-good"},
	store.EventRenamed: {"Renamed", "chip-muted"}, store.EventDeleted: {"Deleted", "chip-warn"}, store.EventAdded: {"Added", "chip-info"},
	store.EventFailed: {"Download failed", "chip-warn"}, store.EventImportFailed: {"Import failed", "chip-warn"}, store.EventNeedsExtraction: {"Needs extraction", "chip-warn"},
	store.EventMatched: {"Match fixed", "chip-muted"}, store.EventHealth: {"Health", "chip-warn"},
}

var eventKinds = []string{store.EventGrabbed, store.EventImported, store.EventUpgraded, store.EventRenamed, store.EventDeleted, store.EventAdded, store.EventFailed, store.EventImportFailed, store.EventNeedsExtraction, store.EventMatched, store.EventHealth}

func toHistoryEventView(e store.Event) historyEventView {
	v := historyEventView{Event: e, When: e.Added.Local().Format("2 Jan 2006 15:04"), Label: e.Event, Chip: "chip-muted"}
	if l, ok := eventLabels[e.Event]; ok {
		v.Label, v.Chip = l[0], l[1]
	}
	switch {
	case e.MovieID.Valid:
		v.ItemLink = fmt.Sprintf("/movies/%d", e.MovieID.Int64)
	case e.SeriesID.Valid:
		v.ItemLink = fmt.Sprintf("/tv/%d", e.SeriesID.Int64)
	case e.AlbumID.Valid:
		v.ItemLink = fmt.Sprintf("/music/albums/%d", e.AlbumID.Int64)
	}
	return v
}

const historyPageSize = 100

func (h *handler) History(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	seriesID, _ := strconv.ParseInt(q.Get("series"), 10, 64)
	filter := store.HistoryFilter{Event: q.Get("event"), MediaType: q.Get("media"), SeriesID: seriesID, Search: q.Get("q"), Limit: historyPageSize, Offset: (page - 1) * historyPageSize}
	subject := ""
	if seriesID > 0 {
		if d, found, err := store.GetSeriesDetail(r.Context(), h.deps.DB, seriesID); err == nil && found {
			subject = d.Title
		}
	}
	events, total, err := store.ListHistory(r.Context(), h.deps.DB, filter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	views := make([]historyEventView, 0, len(events))
	for _, e := range events {
		views = append(views, toHistoryEventView(e))
	}
	pages := (total + historyPageSize - 1) / historyPageSize
	h.renderPage(w, "history", historyPageData{
		Active: "history", PageTitle: "History", Events: views, Total: total, Page: page, Pages: pages, PrevPage: page - 1, NextPage: page + 1,
		Event: filter.Event, MediaType: filter.MediaType, Search: strings.TrimSpace(filter.Search), Kinds: eventKinds, SeriesID: seriesID, Subject: subject,
	})
}

// recordItemEvent notes something the UI did to one item, naming it from
// the library.
func (h *handler) recordItemEvent(r *http.Request, event, mediaType string, movieID, seriesID, albumID int64, detail, source string) {
	if h.deps.Events == nil {
		return
	}
	e := store.Event{Event: event, MediaType: mediaType, Detail: detail, Source: source}
	ctx := r.Context()
	switch {
	case movieID > 0:
		e.MovieID = sql.NullInt64{Int64: movieID, Valid: true}
		if d, found, err := store.GetMovieDetail(ctx, h.deps.DB, movieID); err == nil && found {
			e.Title = d.Title
			if d.Year.Valid {
				e.Title = fmt.Sprintf("%s (%d)", d.Title, d.Year.Int64)
			}
		}
	case seriesID > 0:
		e.SeriesID = sql.NullInt64{Int64: seriesID, Valid: true}
		if d, found, err := store.GetSeriesDetail(ctx, h.deps.DB, seriesID); err == nil && found {
			e.Title = d.Title
		}
	case albumID > 0:
		e.AlbumID = sql.NullInt64{Int64: albumID, Valid: true}
		var artist, title string
		if err := h.deps.DB.QueryRowContext(ctx, `SELECT am.name, al.title FROM albums al JOIN artist_metadata am ON am.id = al.artist_metadata_id WHERE al.id = ?`, albumID).Scan(&artist, &title); err == nil {
			e.Title = artist + " - " + title
		}
	}
	h.deps.Events.Record(ctx, e)
}
