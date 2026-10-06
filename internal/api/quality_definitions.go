package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// UpdateQualityDefinitions saves Settings -> Quality: the minimum, preferred
// and maximum size of each video quality, in MB per minute. A blank box is
// 0 - no limit, or no preference.
func (h *handler) UpdateQualityDefinitions(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	current, err := store.ListQualityDefinitions(r.Context(), h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var problems []string
	number := func(name, label string) float64 {
		raw := strings.TrimSpace(r.FormValue(name))
		if raw == "" {
			return 0
		}
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil || n < 0 {
			problems = append(problems, label+": enter a number, 0 or more.")
			return 0
		}
		return n
	}
	defs := make([]store.QualityDefinition, 0, len(current))
	for _, d := range current {
		d.MinSize = number("min_"+d.Quality, d.Quality+" minimum")
		d.PreferredSize = number("preferred_"+d.Quality, d.Quality+" preferred")
		d.MaxSize = number("max_"+d.Quality, d.Quality+" maximum")
		if err := d.Validate(); err != nil {
			problems = append(problems, err.Error()+".")
		}
		defs = append(defs, d)
	}
	if len(problems) > 0 {
		renderInlineError(w, "Nothing was saved. "+strings.Join(problems, " "))
		return
	}
	if err := store.SaveQualityDefinitions(r.Context(), h.deps.DB, defs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/settings/quality")
	w.WriteHeader(http.StatusOK)
}
