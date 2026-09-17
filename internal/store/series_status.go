package store

import "strings"

// Sonarr's series statuses. TMDB and TVmaze each have their own words
// ("Returning Series", "Canceled", "Running", "To Be Determined"...); the
// UI, filters and API speak Sonarr's.
const (
	SeriesContinuing = "continuing"
	SeriesEnded      = "ended"
	SeriesUpcoming   = "upcoming"
)

// SeriesStatusKey maps a provider's status onto Sonarr's, or "" when there
// isn't one.
func SeriesStatusKey(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return ""
	case "ended", "canceled", "cancelled":
		return SeriesEnded
	case "planned", "pilot", "in development":
		return SeriesUpcoming
	default: // returning series, in production, running, to be determined, continuing
		return SeriesContinuing
	}
}

// SeriesStatusLabel is SeriesStatusKey as the UI shows it.
func SeriesStatusLabel(raw string) string {
	switch SeriesStatusKey(raw) {
	case SeriesEnded:
		return "Ended"
	case SeriesUpcoming:
		return "Upcoming"
	case SeriesContinuing:
		return "Continuing"
	}
	return ""
}

// StatusKey is the series' status in Sonarr's terms.
func (s SeriesSummary) StatusKey() string { return SeriesStatusKey(s.Status.String) }

// StatusLabel is the series' status as the UI shows it.
func (s SeriesSummary) StatusLabel() string { return SeriesStatusLabel(s.Status.String) }
