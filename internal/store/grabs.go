package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Grab is one release sent to a download client - see migration
// 00011_grabs.sql for the exactly-one-of-movie/series/album constraint.
// Status: grabbed|downloading|imported|import_failed|needs_extraction|failed|removed.
type Grab struct {
	ID           int64
	MovieID      sql.NullInt64
	SeriesID     sql.NullInt64
	SeasonNumber sql.NullInt64
	// EpisodeNumber refines SeasonNumber down to a single episode - set
	// only for a per-episode grab (see DownloadService.GrabEpisode); a
	// season-pack or whole-series grab leaves it NULL. No FK - purely a
	// display/bookkeeping refinement, same reasoning as SeasonNumber.
	EpisodeNumber sql.NullInt64
	AlbumID       sql.NullInt64
	// TrackID refines AlbumID down to a single track - set only for a
	// per-track grab (see DownloadService.GrabTrack), which importTrack
	// uses instead of importAlbum's positional whole-release match.
	TrackID          sql.NullInt64
	IndexerID        sql.NullInt64
	GrabbedBy        string // interactive, automatic or rss
	ReleaseTitle     string
	Indexer          string
	Protocol         string
	Size             sql.NullInt64
	DownloadClient   string
	DownloadClientID sql.NullString
	// DownloadClientRef is the download_clients row the grab went to; NULL for
	// grabs from before there were rows (they belong to the client program named
	// by DownloadClient).
	DownloadClientRef sql.NullInt64
	// Published is the indexer's publish date and InfoHash the torrent's
	// infohash, kept so a blocklist entry made later can match the release.
	Published     sql.NullTime
	InfoHash      sql.NullString
	Status        string
	StatusMessage sql.NullString
	LastChecked   sql.NullTime
	Added         time.Time
	Updated       time.Time
}

// InsertGrab records a new grab. Called only after the download client has
// actually accepted the release - see internal/sync/download.go - so a
// failed grab attempt never leaves an orphaned row with no
// download_client_id.
func InsertGrab(ctx context.Context, q Queryer, g Grab) (int64, error) {
	res, err := q.ExecContext(ctx, `
		INSERT INTO grabs (
			movie_id, series_id, season_number, episode_number, album_id, track_id,
			release_title, indexer, protocol, size,
			download_client, download_client_id, status, indexer_id, grabbed_by, download_client_ref, published, info_hash
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, g.MovieID, g.SeriesID, g.SeasonNumber, g.EpisodeNumber, g.AlbumID, g.TrackID,
		g.ReleaseTitle, g.Indexer, g.Protocol, g.Size,
		g.DownloadClient, g.DownloadClientID, g.Status, g.IndexerID, grabbedBy(g.GrabbedBy), g.DownloadClientRef, g.Published, g.InfoHash)
	if err != nil {
		return 0, fmt.Errorf("insert grab: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted grab id: %w", err)
	}
	return id, nil
}

// UpdateGrabStatus updates a grab's status (and optional status message),
// called whenever a grab has just been checked against the download
// client AND its status has changed - also bumps last_checked, since a
// status change is itself a "just checked" event. See TouchGrabChecked
// for the no-change case.
func UpdateGrabStatus(ctx context.Context, q Queryer, grabID int64, status string, message sql.NullString) error {
	_, err := q.ExecContext(ctx, `
		UPDATE grabs SET status = ?, status_message = ?, updated = CURRENT_TIMESTAMP, last_checked = CURRENT_TIMESTAMP
		WHERE id = ?
	`, status, message, grabID)
	if err != nil {
		return fmt.Errorf("update grab %d status: %w", grabID, err)
	}
	return nil
}

// TouchGrabChecked records that grabID was just checked against the
// download client with no status change to report - keeps last_checked
// accurate (so DownloadService.RefreshQueue's grace-period math is
// correct) without touching status/status_message/updated.
func TouchGrabChecked(ctx context.Context, q Queryer, grabID int64) error {
	_, err := q.ExecContext(ctx, `UPDATE grabs SET last_checked = CURRENT_TIMESTAMP WHERE id = ?`, grabID)
	if err != nil {
		return fmt.Errorf("touch grab %d checked: %w", grabID, err)
	}
	return nil
}

const grabColumns = `
	id, movie_id, series_id, season_number, episode_number, album_id, track_id,
	release_title, indexer, protocol, size,
	download_client, download_client_id, status, status_message, last_checked, added, updated, indexer_id, grabbed_by, download_client_ref, published, info_hash
`

func scanGrab(row interface{ Scan(...any) error }) (Grab, error) {
	var g Grab
	err := row.Scan(
		&g.ID, &g.MovieID, &g.SeriesID, &g.SeasonNumber, &g.EpisodeNumber, &g.AlbumID, &g.TrackID,
		&g.ReleaseTitle, &g.Indexer, &g.Protocol, &g.Size,
		&g.DownloadClient, &g.DownloadClientID, &g.Status, &g.StatusMessage, &g.LastChecked, &g.Added, &g.Updated, &g.IndexerID, &g.GrabbedBy, &g.DownloadClientRef, &g.Published, &g.InfoHash,
	)
	return g, err
}

// ListGrabs lists every grab, newest first - the activity/queue page's data
// source.
func ListGrabs(ctx context.Context, q Queryer) ([]Grab, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+grabColumns+` FROM grabs ORDER BY added DESC`)
	if err != nil {
		return nil, fmt.Errorf("list grabs: %w", err)
	}
	defer rows.Close()

	var grabs []Grab
	for rows.Next() {
		g, err := scanGrab(rows)
		if err != nil {
			return nil, fmt.Errorf("scan grab: %w", err)
		}
		grabs = append(grabs, g)
	}
	return grabs, rows.Err()
}

// GetGrab looks up a single grab by id - used by the "Check again" retry
// route, which needs the full row (not just its id) to call
// DownloadService.RetryImport.
func GetGrab(ctx context.Context, q Queryer, id int64) (Grab, bool, error) {
	row := q.QueryRowContext(ctx, `SELECT `+grabColumns+` FROM grabs WHERE id = ?`, id)
	g, err := scanGrab(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Grab{}, false, nil
	}
	if err != nil {
		return Grab{}, false, fmt.Errorf("get grab %d: %w", id, err)
	}
	return g, true, nil
}

// FindGrabByDownloadClientID looks up a grab by its download client's own
// torrent id (e.g. Deluge's infohash) - used by the
// /downloads/{hash}/completed webhook, which only ever receives that hash
// from the download client's own "on complete" script, not an internal
// grab id.
func FindGrabByDownloadClientID(ctx context.Context, q Queryer, downloadClientID string) (Grab, bool, error) {
	row := q.QueryRowContext(ctx, `SELECT `+grabColumns+` FROM grabs WHERE download_client_id = ?`, downloadClientID)
	g, err := scanGrab(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Grab{}, false, nil
	}
	if err != nil {
		return Grab{}, false, fmt.Errorf("find grab by download client id %q: %w", downloadClientID, err)
	}
	return g, true, nil
}

// grabbedBy defaults a grab's source to a manual (interactive) grab.
func grabbedBy(by string) string {
	if by == "" {
		return "interactive"
	}
	return by
}

func importedReleaseTitle(ctx context.Context, q Queryer, query string, args ...any) (string, bool, error) {
	var title string
	err := q.QueryRowContext(ctx, query, args...).Scan(&title)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("find imported release: %w", err)
	}
	return title, true, nil
}

// ImportedReleaseTitleForMovie is the release name of the latest grab
// imported for the movie - what its file was called before renaming.
func ImportedReleaseTitleForMovie(ctx context.Context, q Queryer, movieID int64) (string, bool, error) {
	return importedReleaseTitle(ctx, q, `SELECT release_title FROM grabs WHERE movie_id = ? AND status = 'imported' ORDER BY added DESC LIMIT 1`, movieID)
}

// ImportedReleaseTitleForEpisode is ImportedReleaseTitleForMovie for an
// episode: a grab for it, its season pack or the whole series.
func ImportedReleaseTitleForEpisode(ctx context.Context, q Queryer, episodeID int64) (string, bool, error) {
	return importedReleaseTitle(ctx, q, `
		SELECT g.release_title FROM grabs g JOIN episodes e ON e.id = ?
		WHERE g.series_id = e.series_id AND g.status = 'imported'
		  AND `+grabCoversEpisode+`
		ORDER BY g.added DESC LIMIT 1`, episodeID)
}

// ImportedReleaseTitleForTrack is ImportedReleaseTitleForMovie for a track:
// a grab for it or its album.
func ImportedReleaseTitleForTrack(ctx context.Context, q Queryer, trackID int64) (string, bool, error) {
	return importedReleaseTitle(ctx, q, `
		SELECT g.release_title FROM grabs g
		JOIN tracks t ON t.id = ?
		JOIN album_releases ar ON ar.id = t.album_release_id
		WHERE g.album_id = ar.album_id AND g.status = 'imported' AND (g.track_id IS NULL OR g.track_id = t.id)
		ORDER BY g.added DESC LIMIT 1`, trackID)
}

// EpisodeGrabStatuses is the status of the newest unfinished grab covering
// each episode of a series (a grab for it, its season pack or the whole
// series), keyed by episode id. Episodes with no grab in progress are absent.
func EpisodeGrabStatuses(ctx context.Context, q Queryer, seriesID int64) (map[int64]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT e.id, g.status
		FROM episodes e
		JOIN grabs g ON g.series_id = e.series_id AND `+grabCoversEpisode+`
		WHERE e.series_id = ? AND g.status IN (`+queuedGrabStatuses+`)
		ORDER BY g.added`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("episode grab statuses for series %d: %w", seriesID, err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, fmt.Errorf("scan episode grab status: %w", err)
		}
		out[id] = status // newest wins
	}
	return out, rows.Err()
}

func listGrabs(ctx context.Context, q Queryer, where string, args ...any) ([]Grab, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+grabColumns+` FROM grabs WHERE `+where+` ORDER BY added DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("list grabs: %w", err)
	}
	defer rows.Close()
	var grabs []Grab
	for rows.Next() {
		g, err := scanGrab(rows)
		if err != nil {
			return nil, fmt.Errorf("scan grab: %w", err)
		}
		grabs = append(grabs, g)
	}
	return grabs, rows.Err()
}

// ListGrabsForMovie is a movie's grab history, newest first.
func ListGrabsForMovie(ctx context.Context, q Queryer, movieID int64) ([]Grab, error) {
	return listGrabs(ctx, q, `movie_id = ?`, movieID)
}

// ListGrabsForSeries is a series' grab history narrowed to a season or an
// episode when given: a grab counts when its scope covers what's asked
// (a whole-series grab covers every episode, a season pack its season).
func ListGrabsForSeries(ctx context.Context, q Queryer, seriesID int64, season, episode *int) ([]Grab, error) {
	where, args := `series_id = ?`, []any{seriesID}
	if season != nil {
		where += ` AND (season_number IS NULL OR season_number = ?)`
		args = append(args, *season)
		if episode != nil {
			where += ` AND (episode_number IS NULL OR episode_number = ?)`
			args = append(args, *episode)
		}
	}
	return listGrabs(ctx, q, where, args...)
}

// ListGrabsForAlbum is an album's grab history, narrowed to a track when given.
func ListGrabsForAlbum(ctx context.Context, q Queryer, albumID int64, trackID *int64) ([]Grab, error) {
	if trackID != nil {
		return listGrabs(ctx, q, `album_id = ? AND (track_id IS NULL OR track_id = ?)`, albumID, *trackID)
	}
	return listGrabs(ctx, q, `album_id = ?`, albumID)
}

// EpisodeAiredOn finds the episode of a series that aired on a given day,
// for matching a daily release like "Show 2024.05.01" - those name a date
// instead of a season and episode.
func EpisodeAiredOn(ctx context.Context, q Queryer, seriesID int64, day time.Time) (season, episode int, ok bool) {
	row := q.QueryRowContext(ctx, `
		SELECT season_number, episode_number FROM episodes
		WHERE series_id = ? AND date(air_date) = date(?)
		ORDER BY season_number, episode_number LIMIT 1`, seriesID, day.Format("2006-01-02"))
	switch err := row.Scan(&season, &episode); {
	case errors.Is(err, sql.ErrNoRows):
		return 0, 0, false
	case err != nil:
		return 0, 0, false
	}
	return season, episode, true
}

// QueuedSeriesGrabsWithoutSeason are in-flight series grabs that recorded no
// season, i.e. that claim every episode of their series. Genuine
// whole-series packs look the same, so callers re-read the release title
// before changing anything.
func QueuedSeriesGrabsWithoutSeason(ctx context.Context, q Queryer) ([]Grab, error) {
	return listGrabs(ctx, q, `series_id IS NOT NULL AND season_number IS NULL AND status IN (`+queuedGrabStatuses+`)`)
}

// SetGrabCoverage corrects which episodes a grab covers.
func SetGrabCoverage(ctx context.Context, q Queryer, grabID int64, season, episode sql.NullInt64) error {
	_, err := q.ExecContext(ctx, `
		UPDATE grabs SET season_number = ?, episode_number = ?, updated = CURRENT_TIMESTAMP WHERE id = ?`,
		season, episode, grabID)
	if err != nil {
		return fmt.Errorf("set grab %d coverage: %w", grabID, err)
	}
	return nil
}
