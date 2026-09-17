package api

import (
	"net/http"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// The release calendar, as Radarr, Sonarr and Lidarr have one: a month at a
// time, with movies, TV and music each toggleable.

type calendarEntryView struct {
	store.CalendarEntry
	StatusClass string // done, missing or unreleased
	Event       string // cinema, digital or physical, for the movie toggles
}

// movieEvents maps a movie entry's label to its toggle.
var movieEvents = map[string]string{"In Cinemas": "cinema", "Digital": "digital", "Physical": "physical"}

type calendarDay struct {
	Date    time.Time
	Day     int
	InMonth bool
	Today   bool
	Entries []calendarEntryView // the first few
	More    []calendarEntryView // the rest, behind "+N more"
}

// calendarDayLimit is how many entries a day shows before the rest go
// behind a "+N more" - ten episodes of one series on one day is common.
const calendarDayLimit = 5

type calendarPageData struct {
	Active, PageTitle string
	MonthLabel        string
	PrevMonth         string // YYYY-MM for the links
	NextMonth         string
	ThisMonth         string
	Weekdays          []string
	Weeks             [][]calendarDay
	Counts            map[string]int
	Total             int
}

// Calendar shows a month of releases: episodes by air date, movies by their
// cinema, digital and physical dates, and albums by release date.
func (h *handler) Calendar(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	month := monthStart(now)
	if v := r.URL.Query().Get("month"); v != "" {
		parsed, err := time.Parse("2006-01", v)
		if err != nil {
			http.Error(w, "month must look like 2026-09", http.StatusBadRequest)
			return
		}
		month = parsed
	}
	// The grid runs whole weeks, Monday to Sunday, so it spills either side
	// of the month.
	gridStart := month.AddDate(0, 0, -weekdayOffset(month))
	gridEnd := gridStart.AddDate(0, 0, 41)
	entries, err := store.ListCalendar(r.Context(), h.deps.DB, gridStart, gridEnd)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	byDay := map[string][]calendarEntryView{}
	counts := map[string]int{}
	for _, e := range entries {
		view := calendarEntryView{CalendarEntry: e, StatusClass: calendarStatus(e, now), Event: movieEvents[e.Label]}
		key := e.Date.Format("2006-01-02")
		byDay[key] = append(byDay[key], view)
		counts[e.Kind]++
		if view.Event != "" {
			counts[view.Event]++
		}
	}
	today := now.Format("2006-01-02")
	var weeks [][]calendarDay
	for week := 0; week < 6; week++ {
		days := make([]calendarDay, 0, 7)
		for i := 0; i < 7; i++ {
			date := gridStart.AddDate(0, 0, week*7+i)
			key := date.Format("2006-01-02")
			day := calendarDay{Date: date, Day: date.Day(), InMonth: date.Month() == month.Month(), Today: key == today, Entries: byDay[key]}
			if len(day.Entries) > calendarDayLimit {
				day.Entries, day.More = day.Entries[:calendarDayLimit], day.Entries[calendarDayLimit:]
			}
			days = append(days, day)
		}
		weeks = append(weeks, days)
	}
	h.renderPage(w, "calendar", calendarPageData{
		Active: "calendar", PageTitle: "Calendar", MonthLabel: month.Format("January 2006"),
		PrevMonth: month.AddDate(0, -1, 0).Format("2006-01"), NextMonth: month.AddDate(0, 1, 0).Format("2006-01"),
		ThisMonth: monthStart(now).Format("2006-01"),
		Weekdays:  []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"},
		Weeks:     weeks, Counts: counts, Total: len(entries),
	})
}

// calendarStatus colours an entry: already in the library, still to come, or
// due and missing.
func calendarStatus(e store.CalendarEntry, now time.Time) string {
	switch {
	case e.HasFile:
		return "done"
	case e.Date.After(now):
		return "unreleased"
	case !e.Monitored:
		return "unmonitored"
	}
	return "missing"
}

func monthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// weekdayOffset is how many days back Monday is.
func weekdayOffset(t time.Time) int {
	return (int(t.Weekday()) + 6) % 7
}
