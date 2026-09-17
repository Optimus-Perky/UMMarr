package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// UpdateIndexerOptions saves Settings -> Indexers -> Options. Radarr allows
// an RSS Sync Interval of 0 (off) or 10 to 120 minutes.
func (h *handler) UpdateIndexerOptions(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var problems []string
	whole := func(name, label string) int {
		n, err := strconv.Atoi(strings.TrimSpace(r.FormValue(name)))
		if err != nil || n < 0 {
			problems = append(problems, label+": enter a whole number, 0 or more.")
			return 0
		}
		return n
	}
	current, err := store.GetIndexerSettings(r.Context(), h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s := current
	s.MinimumAge = whole("minimum_age", "Minimum Age")
	s.Retention = whole("retention", "Retention")
	s.MaximumSize = whole("maximum_size", "Maximum Size")
	// Radarr's delay can be negative: search that many days before the date.
	if n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("availability_delay"))); err != nil {
		problems = append(problems, "Availability Delay: enter a whole number of days.")
	} else {
		s.AvailabilityDelay = n
	}
	s.RSSSyncInterval = whole("rss_sync_interval", "RSS Sync Interval")
	if s.RSSSyncInterval > 0 && (s.RSSSyncInterval < 10 || s.RSSSyncInterval > 120) {
		problems = append(problems, "RSS Sync Interval: use 0 to turn it off, or 10 to 120 minutes.")
	}
	s.PreferIndexerFlags = r.FormValue("prefer_indexer_flags") == "on"
	s.AllowHardcodedSubs = r.FormValue("allow_hardcoded_subs") == "on"
	s.WhitelistedHardcodedSubs = strings.TrimSpace(r.FormValue("whitelisted_hardcoded_subs"))
	if len(problems) > 0 {
		renderInlineError(w, "Nothing was saved. "+strings.Join(problems, " "))
		return
	}
	if err := store.UpdateIndexerOptions(r.Context(), h.deps.DB, s); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/indexers")
	w.WriteHeader(http.StatusOK)
}
