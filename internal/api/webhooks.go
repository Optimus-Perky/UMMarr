// Package api (this file): the one route meant to be called by something
// other than UMMarr's own browser UI - a download client's "on torrent
// complete" execute hook, POSTing here the moment a download finishes
// instead of UMMarr having to wait for its own once-a-minute fallback
// poll (see DownloadService.RefreshQueue) to notice. For Deluge
// specifically, this is its "Execute" plugin's on-complete event, running
// a small script such as:
//
//	curl -s -X POST "http://<ummarr-host>:8080/downloads/$(torrent_id)/completed?token=$UMMARR_WEBHOOK_TOKEN"
//
// Exempt from the session-cookie auth every other route requires (see
// auth.go) - a download client's script can't hold a browser session -
// but gated by its own shared-secret token instead, when configured
// (UMMARR_WEBHOOK_TOKEN), since this is otherwise the one route on the
// whole site reachable with zero credentials at all.
package api

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strconv"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func (h *handler) DownloadCompleted(w http.ResponseWriter, r *http.Request) {
	if h.deps.WebhookToken != "" {
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(h.deps.WebhookToken)) != 1 {
			http.Error(w, "invalid or missing token", http.StatusUnauthorized)
			return
		}
	}

	hash := r.PathValue("hash")
	grab, found, err := store.FindGrabByDownloadClientID(r.Context(), h.deps.DB, hash)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "no grab found for that download id", http.StatusNotFound)
		return
	}
	grab = h.deps.Download.RefreshGrab(r.Context(), grab)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, grab.Status)
}

// RetryGrab is the Activity page's "Check again" button - re-attempts
// import for a grab stuck in needs_extraction or import_failed, scanning
// its download directory live rather than trusting Deluge's own file
// listing (see DownloadService.RetryImport). No response body needed: the
// Activity page's queue partial re-polls the (cheap, DB-only)
// /activity/queue endpoint every few seconds and will pick up the new
// status on its own.
func (h *handler) RetryGrab(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid grab id", http.StatusBadRequest)
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
	h.deps.Download.RetryImport(r.Context(), grab)
	w.WriteHeader(http.StatusOK)
}
