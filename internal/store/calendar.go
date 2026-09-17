package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/titleutil"
)

// CalendarEntry is one thing coming out on a day: an episode airing, a
// movie's cinema, digital or physical release, or an album's release.
type CalendarEntry struct {
	Kind      string // movie, series or music
	Date      time.Time
	Title     string // series, movie or artist
	Subtitle  string // "1x05 - Pilot", the album title, or blank
	Label     string // In Cinemas, Digital, Physical, Album
	URL       string
	HasFile   bool
	Monitored bool
}

const calendarDate = "2006-01-02"

// ListCalendar lists everything due between from and to (inclusive), in date
// order.
func ListCalendar(ctx context.Context, q Queryer, from, to time.Time) ([]CalendarEntry, error) {
	start, end := from.Format(calendarDate), to.Format(calendarDate)
	var entries []CalendarEntry

	rows, err := q.QueryContext(ctx, `
		SELECT e.air_date, sm.title, e.season_number, e.episode_number, COALESCE(e.title, ''),
		       e.episode_file_id IS NOT NULL, e.monitored AND s.monitored
		FROM episodes e JOIN series s ON s.id = e.series_id JOIN series_metadata sm ON sm.id = s.series_metadata_id
		WHERE e.air_date IS NOT NULL AND date(e.air_date) BETWEEN ? AND ?`, start, end)
	if err != nil {
		return nil, fmt.Errorf("calendar episodes: %w", err)
	}
	for rows.Next() {
		var e CalendarEntry
		var season, episode int
		var episodeTitle string
		if err := rows.Scan(&e.Date, &e.Title, &season, &episode, &episodeTitle, &e.HasFile, &e.Monitored); err != nil {
			rows.Close()
			return nil, err
		}
		e.Kind, e.Label = "series", fmt.Sprintf("%dx%02d", season, episode)
		e.Subtitle = episodeTitle
		e.URL = "/tv/" + titleutil.Slug(e.Title)
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = q.QueryContext(ctx, `
		SELECT mm.title, mm.year, m.monitored, EXISTS (SELECT 1 FROM movie_files f WHERE f.movie_id = m.id),
		       mm.in_cinemas, mm.digital_release, mm.physical_release
		FROM movies m JOIN movie_metadata mm ON mm.id = m.movie_metadata_id
		WHERE date(mm.in_cinemas) BETWEEN ? AND ? OR date(mm.digital_release) BETWEEN ? AND ? OR date(mm.physical_release) BETWEEN ? AND ?`,
		start, end, start, end, start, end)
	if err != nil {
		return nil, fmt.Errorf("calendar movies: %w", err)
	}
	for rows.Next() {
		var title string
		var year sql.NullInt64
		var monitored, hasFile bool
		var cinemas, digital, physical sql.NullTime
		if err := rows.Scan(&title, &year, &monitored, &hasFile, &cinemas, &digital, &physical); err != nil {
			rows.Close()
			return nil, err
		}
		url := "/movies/" + titleutil.SlugWithYear(title, year.Int64)
		for _, event := range []struct {
			date  sql.NullTime
			label string
		}{{cinemas, "In Cinemas"}, {digital, "Digital"}, {physical, "Physical"}} {
			if !event.date.Valid || event.date.Time.Format(calendarDate) < start || event.date.Time.Format(calendarDate) > end {
				continue
			}
			entries = append(entries, CalendarEntry{
				Kind: "movie", Date: event.date.Time, Title: title, Label: event.label,
				URL: url, HasFile: hasFile, Monitored: monitored,
			})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = q.QueryContext(ctx, `
		SELECT al.release_date, am.name, al.title, al.monitored,
		       EXISTS (SELECT 1 FROM tracks t JOIN album_releases ar ON ar.id = t.album_release_id
		               WHERE ar.album_id = al.id AND t.track_file_id IS NOT NULL)
		FROM albums al JOIN artist_metadata am ON am.id = al.artist_metadata_id
		WHERE al.release_date IS NOT NULL AND date(al.release_date) BETWEEN ? AND ?`, start, end)
	if err != nil {
		return nil, fmt.Errorf("calendar albums: %w", err)
	}
	for rows.Next() {
		var e CalendarEntry
		var album string
		if err := rows.Scan(&e.Date, &e.Title, &album, &e.Monitored, &e.HasFile); err != nil {
			rows.Close()
			return nil, err
		}
		e.Kind, e.Label, e.Subtitle = "music", "Album", album
		e.URL = "/music/albums/" + titleutil.Slug(e.Title) + "/" + titleutil.Slug(album)
		entries = append(entries, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].Date.Equal(entries[j].Date) {
			return entries[i].Date.Before(entries[j].Date)
		}
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		return entries[i].Title < entries[j].Title
	})
	return entries, nil
}
