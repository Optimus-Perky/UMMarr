package store

import "testing"

// The album page reads these straight out, so what the column happens to
// hold is what the page shows: a DATE column written from MusicBrainz
// carries a full timestamp, and MusicBrainz's country list has entries
// that are not countries.
func TestCurrentReleaseSummary(t *testing.T) {
	r := CurrentRelease{TrackCount: 12, Country: countryName("XW"), Date: shortDate("2023-07-21T00:00:00Z")}
	if got := r.Summary(); got != "12 tracks · Worldwide · 2023-07-21" {
		t.Errorf("summary = %q", got)
	}
	// Disambiguation is what tells two otherwise identical rows apart.
	r.Disambiguation = "deluxe edition"
	if got := r.Summary(); got != "12 tracks · Worldwide · 2023-07-21 · deluxe edition" {
		t.Errorf("summary = %q", got)
	}
	// Nothing known is nothing shown, rather than a line of separators.
	if got := (CurrentRelease{Title: "Homework"}).Summary(); got != "" {
		t.Errorf("empty release summarised as %q", got)
	}
	// A real country code is left alone; it needs no translating.
	if countryName("GB") != "GB" {
		t.Error("GB should be left as it is")
	}
	// A plain date is already short.
	if shortDate("2023-07-21") != "2023-07-21" || shortDate("2023") != "2023" {
		t.Error("shortDate mangled a date that was already short")
	}
}
