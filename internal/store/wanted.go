package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
)

// This file describes library items the way deciding on a release needs
// them: monitored, missing, already queued, and the dates availability is
// judged by.

// queuedGrabStatuses are grabs whose download is still in the download
// client, waiting to download or to import - a new grab for the same item
// would only duplicate them.
const queuedGrabStatuses = `'grabbed', 'downloading', 'needs_extraction', 'import_failed'`

// grabCoversEpisode matches a grab (g) against an episode (e) by how much
// the grab recorded that it covers: an episode number for one episode, a
// season number alone for a season pack, and neither for a whole-series
// pack.
//
// A whole-series pack does not claim episodes that have not aired yet -
// nothing can be downloading an episode that does not exist - which also
// stops one release with an unreadable title from putting Downloading on a
// twenty-five year run.
const grabCoversEpisode = `(
		   (g.season_number = e.season_number AND (g.episode_number IS NULL OR g.episode_number = e.episode_number))
		OR (g.season_number IS NULL AND (e.air_date IS NULL OR date(e.air_date) <= date('now')))
	)`

// DefaultQualityProfileItems is the weight table a profile of that kind
// starts with: every quality in its catalog allowed, better qualities
// weighted higher.
func DefaultQualityProfileItems(mediaKind string) []releaseparse.QualityProfileItem {
	return defaultQualityProfileItems(mediaKind)
}

// WantedMovie is a movie as release decisions see it.
type WantedMovie struct {
	ID                  int64
	Title               string
	OriginalTitle       string
	Year                int
	Monitored           bool
	HasFile             bool
	Queued              bool
	MinimumAvailability string // tba, announced, inCinemas or released
	InCinemas           sql.NullTime
	PhysicalRelease     sql.NullTime
	DigitalRelease      sql.NullTime
	QualityProfileID    sql.NullInt64
	TMDbID              int
	IMDbID              string
	// FileQuality is the recorded quality of the movie's file, when it has
	// one; FileRelease the release title that file was imported from ("" when
	// unknown). Together they say whether a release would be an upgrade.
	FileQuality releaseparse.FileQuality
	FileRelease string
}

const wantedMovieSelect = `
	SELECT m.id, mm.title, COALESCE(mm.original_title, ''), COALESCE(mm.year, 0), m.monitored,
	       EXISTS (SELECT 1 FROM movie_files f WHERE f.movie_id = m.id),
	       EXISTS (SELECT 1 FROM grabs g WHERE g.movie_id = m.id AND g.status IN (` + queuedGrabStatuses + `)),
	       m.minimum_availability, mm.in_cinemas, mm.physical_release, mm.digital_release, m.quality_profile_id,
	       COALESCE((SELECT e.external_id FROM external_ids e WHERE e.entity_type = 'movie' AND e.entity_id = mm.id AND e.provider = 'tmdb'), ''),
	       COALESCE((SELECT e.external_id FROM external_ids e WHERE e.entity_type = 'movie' AND e.entity_id = mm.id AND e.provider = 'imdb'), ''),
	       COALESCE((SELECT f.quality FROM movie_files f WHERE f.movie_id = m.id ORDER BY f.id DESC LIMIT 1), '{}'),
	       COALESCE((SELECT g.release_title FROM grabs g WHERE g.movie_id = m.id AND g.status = 'imported' ORDER BY g.added DESC LIMIT 1), '')
	FROM movies m JOIN movie_metadata mm ON mm.id = m.movie_metadata_id`

func scanWantedMovie(row interface{ Scan(...any) error }) (WantedMovie, error) {
	var m WantedMovie
	var tmdb, quality string
	err := row.Scan(&m.ID, &m.Title, &m.OriginalTitle, &m.Year, &m.Monitored, &m.HasFile, &m.Queued,
		&m.MinimumAvailability, &m.InCinemas, &m.PhysicalRelease, &m.DigitalRelease, &m.QualityProfileID, &tmdb, &m.IMDbID, &quality, &m.FileRelease)
	m.FileQuality = unmarshalQuality(quality)
	m.TMDbID, _ = strconv.Atoi(tmdb)
	return m, err
}

// GetWantedMovie reads one movie.
func GetWantedMovie(ctx context.Context, q Queryer, movieID int64) (WantedMovie, error) {
	m, err := scanWantedMovie(q.QueryRowContext(ctx, wantedMovieSelect+` WHERE m.id = ?`, movieID))
	if err != nil {
		return WantedMovie{}, fmt.Errorf("get wanted movie %d: %w", movieID, err)
	}
	return m, nil
}

// ListWantedMovies lists monitored movies, with or without a file - what RSS
// sync and the searches consider (the decision engine tells missing from
// upgradable).
func ListWantedMovies(ctx context.Context, q Queryer) ([]WantedMovie, error) {
	rows, err := q.QueryContext(ctx, wantedMovieSelect+`
		WHERE m.monitored = 1
		ORDER BY mm.sort_title`)
	if err != nil {
		return nil, fmt.Errorf("list wanted movies: %w", err)
	}
	defer rows.Close()
	var out []WantedMovie
	for rows.Next() {
		m, err := scanWantedMovie(rows)
		if err != nil {
			return nil, fmt.Errorf("scan wanted movie: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// WantedEpisode is an episode as release decisions see it. Monitored means
// the episode and its season are both monitored.
type WantedEpisode struct {
	ID            int64
	SeasonNumber  int
	EpisodeNumber int
	AirDate       sql.NullTime
	Monitored     bool
	HasFile       bool
	Queued        bool
	FileQuality   releaseparse.FileQuality
	FileRelease   string
}

// WantedSeries is a series with every one of its episodes.
type WantedSeries struct {
	ID               int64
	Title            string
	Year             int
	TVDBID           int
	Monitored        bool
	QualityProfileID sql.NullInt64
	Episodes         []WantedEpisode
}

// Episode finds an episode by season and number.
func (s WantedSeries) Episode(season, episode int) (WantedEpisode, bool) {
	for _, e := range s.Episodes {
		if e.SeasonNumber == season && e.EpisodeNumber == episode {
			return e, true
		}
	}
	return WantedEpisode{}, false
}

// Season lists a season's episodes.
func (s WantedSeries) Season(season int) []WantedEpisode {
	var out []WantedEpisode
	for _, e := range s.Episodes {
		if e.SeasonNumber == season {
			out = append(out, e)
		}
	}
	return out
}

func loadWantedEpisodes(ctx context.Context, q Queryer, s *WantedSeries) error {
	rows, err := q.QueryContext(ctx, `
		SELECT e.id, e.season_number, e.episode_number, e.air_date,
		       e.monitored AND COALESCE(se.monitored, 1),
		       e.episode_file_id IS NOT NULL,
		       COALESCE((SELECT ef.quality FROM episode_files ef WHERE ef.id = e.episode_file_id), '{}'),
		       COALESCE((SELECT g.release_title FROM grabs g WHERE g.series_id = e.series_id AND g.status = 'imported'
		                 AND `+grabCoversEpisode+`
		                 ORDER BY g.added DESC LIMIT 1), ''),
		       EXISTS (SELECT 1 FROM grabs g WHERE g.series_id = e.series_id AND g.status IN (`+queuedGrabStatuses+`)
		               AND `+grabCoversEpisode+`)
		FROM episodes e
		LEFT JOIN seasons se ON se.series_id = e.series_id AND se.season_number = e.season_number
		WHERE e.series_id = ?
		ORDER BY e.season_number, e.episode_number
	`, s.ID)
	if err != nil {
		return fmt.Errorf("list wanted episodes for series %d: %w", s.ID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var e WantedEpisode
		var quality string
		if err := rows.Scan(&e.ID, &e.SeasonNumber, &e.EpisodeNumber, &e.AirDate, &e.Monitored, &e.HasFile, &quality, &e.FileRelease, &e.Queued); err != nil {
			return fmt.Errorf("scan wanted episode: %w", err)
		}
		e.FileQuality = unmarshalQuality(quality)
		s.Episodes = append(s.Episodes, e)
	}
	return rows.Err()
}

const wantedSeriesSelect = `
	SELECT s.id, sm.title, COALESCE(sm.year, 0), s.monitored, s.quality_profile_id,
	       COALESCE((SELECT e.external_id FROM external_ids e WHERE e.entity_type = 'series' AND e.entity_id = sm.id AND e.provider = 'tvdb'), '')
	FROM series s JOIN series_metadata sm ON sm.id = s.series_metadata_id`

func scanWantedSeries(row interface{ Scan(...any) error }) (WantedSeries, error) {
	var s WantedSeries
	var tvdb string
	err := row.Scan(&s.ID, &s.Title, &s.Year, &s.Monitored, &s.QualityProfileID, &tvdb)
	s.TVDBID, _ = strconv.Atoi(tvdb)
	return s, err
}

// GetWantedSeries reads one series with its episodes.
func GetWantedSeries(ctx context.Context, q Queryer, seriesID int64) (WantedSeries, error) {
	s, err := scanWantedSeries(q.QueryRowContext(ctx, wantedSeriesSelect+` WHERE s.id = ?`, seriesID))
	if err != nil {
		return WantedSeries{}, fmt.Errorf("get wanted series %d: %w", seriesID, err)
	}
	return s, loadWantedEpisodes(ctx, q, &s)
}

// ListWantedSeries lists monitored series with at least one monitored episode
// that has no file, each with all its episodes.
func ListWantedSeries(ctx context.Context, q Queryer) ([]WantedSeries, error) {
	rows, err := q.QueryContext(ctx, wantedSeriesSelect+`
		WHERE s.monitored = 1 AND EXISTS (
			SELECT 1 FROM episodes e
			LEFT JOIN seasons se ON se.series_id = e.series_id AND se.season_number = e.season_number
			WHERE e.series_id = s.id AND e.monitored = 1 AND COALESCE(se.monitored, 1) = 1)
		ORDER BY sm.sort_title`)
	if err != nil {
		return nil, fmt.Errorf("list wanted series: %w", err)
	}
	var out []WantedSeries
	for rows.Next() {
		s, err := scanWantedSeries(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan wanted series: %w", err)
		}
		out = append(out, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := loadWantedEpisodes(ctx, q, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// WantedTrack is one track of an album's chosen release.
type WantedTrack struct {
	ID      int64
	Title   string
	HasFile bool
}

// WantedAlbum is an album as release decisions see it. Monitored means the
// album and (when it has one) its artist are both monitored; Tracks are the
// release FindImportRelease picks.
type WantedAlbum struct {
	ID               int64
	Artist           string
	Title            string
	ReleaseDate      sql.NullTime
	Monitored        bool
	Queued           bool
	QualityProfileID sql.NullInt64
	Tracks           []WantedTrack
}

// FileCount is how many of the album's tracks have files.
func (a WantedAlbum) FileCount() int {
	n := 0
	for _, t := range a.Tracks {
		if t.HasFile {
			n++
		}
	}
	return n
}

const wantedAlbumSelect = `
	SELECT al.id, am.name, al.title, al.release_date, al.monitored AND COALESCE(ar.monitored, 1),
	       EXISTS (SELECT 1 FROM grabs g WHERE g.album_id = al.id AND g.status IN (` + queuedGrabStatuses + `)),
	       ar.quality_profile_id
	FROM albums al
	JOIN artist_metadata am ON am.id = al.artist_metadata_id
	LEFT JOIN artists ar ON ar.artist_metadata_id = al.artist_metadata_id`

func loadWantedTracks(ctx context.Context, q Queryer, a *WantedAlbum) error {
	_, tracks, err := FindImportRelease(ctx, q, a.ID)
	if errors.Is(err, ErrNoAlbumRelease) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, t := range tracks {
		a.Tracks = append(a.Tracks, WantedTrack{ID: t.ID, Title: t.Title, HasFile: t.HasFile})
	}
	return nil
}

// GetWantedAlbum reads one album with its tracks.
func GetWantedAlbum(ctx context.Context, q Queryer, albumID int64) (WantedAlbum, error) {
	var a WantedAlbum
	err := q.QueryRowContext(ctx, wantedAlbumSelect+` WHERE al.id = ?`, albumID).
		Scan(&a.ID, &a.Artist, &a.Title, &a.ReleaseDate, &a.Monitored, &a.Queued, &a.QualityProfileID)
	if err != nil {
		return WantedAlbum{}, fmt.Errorf("get wanted album %d: %w", albumID, err)
	}
	return a, loadWantedTracks(ctx, q, &a)
}

// ListWantedAlbums lists monitored albums (of monitored artists) with no
// track files yet, as Lidarr's Wanted -> Missing does.
func ListWantedAlbums(ctx context.Context, q Queryer) ([]WantedAlbum, error) {
	rows, err := q.QueryContext(ctx, wantedAlbumSelect+`
		WHERE al.monitored = 1 AND COALESCE(ar.monitored, 1) = 1 AND NOT EXISTS (
			SELECT 1 FROM album_releases rel JOIN tracks t ON t.album_release_id = rel.id
			WHERE rel.album_id = al.id AND t.track_file_id IS NOT NULL)
		ORDER BY am.sort_name, al.title`)
	if err != nil {
		return nil, fmt.Errorf("list wanted albums: %w", err)
	}
	var out []WantedAlbum
	for rows.Next() {
		var a WantedAlbum
		if err := rows.Scan(&a.ID, &a.Artist, &a.Title, &a.ReleaseDate, &a.Monitored, &a.Queued, &a.QualityProfileID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan wanted album: %w", err)
		}
		out = append(out, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := loadWantedTracks(ctx, q, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// MissingMonitored reports whether any monitored episode has no file.
func (s WantedSeries) MissingMonitored() bool {
	for _, e := range s.Episodes {
		if e.Monitored && !e.HasFile {
			return true
		}
	}
	return false
}

func unmarshalQuality(raw string) releaseparse.FileQuality {
	var q releaseparse.FileQuality
	_ = json.Unmarshal([]byte(raw), &q)
	return q
}

// AlbumFileQualities is the recorded quality of every track file of one
// album - what a cutoff-unmet search compares against the profile.
func AlbumFileQualities(ctx context.Context, q Queryer, albumID int64) ([]releaseparse.FileQuality, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT tf.quality FROM album_releases r
		JOIN tracks t ON t.album_release_id = r.id
		JOIN track_files tf ON tf.id = t.track_file_id
		WHERE r.album_id = ?`, albumID)
	if err != nil {
		return nil, fmt.Errorf("album %d file qualities: %w", albumID, err)
	}
	defer rows.Close()
	var out []releaseparse.FileQuality
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, unmarshalQuality(raw))
	}
	return out, rows.Err()
}

// ListUpgradableAlbums lists monitored albums (of monitored artists) that
// DO have files - the candidates for a cutoff-unmet search, which is the
// mirror image of what ListWantedAlbums returns.
func ListUpgradableAlbums(ctx context.Context, q Queryer) ([]WantedAlbum, error) {
	rows, err := q.QueryContext(ctx, wantedAlbumSelect+`
		WHERE al.monitored = 1 AND COALESCE(ar.monitored, 1) = 1 AND EXISTS (
			SELECT 1 FROM album_releases rel JOIN tracks t ON t.album_release_id = rel.id
			WHERE rel.album_id = al.id AND t.track_file_id IS NOT NULL)
		ORDER BY am.sort_name, al.title`)
	if err != nil {
		return nil, fmt.Errorf("list upgradable albums: %w", err)
	}
	var out []WantedAlbum
	for rows.Next() {
		var a WantedAlbum
		if err := rows.Scan(&a.ID, &a.Artist, &a.Title, &a.ReleaseDate, &a.Monitored, &a.Queued, &a.QualityProfileID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan upgradable album: %w", err)
		}
		out = append(out, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := loadWantedTracks(ctx, q, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}
