package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Scheduled metadata refresh cadence, after Sonarr's and Radarr's own
// rules: anything still changing - a series still airing or not yet
// started, a movie not yet released or released this year or last - is
// refreshed every RefreshActiveAfter, so a newly announced episode or a
// digital release date shows up within the day. Everything else changes
// rarely and is refreshed every RefreshSettledAfter. Something never
// refreshed is always due.
const (
	RefreshActiveAfter  = 12 * time.Hour
	RefreshSettledAfter = 30 * 24 * time.Hour
)

// refreshCandidate is one movie or series and how long since its refresh.
type refreshCandidate struct {
	id         int64
	status     string
	year       sql.NullInt64
	hoursSince sql.NullFloat64 // since last_info_sync; NULL when never synced
}

func (c refreshCandidate) due(active bool) bool {
	if !c.hoursSince.Valid {
		return true
	}
	since := time.Duration(c.hoursSince.Float64 * float64(time.Hour))
	if active {
		return since >= RefreshActiveAfter
	}
	return since >= RefreshSettledAfter
}

func queryRefreshCandidates(ctx context.Context, q Queryer, query string) ([]refreshCandidate, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []refreshCandidate
	for rows.Next() {
		var c refreshCandidate
		var status sql.NullString
		if err := rows.Scan(&c.id, &status, &c.year, &c.hoursSince); err != nil {
			return nil, err
		}
		c.status = status.String
		out = append(out, c)
	}
	return out, rows.Err()
}

// SeriesDueForRefresh lists the series whose metadata is due a scheduled
// refresh (see RefreshActiveAfter), least recently refreshed first.
func SeriesDueForRefresh(ctx context.Context, q Queryer) ([]int64, error) {
	candidates, err := queryRefreshCandidates(ctx, q, `
		SELECT s.id, sm.status, sm.year,
		       (julianday('now') - julianday(sm.last_info_sync)) * 24
		FROM series s JOIN series_metadata sm ON sm.id = s.series_metadata_id
		ORDER BY sm.last_info_sync IS NOT NULL, sm.last_info_sync`)
	if err != nil {
		return nil, err
	}
	var due []int64
	for _, c := range candidates {
		if c.due(SeriesStatusKey(c.status) != SeriesEnded) {
			due = append(due, c.id)
		}
	}
	return due, nil
}

// MoviesDueForRefresh is SeriesDueForRefresh for movies. A movie counts as
// still changing until TMDB calls it released, and for the year after its
// own (release dates, ratings and posters settle over that time).
func MoviesDueForRefresh(ctx context.Context, q Queryer, now time.Time) ([]int64, error) {
	candidates, err := queryRefreshCandidates(ctx, q, `
		SELECT m.id, mm.status, mm.year,
		       (julianday('now') - julianday(mm.last_info_sync)) * 24
		FROM movies m JOIN movie_metadata mm ON mm.id = m.movie_metadata_id
		ORDER BY mm.last_info_sync IS NOT NULL, mm.last_info_sync`)
	if err != nil {
		return nil, err
	}
	var due []int64
	for _, c := range candidates {
		released := strings.EqualFold(strings.TrimSpace(c.status), "released")
		recent := !c.year.Valid || c.year.Int64 >= int64(now.Year()-1)
		if c.due(!released || recent) {
			due = append(due, c.id)
		}
	}
	return due, nil
}
