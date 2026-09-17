package sync

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Listener is told about every recorded event - the notifier, for one.
type Listener interface {
	OnEvent(ctx context.Context, e store.Event)
}

// Events records what happens to the library and passes it on. A nil
// *Events is safe to call: nothing is recorded.
type Events struct {
	DB        *sql.DB
	Listeners []Listener
}

// Record writes e to history and tells every listener.
func (s *Events) Record(ctx context.Context, e store.Event) {
	if s == nil || s.DB == nil {
		return
	}
	id, err := store.RecordEvent(ctx, s.DB, e)
	if err != nil {
		log.Printf("history: %v", err)
		return
	}
	e.ID = id
	for _, l := range s.Listeners {
		l.OnEvent(ctx, e)
	}
}

// Helpers that name an item the way the History page shows it.

func movieEvent(ctx context.Context, q store.Queryer, movieID int64, event string) store.Event {
	e := store.Event{Event: event, MediaType: "movie", MovieID: sql.NullInt64{Int64: movieID, Valid: true}}
	if d, found, err := store.GetMovieDetail(ctx, q, movieID); err == nil && found {
		e.Title = d.Title
		if d.Year.Valid {
			e.Title = fmt.Sprintf("%s (%d)", d.Title, d.Year.Int64)
		}
	}
	return e
}

func seriesEvent(ctx context.Context, q store.Queryer, seriesID int64, event string) store.Event {
	e := store.Event{Event: event, MediaType: "series", SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}}
	if d, found, err := store.GetSeriesDetail(ctx, q, seriesID); err == nil && found {
		e.Title = d.Title
	}
	return e
}

func albumEvent(ctx context.Context, q store.Queryer, albumID int64, event string) store.Event {
	e := store.Event{Event: event, MediaType: "music", AlbumID: sql.NullInt64{Int64: albumID, Valid: true}}
	var artist, title string
	if err := q.QueryRowContext(ctx, `SELECT am.name, al.title FROM albums al JOIN artist_metadata am ON am.id = al.artist_metadata_id WHERE al.id = ?`, albumID).Scan(&artist, &title); err == nil {
		e.Title = artist + " - " + title
	}
	return e
}

// grabEvent names whatever a grab is for.
func grabEvent(ctx context.Context, q store.Queryer, g store.Grab, event string) store.Event {
	var e store.Event
	switch {
	case g.MovieID.Valid:
		e = movieEvent(ctx, q, g.MovieID.Int64, event)
	case g.SeriesID.Valid:
		e = seriesEvent(ctx, q, g.SeriesID.Int64, event)
		switch {
		case g.EpisodeNumber.Valid:
			e.Title += fmt.Sprintf(" S%02dE%02d", g.SeasonNumber.Int64, g.EpisodeNumber.Int64)
		case g.SeasonNumber.Valid:
			e.Title += fmt.Sprintf(" Season %d", g.SeasonNumber.Int64)
		}
	case g.AlbumID.Valid:
		e = albumEvent(ctx, q, g.AlbumID.Int64, event)
	default:
		e = store.Event{Event: event}
	}
	e.Detail, e.Source = g.ReleaseTitle, g.GrabbedBy
	if g.StatusMessage.Valid && g.StatusMessage.String != "" && event != store.EventGrabbed {
		e.Detail = g.ReleaseTitle + " - " + g.StatusMessage.String
	}
	return e
}
