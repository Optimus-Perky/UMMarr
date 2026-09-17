package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

// activityTabs are the Activity page's tabs, as in Sonarr (History has its
// own page here).
var activityTabs = []struct{ Key, Label string }{{"queue", "Queue"}, {"manual-import", "Manual Import"}, {"blocklist", "Blocklist"}}

type activityPageData struct {
	Active    string
	PageTitle string
	Tab       string
	Tabs      []struct{ Key, Label string }

	// Blocklist tab.
	Blocklist          []blocklistView
	Total              int
	Page, Pages        int
	PrevPage, NextPage int

	// Manual Import tab.
	ManualImport *manualImportData
}

type blocklistView struct {
	ID                                   int64
	Item, SourceTitle, Quality, Protocol string
	Indexer, Message, When               string
}

const blocklistPageSize = 50

const queueTimeFormat = "2 Jan 2006 15:04"

func (h *handler) Activity(w http.ResponseWriter, r *http.Request) {
	h.renderPage(w, "activity", activityPageData{Active: "activity", PageTitle: "Activity", Tab: "queue", Tabs: activityTabs})
}

// ActivityBlocklist is Activity -> Blocklist: releases that failed and won't
// be grabbed again for the same item.
func (h *handler) ActivityBlocklist(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	entries, total, err := store.ListBlocklist(r.Context(), h.deps.DB, blocklistPageSize, (page-1)*blocklistPageSize)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := activityPageData{Active: "activity", PageTitle: "Blocklist", Tab: "blocklist", Tabs: activityTabs, Total: total, Page: page,
		Pages: max(1, (total+blocklistPageSize-1)/blocklistPageSize), PrevPage: page - 1, NextPage: page + 1}
	for _, b := range entries {
		item := b.ItemTitle
		switch {
		case b.SeriesID.Valid && b.EpisodeNumber.Valid:
			item += fmt.Sprintf(" S%02dE%02d", b.SeasonNumber.Int64, b.EpisodeNumber.Int64)
		case b.SeriesID.Valid && b.SeasonNumber.Valid:
			item += fmt.Sprintf(" Season %d", b.SeasonNumber.Int64)
		}
		data.Blocklist = append(data.Blocklist, blocklistView{
			ID: b.ID, Item: item, SourceTitle: b.SourceTitle, Quality: b.Quality, Protocol: b.Protocol,
			Indexer: b.Indexer, Message: b.Message, When: b.Added.Local().Format("2 Jan 2006 15:04"),
		})
	}
	h.renderPage(w, "activity", data)
}

// DeleteBlocklistEntry removes one entry, so its release can be grabbed again.
func (h *handler) DeleteBlocklistEntry(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid blocklist id", http.StatusBadRequest)
		return
	}
	if err := store.DeleteBlocklist(r.Context(), h.deps.DB, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
}

// ClearBlocklist empties the blocklist.
func (h *handler) ClearBlocklist(w http.ResponseWriter, r *http.Request) {
	if err := store.ClearBlocklist(r.Context(), h.deps.DB); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/activity/blocklist")
	w.WriteHeader(http.StatusOK)
}

type queueGrabView struct {
	ID                            int64
	ReleaseTitle, Indexer, Status string
	Message                       string
	SizeHuman                     string
	// Grabbed is when it was sent to the client; Changed when its status
	// last changed, shown once it has moved on from grabbed.
	Grabbed, Changed string
}

type queueResultsData struct {
	Grabs []queueGrabView
}

// ActivityQueue renders the current grab list straight from the DB -
// htmx-polled every few seconds from the activity page. Deliberately does
// NOT call Deluge (that's DownloadService.RefreshQueue's job now, run on
// a slow background timer in cmd/ummarr's serve command - see
// internal/sync/download.go) so this stays a cheap local read regardless
// of the page's own refresh rate.
func (h *handler) ActivityQueue(w http.ResponseWriter, r *http.Request) {
	grabs, err := store.ListGrabs(r.Context(), h.deps.DB)
	if err != nil {
		grabs = nil
	}

	views := make([]queueGrabView, 0, len(grabs))
	for _, g := range grabs {
		v := queueGrabView{
			ID: g.ID, ReleaseTitle: g.ReleaseTitle, Indexer: g.Indexer, Status: g.Status, Message: g.StatusMessage.String,
			SizeHuman: humanizeBytes(g.Size.Int64), Grabbed: g.Added.Local().Format(queueTimeFormat),
		}
		if g.Status != "grabbed" && g.Updated.After(g.Added) {
			v.Changed = g.Updated.Local().Format(queueTimeFormat)
		}
		views = append(views, v)
	}
	h.renderPartial(w, "queue_results", queueResultsData{Grabs: views})
}

// RemoveGrab is the queue's Remove dialog: remove from the download client,
// and blocklist the release with or without searching for another.
func (h *handler) RemoveGrab(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid grab id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	grab, found, err := store.GetGrab(r.Context(), h.deps.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "grab not found", http.StatusNotFound)
		return
	}
	blocklist := r.FormValue("blocklist")
	switch blocklist {
	case sync.BlocklistNone, sync.BlocklistAndSearch, sync.BlocklistOnly:
	default:
		blocklist = sync.BlocklistNone
	}
	if _, err := h.deps.Download.RemoveGrab(r.Context(), grab, r.FormValue("remove_from_client") == "on", blocklist); err != nil {
		renderInlineError(w, "Couldn't remove "+grab.ReleaseTitle+": "+err.Error())
		return
	}
	w.Header().Set("HX-Trigger", "grab-removed")
	w.WriteHeader(http.StatusOK)
}
