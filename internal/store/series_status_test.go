package store

import "testing"

func TestSeriesStatusKey(t *testing.T) {
	for raw, want := range map[string]string{
		"Returning Series": SeriesContinuing, "In Production": SeriesContinuing, "Running": SeriesContinuing, "To Be Determined": SeriesContinuing,
		"Ended": SeriesEnded, "Canceled": SeriesEnded, "cancelled": SeriesEnded,
		"Planned": SeriesUpcoming, "Pilot": SeriesUpcoming, "In Development": SeriesUpcoming,
		"": "",
	} {
		if got := SeriesStatusKey(raw); got != want {
			t.Errorf("SeriesStatusKey(%q) = %q, want %q", raw, got, want)
		}
	}
}
