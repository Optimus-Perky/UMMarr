package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// MovieFileDate returns the date a movie's file should carry under setting
// (one of the MovieFileDate* constants), and whether the movie has that date.
// FileDateNone, or any unknown setting, reports no date.
func MovieFileDate(ctx context.Context, q Queryer, movieID int64, setting string) (time.Time, bool, error) {
	column := map[string]string{
		MovieFileDateInCinemas: "mm.in_cinemas",
		MovieFileDatePhysical:  "mm.physical_release",
		MovieFileDateDigital:   "mm.digital_release",
	}[setting]
	if column == "" {
		return time.Time{}, false, nil
	}
	var d sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT `+column+` FROM movies m JOIN movie_metadata mm ON mm.id = m.movie_metadata_id WHERE m.id = ?
	`, movieID).Scan(&d)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("get file date for movie %d: %w", movieID, err)
	}
	return d.Time, d.Valid, nil
}

// EpisodeFileDate is MovieFileDate for an episode (EpisodeFileDateAirDate).
func EpisodeFileDate(ctx context.Context, q Queryer, episodeID int64, setting string) (time.Time, bool, error) {
	if setting != EpisodeFileDateAirDate {
		return time.Time{}, false, nil
	}
	var d sql.NullTime
	if err := q.QueryRowContext(ctx, `SELECT air_date FROM episodes WHERE id = ?`, episodeID).Scan(&d); err != nil {
		return time.Time{}, false, fmt.Errorf("get file date for episode %d: %w", episodeID, err)
	}
	return d.Time, d.Valid, nil
}

// TrackFileDate is MovieFileDate for a track, using its album's release date
// (TrackFileDateReleaseDate).
func TrackFileDate(ctx context.Context, q Queryer, trackID int64, setting string) (time.Time, bool, error) {
	if setting != TrackFileDateReleaseDate {
		return time.Time{}, false, nil
	}
	var d sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT al.release_date
		FROM tracks t
		JOIN album_releases ar ON ar.id = t.album_release_id
		JOIN albums al ON al.id = ar.album_id
		WHERE t.id = ?
	`, trackID).Scan(&d)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("get file date for track %d: %w", trackID, err)
	}
	return d.Time, d.Valid, nil
}
