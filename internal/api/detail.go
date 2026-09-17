package api

import (
	"fmt"
	"net/http"
)

// monitoredChipView is the shared data shape for partials/monitored_chip.html
// - the movie/series/season/episode/album detail pages' clickable
// Monitored/Unmonitored (or "Yes"/"No", for the episode table's tighter
// column) toggle. ToggleURL is whichever POST endpoint flips that one
// item's monitored flag - the handler re-renders this same partial with
// the new state, targeted at itself (hx-target="this"), so toggling
// never needs a full page reload.
type monitoredChipView struct {
	Monitored bool
	ToggleURL string
	LabelOn   string
	LabelOff  string
	// Icon renders Sonarr's filled/outlined bookmark instead of a text
	// chip, for the dense per-episode and per-season rows where a
	// "Monitored"/"Unmonitored" word would dominate the row.
	Icon bool
}

// renderMonitoredChip writes just the monitored_chip partial - the
// response body for every *Monitored toggle handler.
func (h *handler) renderMonitoredChip(w http.ResponseWriter, view monitoredChipView) {
	h.renderPartial(w, "monitored_chip", view)
}

// ratingView is a template-friendly {label, value} pair projected from a
// ratings map[string]float64 - html/template can range over a map, but
// only in undefined key order, so every detail-page handler converts to a
// slice instead. Shared by movie/series/album detail pages.
type ratingView struct {
	Provider string
	Value    float64
}

func toRatingViews(ratings map[string]float64) []ratingView {
	views := make([]ratingView, 0, len(ratings))
	for provider, value := range ratings {
		views = append(views, ratingView{Provider: provider, Value: value})
	}
	return views
}

// fileStatusChip renders a small "Downloaded"/"Missing" label for a
// per-episode/per-track file-status chip - shared by the series and album
// detail page templates via this one Go-side helper rather than repeating
// the same {{if}} in three places.
func fileStatusLabel(hasFile bool) string {
	if hasFile {
		return "Downloaded"
	}
	return "Missing"
}

// humanizeDuration formats a track's duration_ms as "3:45" - the same
// mm:ss style Radarr/Sonarr/Lidarr all use for runtime-ish fields.
func humanizeDuration(ms int64) string {
	if ms <= 0 {
		return ""
	}
	totalSeconds := ms / 1000
	minutes := totalSeconds / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}
