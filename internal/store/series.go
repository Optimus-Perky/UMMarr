package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/titleutil"
)

// UpsertSeriesMetadata creates or updates the series_metadata row for m,
// keyed on its TMDB id (the only provider a series can be added by in
// this phase). Mirrors UpsertMovieMetadata's shape - see its comments.
func UpsertSeriesMetadata(ctx context.Context, q Queryer, m metadata.SeriesMetadata) (int64, error) {
	tmdbID, ok := m.ExternalIDs["tmdb"]
	if !ok {
		return 0, fmt.Errorf("upsert series_metadata: %w", ErrNoAnchorExternalID)
	}

	ratings, err := marshalJSON(m.Ratings, "{}")
	if err != nil {
		return 0, fmt.Errorf("marshal ratings: %w", err)
	}
	genres, err := marshalJSON(m.Genres.Value, "[]")
	if err != nil {
		return 0, fmt.Errorf("marshal genres: %w", err)
	}
	images, err := marshalJSON(m.Images.Value, "[]")
	if err != nil {
		return 0, fmt.Errorf("marshal images: %w", err)
	}

	cleanTitle := titleutil.CleanTitle(m.Title.Value)
	sortTitle := titleutil.SortTitle(m.Title.Value)

	existingID, found, err := FindEntityIDByExternalID(ctx, q, "series", "tmdb", tmdbID)
	if err != nil {
		return 0, err
	}

	var metadataID int64
	if found {
		_, err = q.ExecContext(ctx, `
			UPDATE series_metadata SET
				title = ?, sort_title = ?, clean_title = ?, status = ?,
				overview = ?, network = ?, air_time = ?, series_type = ?,
				certification = ?, year = ?, first_aired = ?, last_aired = ?,
				runtime = ?, original_language = ?,
				ratings = ?, genres = ?, images = ?, last_info_sync = CURRENT_TIMESTAMP
			WHERE id = ?
		`, m.Title.Value, sortTitle, cleanTitle, m.Status.Value,
			m.Overview.Value, m.Network.Value, m.AirTime.Value, orDefault(m.SeriesType.Value, "standard"),
			m.Certification.Value, m.Year.Value, dateOrNull(m.FirstAired.Value), dateOrNull(m.LastAired.Value),
			m.Runtime.Value, m.OriginalLanguage.Value,
			ratings, genres, images, existingID)
		if err != nil {
			return 0, fmt.Errorf("update series_metadata: %w", err)
		}
		metadataID = existingID
	} else {
		res, err := q.ExecContext(ctx, `
			INSERT INTO series_metadata (
				title, sort_title, clean_title, status,
				overview, network, air_time, series_type,
				certification, year, first_aired, last_aired,
				runtime, original_language,
				ratings, genres, images, last_info_sync
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		`, m.Title.Value, sortTitle, cleanTitle, m.Status.Value,
			m.Overview.Value, m.Network.Value, m.AirTime.Value, orDefault(m.SeriesType.Value, "standard"),
			m.Certification.Value, m.Year.Value, dateOrNull(m.FirstAired.Value), dateOrNull(m.LastAired.Value),
			m.Runtime.Value, m.OriginalLanguage.Value,
			ratings, genres, images)
		if err != nil {
			return 0, fmt.Errorf("insert series_metadata: %w", err)
		}
		metadataID, err = res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("get inserted series_metadata id: %w", err)
		}
	}

	if err := upsertExternalIDs(ctx, q, "series", metadataID, m.ExternalIDs); err != nil {
		return 0, err
	}
	provenance := map[string]string{
		"title": m.Title.Provider, "status": m.Status.Provider, "network": m.Network.Provider,
		"overview": m.Overview.Provider, "year": m.Year.Provider, "runtime": m.Runtime.Provider,
		"first_aired": m.FirstAired.Provider, "last_aired": m.LastAired.Provider,
		"original_language": m.OriginalLanguage.Provider, "genres": m.Genres.Provider, "images": m.Images.Provider,
	}
	if err := upsertProvenance(ctx, q, "series", metadataID, provenance); err != nil {
		return 0, err
	}

	return metadataID, nil
}

// UpsertSeries ensures a series (per-instance tracked) row exists for
// metadataID, returning its id unchanged if already present - same
// don't-clobber-user-settings rule as UpsertMovie.
func UpsertSeries(ctx context.Context, q Queryer, metadataID, qualityProfileID, rootFolderID int64, monitored bool) (int64, error) {
	var existingID int64
	err := q.QueryRowContext(ctx, `SELECT id FROM series WHERE series_metadata_id = ?`, metadataID).Scan(&existingID)
	if err == nil {
		return existingID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("find series by metadata id: %w", err)
	}

	res, err := q.ExecContext(ctx, `
		INSERT INTO series (series_metadata_id, monitored, quality_profile_id, root_folder_id)
		VALUES (?, ?, ?, ?)
	`, metadataID, monitored, qualityProfileID, rootFolderID)
	if err != nil {
		return 0, fmt.Errorf("insert series: %w", err)
	}
	seriesID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted series id: %w", err)
	}

	path, err := ResolveSeriesPath(ctx, q, seriesID)
	if err != nil {
		return 0, fmt.Errorf("resolve path for series %d: %w", seriesID, err)
	}
	config, err := GetNamingConfig(ctx, q, "series")
	if err != nil {
		return 0, err
	}
	if _, err := q.ExecContext(ctx, `UPDATE series SET path = ?, path_template = ? WHERE id = ?`,
		path, config.SeriesFolderFormat.String, seriesID); err != nil {
		return 0, fmt.Errorf("set series path: %w", err)
	}

	return seriesID, nil
}

// UpsertSeason ensures a seasons row exists for (seriesID,
// s.SeasonNumber) - seasons have no external-id anchor of their own
// (providers don't expose a useful standalone season id here), so the
// natural key from migration 00005's UNIQUE(series_id, season_number) is
// used instead. Monitored is only ever set on first creation, matching
// the series/movie/artist pattern - a refresh must not silently
// re-monitor a season the user turned off.
// specialsSeasonNumber is the season every provider files one-offs,
// recaps and webisodes under.
const specialsSeasonNumber = 0

func UpsertSeason(ctx context.Context, q Queryer, seriesID int64, s metadata.SeasonMetadata) (int64, error) {
	var existingID int64
	err := q.QueryRowContext(ctx, `SELECT id FROM seasons WHERE series_id = ? AND season_number = ?`, seriesID, s.SeasonNumber).Scan(&existingID)
	if err == nil {
		if s.Poster != "" {
			_, _ = q.ExecContext(ctx, `UPDATE seasons SET poster = ? WHERE id = ?`, s.Poster, existingID)
		}
		return existingID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("find season: %w", err)
	}

	// Season 0 is the specials: one-offs, recaps, behind-the-scenes and
	// webisodes, usually with no air date and often never released as a
	// file at all. Monitoring them by default meant searching every pass
	// for hundreds of episodes that will never be found - 475 of them
	// across this library on 2026-09-16, against 140 for every real
	// season put together.
	res, err := q.ExecContext(ctx, `
		INSERT INTO seasons (series_id, season_number, monitored, poster) VALUES (?, ?, ?, ?)
	`, seriesID, s.SeasonNumber, s.SeasonNumber != specialsSeasonNumber, s.Poster)
	if err != nil {
		return 0, fmt.Errorf("insert season: %w", err)
	}
	seasonID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted season id: %w", err)
	}
	return seasonID, nil
}

// UpsertEpisode creates or updates the episodes row for (seriesID,
// seasonID, e.EpisodeNumber), keyed on migration 00005's
// UNIQUE(series_id, season_number, episode_number). seasonNumber is
// required separately since EpisodeMetadata doesn't carry it (it's a
// property of the enclosing SeasonMetadata the caller is already
// iterating over). On update, only descriptive metadata fields are
// refreshed - monitored is left alone, same reasoning as UpsertSeason.
func UpsertEpisode(ctx context.Context, q Queryer, seriesID, seasonID int64, seasonNumber int, e metadata.EpisodeMetadata) (int64, error) {
	ratings := "{}" // no provider currently supplies per-episode ratings

	var existingID int64
	err := q.QueryRowContext(ctx, `
		SELECT id FROM episodes WHERE season_id = ? AND episode_number = ?
	`, seasonID, e.EpisodeNumber).Scan(&existingID)
	if err == nil {
		_, err = q.ExecContext(ctx, `
			UPDATE episodes SET
				title = ?, air_date = ?, overview = ?, absolute_episode_number = ?,
				runtime = ?, ratings = ?
			WHERE id = ?
		`, e.Title.Value, dateOrNull(e.AirDate.Value), e.Overview.Value, e.AbsoluteEpisodeNumber.Value,
			e.Runtime.Value, ratings, existingID)
		if err != nil {
			return 0, fmt.Errorf("update episode: %w", err)
		}
		if perr := upsertEpisodeProvenance(ctx, q, existingID, e); perr != nil {
			return 0, perr
		}
		return existingID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("find episode: %w", err)
	}

	res, err := q.ExecContext(ctx, `
		INSERT INTO episodes (
			series_id, season_id, season_number, episode_number, title, air_date,
			overview, absolute_episode_number, runtime, ratings
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, seriesID, seasonID, seasonNumber, e.EpisodeNumber, e.Title.Value, dateOrNull(e.AirDate.Value),
		e.Overview.Value, e.AbsoluteEpisodeNumber.Value, e.Runtime.Value, ratings)
	if err != nil {
		return 0, fmt.Errorf("insert episode: %w", err)
	}
	episodeID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted episode id: %w", err)
	}
	if perr := upsertEpisodeProvenance(ctx, q, episodeID, e); perr != nil {
		return 0, perr
	}
	return episodeID, nil
}

func upsertEpisodeProvenance(ctx context.Context, q Queryer, episodeID int64, e metadata.EpisodeMetadata) error {
	return upsertProvenance(ctx, q, "episode", episodeID, map[string]string{
		"title": e.Title.Provider, "air_date": e.AirDate.Provider,
		"overview": e.Overview.Provider, "absolute_episode_number": e.AbsoluteEpisodeNumber.Provider,
		"runtime": e.Runtime.Provider,
	})
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// SeriesSummary is a read-side view of one series for library listing
// pages - a thin projection of the series+series_metadata join.
type SeriesSummary struct {
	ID               int64
	RootFolderID     int64
	QualityProfileID sql.NullInt64
	Title            string
	Status           sql.NullString
	Path             sql.NullString
	Monitored        bool
	Added            time.Time
	PosterURL        string
	Slug             string // address segment, e.g. breaking-bad

	// For the poster grid's options: EpisodeCount is the monitored, aired
	// episodes (an unmonitored season's don't count); EpisodeFileCount those
	// of them with a file.
	QualityProfileName string
	EpisodeCount       int
	EpisodeFileCount   int
	Ratings            map[string]float64

	// For the Overview and Table views.
	Overview    string
	Network     string
	Year        sql.NullInt64
	SeasonCount int // not counting specials
	SizeOnDisk  int64
}

const seriesSummaryQuery = `
	SELECT s.id, s.root_folder_id, s.quality_profile_id, sm.title, sm.status, s.path, s.monitored, s.added, sm.images,
	       COALESCE(qp.name, ''),
	       (SELECT COUNT(*) FROM episodes e LEFT JOIN seasons se ON se.series_id = e.series_id AND se.season_number = e.season_number
	        WHERE e.series_id = s.id AND e.monitored AND COALESCE(se.monitored, 1) = 1 AND e.season_number > 0 AND e.air_date IS NOT NULL AND e.air_date <= date('now')),
	       (SELECT COUNT(*) FROM episodes e LEFT JOIN seasons se ON se.series_id = e.series_id AND se.season_number = e.season_number
	        WHERE e.series_id = s.id AND e.monitored AND COALESCE(se.monitored, 1) = 1 AND e.season_number > 0 AND e.episode_file_id IS NOT NULL),
	       sm.ratings, COALESCE(sm.overview, ''), COALESCE(sm.network, ''), sm.year,
	       (SELECT COUNT(*) FROM seasons se WHERE se.series_id = s.id AND se.season_number > 0),
	       COALESCE((SELECT SUM(ef.size) FROM episode_files ef JOIN episodes e ON e.id = ef.episode_id WHERE e.series_id = s.id), 0)
	FROM series s JOIN series_metadata sm ON sm.id = s.series_metadata_id
	LEFT JOIN quality_profiles qp ON qp.id = s.quality_profile_id
`

// ListSeries lists every tracked series, newest first.
func ListSeries(ctx context.Context, q Queryer) ([]SeriesSummary, error) {
	return querySeriesSummaries(ctx, q, seriesSummaryQuery+` ORDER BY s.added DESC`)
}

// SeriesWithMissingEpisodes lists series with at least one episode
// lacking a file - the library scan's series candidate list (skips
// fully-complete series without ever walking their folders on disk).
func SeriesWithMissingEpisodes(ctx context.Context, q Queryer) ([]SeriesSummary, error) {
	return querySeriesSummaries(ctx, q, seriesSummaryQuery+`
		WHERE EXISTS (SELECT 1 FROM episodes e WHERE e.series_id = s.id AND e.episode_file_id IS NULL)
	`)
}

// ListRecentSeries lists the most recently added series, for the home
// dashboard's "recently added" strip.
func ListRecentSeries(ctx context.Context, q Queryer, limit int) ([]SeriesSummary, error) {
	return querySeriesSummaries(ctx, q, seriesSummaryQuery+` ORDER BY s.added DESC LIMIT ?`, limit)
}

func querySeriesSummaries(ctx context.Context, q Queryer, query string, args ...any) ([]SeriesSummary, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list series: %w", err)
	}
	defer rows.Close()

	var series []SeriesSummary
	for rows.Next() {
		var s SeriesSummary
		var images string
		var ratings string
		if err := rows.Scan(&s.ID, &s.RootFolderID, &s.QualityProfileID, &s.Title, &s.Status, &s.Path, &s.Monitored, &s.Added, &images,
			&s.QualityProfileName, &s.EpisodeCount, &s.EpisodeFileCount, &ratings, &s.Overview, &s.Network, &s.Year, &s.SeasonCount, &s.SizeOnDisk); err != nil {
			return nil, fmt.Errorf("scan series summary: %w", err)
		}
		s.PosterURL = firstOrEmpty(unmarshalStringSlice(images))
		s.Slug = titleutil.Slug(s.Title)
		s.Ratings = unmarshalRatings(ratings)
		series = append(series, s)
	}
	applyPosters(ctx, q, "series", len(series), func(i int) int64 { return series[i].ID }, func(i int, url string) { series[i].PosterURL = url })
	return series, rows.Err()
}

// SeriesEpisodeCounts returns the total and downloaded episode counts for
// a series - used to render "71/71 eps" style status on the library page.
func SeriesEpisodeCounts(ctx context.Context, q Queryer, seriesID int64) (total, downloaded int, err error) {
	row := q.QueryRowContext(ctx, `
		SELECT COUNT(*), COUNT(episode_file_id) FROM episodes WHERE series_id = ?
	`, seriesID)
	if err := row.Scan(&total, &downloaded); err != nil {
		return 0, 0, fmt.Errorf("count episodes for series %d: %w", seriesID, err)
	}
	return total, downloaded, nil
}

// SeriesDetail is the full read-side view of one series for its detail
// page - unlike SeriesSummary, includes every descriptive metadata field
// the detail page renders.
type SeriesDetail struct {
	SeriesSummary
	Overview           sql.NullString
	Network            sql.NullString
	Certification      sql.NullString
	Runtime            sql.NullInt64
	Year               sql.NullInt64
	Genres             []string
	Ratings            map[string]float64
	PosterURL          string
	QualityProfileName sql.NullString
	MetadataID         int64
	FirstAired         sql.NullTime
	LastAired          sql.NullTime
	OriginalLanguage   sql.NullString
}

// GetSeriesDetail fetches seriesID's full detail view. found is false if
// no series with that id exists.
func GetSeriesDetail(ctx context.Context, q Queryer, seriesID int64) (SeriesDetail, bool, error) {
	var d SeriesDetail
	var genres, images, ratings string
	err := q.QueryRowContext(ctx, `
		SELECT s.id, s.root_folder_id, s.quality_profile_id, sm.title, sm.status, s.path, s.monitored, s.added,
		       sm.overview, sm.network, sm.certification, sm.runtime, sm.year,
		       sm.genres, sm.images, sm.ratings, qp.name, sm.id, sm.first_aired, sm.last_aired, sm.original_language
		FROM series s
		JOIN series_metadata sm ON sm.id = s.series_metadata_id
		LEFT JOIN quality_profiles qp ON qp.id = s.quality_profile_id
		WHERE s.id = ?
	`, seriesID).Scan(&d.ID, &d.RootFolderID, &d.QualityProfileID, &d.Title, &d.Status, &d.Path, &d.Monitored, &d.Added,
		&d.Overview, &d.Network, &d.Certification, &d.Runtime, &d.Year,
		&genres, &images, &ratings, &d.QualityProfileName, &d.MetadataID, &d.FirstAired, &d.LastAired, &d.OriginalLanguage)
	if errors.Is(err, sql.ErrNoRows) {
		return SeriesDetail{}, false, nil
	}
	if err != nil {
		return SeriesDetail{}, false, fmt.Errorf("get series detail %d: %w", seriesID, err)
	}
	d.Genres = unmarshalStringSlice(genres)
	d.Ratings = unmarshalRatings(ratings)
	d.PosterURL = firstOrEmpty(unmarshalStringSlice(images))
	return d, true, nil
}

// UpdateSeriesMonitored sets seriesID's monitored flag - the series
// detail page's clickable Monitored/Unmonitored chip. See
// UpdateMovieMonitored's comment for why this is separate from
// UpsertSeries's one-time set.
func UpdateSeriesMonitored(ctx context.Context, q Queryer, seriesID int64, monitored bool) error {
	if _, err := q.ExecContext(ctx, `UPDATE series SET monitored = ? WHERE id = ?`, monitored, seriesID); err != nil {
		return fmt.Errorf("update series %d monitored: %w", seriesID, err)
	}
	return nil
}

// SeasonSummary is one season row for a series detail page's season list -
// per-season episode/downloaded counts, mirroring SeriesEpisodeCounts but
// broken out GROUP BY season_number instead of aggregated whole-series.
type SeasonSummary struct {
	SeasonNumber int
	Monitored    bool
	Poster       string
	Total        int
	Downloaded   int
}

// ListSeasonsForSeries lists seriesID's seasons in order, with per-season
// episode/downloaded counts - the series detail page's season list.
func ListSeasonsForSeries(ctx context.Context, q Queryer, seriesID int64) ([]SeasonSummary, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT se.season_number, se.monitored, se.poster, COUNT(e.id), COUNT(e.episode_file_id)
		FROM seasons se
		LEFT JOIN episodes e ON e.season_id = se.id
		WHERE se.series_id = ?
		GROUP BY se.id
		ORDER BY se.season_number ASC
	`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("list seasons for series %d: %w", seriesID, err)
	}
	defer rows.Close()

	var seasons []SeasonSummary
	for rows.Next() {
		var s SeasonSummary
		if err := rows.Scan(&s.SeasonNumber, &s.Monitored, &s.Poster, &s.Total, &s.Downloaded); err != nil {
			return nil, fmt.Errorf("scan season summary: %w", err)
		}
		seasons = append(seasons, s)
	}
	return seasons, rows.Err()
}

// UpdateSeasonMonitored sets one season's monitored flag, identified by
// (seriesID, seasonNumber) - seasons have no natural single-argument
// lookup exposed at the API layer (see ListSeasonsForSeries), same
// natural-key reasoning as UpsertSeason.
func UpdateSeasonMonitored(ctx context.Context, q Queryer, seriesID int64, seasonNumber int, monitored bool) error {
	if _, err := q.ExecContext(ctx, `UPDATE seasons SET monitored = ? WHERE series_id = ? AND season_number = ?`,
		monitored, seriesID, seasonNumber); err != nil {
		return fmt.Errorf("update series %d season %d monitored: %w", seriesID, seasonNumber, err)
	}
	return nil
}

// EpisodeDetail is one episode row for a series detail page's episode
// list - HasFile mirrors TrackImportInfo's track_file_id IS NOT NULL
// pattern (internal/store/import.go), applied to episode_file_id.
type EpisodeDetail struct {
	ID            int64
	SeasonNumber  int
	EpisodeNumber int
	Title         sql.NullString
	AirDate       sql.NullTime
	Monitored     bool
	HasFile       bool
	FileSize      sql.NullInt64
	Quality       releaseparse.FileQuality
	MediaInfo     mediainfo.Info
}

// ListEpisodesForSeries lists every episode of seriesID, ordered by
// season/episode number - the series detail page's episode list. No
// per-series aggregate/list function existed for individual episode rows
// before this (only SeriesEpisodeCounts' whole-series totals).
func ListEpisodesForSeries(ctx context.Context, q Queryer, seriesID int64) ([]EpisodeDetail, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT e.id, e.season_number, e.episode_number, e.title, e.air_date, e.monitored,
		       e.episode_file_id IS NOT NULL, ef.size, COALESCE(ef.quality, '{}'), COALESCE(ef.media_info, '{}')
		FROM episodes e
		LEFT JOIN episode_files ef ON ef.id = e.episode_file_id
		WHERE e.series_id = ?
		ORDER BY e.season_number ASC, e.episode_number ASC
	`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("list episodes for series %d: %w", seriesID, err)
	}
	defer rows.Close()

	var episodes []EpisodeDetail
	for rows.Next() {
		var e EpisodeDetail
		var quality, mediaInfo string
		if err := rows.Scan(&e.ID, &e.SeasonNumber, &e.EpisodeNumber, &e.Title, &e.AirDate, &e.Monitored,
			&e.HasFile, &e.FileSize, &quality, &mediaInfo); err != nil {
			return nil, fmt.Errorf("scan episode detail: %w", err)
		}
		e.Quality = unmarshalFileQuality(quality)
		e.MediaInfo = mediainfo.Decode(mediaInfo)
		episodes = append(episodes, e)
	}
	return episodes, rows.Err()
}

// UpdateEpisodeMonitored sets one episode's monitored flag.
func UpdateEpisodeMonitored(ctx context.Context, q Queryer, episodeID int64, monitored bool) error {
	if _, err := q.ExecContext(ctx, `UPDATE episodes SET monitored = ? WHERE id = ?`, monitored, episodeID); err != nil {
		return fmt.Errorf("update episode %d monitored: %w", episodeID, err)
	}
	return nil
}

// EpisodeFileRef is one episode file with what renaming it needs.
type EpisodeFileRef struct {
	FileID       int64
	EpisodeID    int64
	SeasonNumber int
	RelativePath string
	Size         int64
}

// ListEpisodeFileRefs lists a series' episode files in season and episode order.
func ListEpisodeFileRefs(ctx context.Context, q Queryer, seriesID int64) ([]EpisodeFileRef, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT ef.id, e.id, e.season_number, ef.relative_path, COALESCE(ef.size, 0)
		FROM episode_files ef JOIN episodes e ON e.id = ef.episode_id
		WHERE e.series_id = ? ORDER BY e.season_number, e.episode_number`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("list episode files for series %d: %w", seriesID, err)
	}
	defer rows.Close()
	var out []EpisodeFileRef
	for rows.Next() {
		var r EpisodeFileRef
		if err := rows.Scan(&r.FileID, &r.EpisodeID, &r.SeasonNumber, &r.RelativePath, &r.Size); err != nil {
			return nil, fmt.Errorf("scan episode file: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateEpisodeFilePath records where an episode file now is, after a rename.
func UpdateEpisodeFilePath(ctx context.Context, q Queryer, fileID int64, relativePath string) error {
	if _, err := q.ExecContext(ctx, `UPDATE episode_files SET relative_path = ? WHERE id = ?`, relativePath, fileID); err != nil {
		return fmt.Errorf("update episode_file %d path: %w", fileID, err)
	}
	return nil
}

// SeriesSizeOnDisk is the total size of a series' episode files as recorded.
func SeriesSizeOnDisk(ctx context.Context, q Queryer, seriesID int64) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(ef.size), 0) FROM episode_files ef JOIN episodes e ON e.id = ef.episode_id WHERE e.series_id = ?`, seriesID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("series %d size: %w", seriesID, err)
	}
	return n, nil
}

// DeleteSeries removes a series and everything hanging off it: its grabs,
// episode files, episodes and seasons. The shared series_metadata row stays
// (it's harmless and can be re-added). Nothing on disk is touched here.
func DeleteSeries(ctx context.Context, q Queryer, seriesID int64) error {
	for _, stmt := range []string{
		`DELETE FROM grabs WHERE series_id = ?`,
		`DELETE FROM episode_files WHERE episode_id IN (SELECT id FROM episodes WHERE series_id = ?)`,
		`DELETE FROM episodes WHERE series_id = ?`,
		`DELETE FROM seasons WHERE series_id = ?`,
		`DELETE FROM series WHERE id = ?`,
	} {
		if _, err := q.ExecContext(ctx, stmt, seriesID); err != nil {
			return fmt.Errorf("delete series %d: %w", seriesID, err)
		}
	}
	return nil
}

// FindSeriesIDBySlug resolves an address like breaking-bad (or with the
// year, breaking-bad-2008) to a series. With more than one match, the
// oldest entry wins.
func FindSeriesIDBySlug(ctx context.Context, q Queryer, slug string) (int64, bool, error) {
	clean := titleutil.CleanTitle(slug)
	var id int64
	err := q.QueryRowContext(ctx, `
		SELECT s.id FROM series s JOIN series_metadata sm ON sm.id = s.series_metadata_id
		WHERE sm.clean_title = ? OR sm.clean_title || COALESCE(sm.year, '') = ?
		ORDER BY s.id LIMIT 1`, clean, clean).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("find series by slug %q: %w", slug, err)
	}
	return id, true, nil
}

// EpisodeFileInfo is an episode's file as the episode dialog shows it.
type EpisodeFileInfo struct {
	RelativePath string
	Size         sql.NullInt64
	Quality      releaseparse.FileQuality
	DateAdded    time.Time
	MediaInfo    mediainfo.Info
}

// EpisodeInfo is one episode with what its dialog needs: airing details,
// overview, the series' profile and folder, and its file if it has one.
type EpisodeInfo struct {
	ID                 int64
	SeriesID           int64
	SeriesTitle        string
	SeasonNumber       int
	EpisodeNumber      int
	Title              sql.NullString
	AirDate            sql.NullTime
	Overview           sql.NullString
	Runtime            sql.NullInt64
	Monitored          bool
	Network            sql.NullString
	AirTime            sql.NullString
	QualityProfileName sql.NullString
	SeriesPath         sql.NullString
	File               *EpisodeFileInfo
}

// GetEpisodeInfo reads one episode for its dialog.
func GetEpisodeInfo(ctx context.Context, q Queryer, episodeID int64) (EpisodeInfo, bool, error) {
	var e EpisodeInfo
	var fileID sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT e.id, e.series_id, sm.title, e.season_number, e.episode_number, e.title, e.air_date, e.overview, e.runtime, e.monitored,
		       sm.network, sm.air_time, qp.name, s.path, e.episode_file_id
		FROM episodes e
		JOIN series s ON s.id = e.series_id
		JOIN series_metadata sm ON sm.id = s.series_metadata_id
		LEFT JOIN quality_profiles qp ON qp.id = s.quality_profile_id
		WHERE e.id = ?`, episodeID).Scan(&e.ID, &e.SeriesID, &e.SeriesTitle, &e.SeasonNumber, &e.EpisodeNumber, &e.Title, &e.AirDate, &e.Overview, &e.Runtime, &e.Monitored,
		&e.Network, &e.AirTime, &e.QualityProfileName, &e.SeriesPath, &fileID)
	if errors.Is(err, sql.ErrNoRows) {
		return EpisodeInfo{}, false, nil
	}
	if err != nil {
		return EpisodeInfo{}, false, fmt.Errorf("get episode %d: %w", episodeID, err)
	}
	if fileID.Valid {
		var f EpisodeFileInfo
		var quality string
		err := q.QueryRowContext(ctx, `SELECT relative_path, size, quality, date_added FROM episode_files WHERE id = ?`, fileID.Int64).
			Scan(&f.RelativePath, &f.Size, &quality, &f.DateAdded)
		if err == nil {
			_ = json.Unmarshal([]byte(quality), &f.Quality)
			e.File = &f
			var mi string
			if q.QueryRowContext(ctx, `SELECT COALESCE(ef.media_info, '{}') FROM episodes e JOIN episode_files ef ON ef.id = e.episode_file_id WHERE e.id = ?`, episodeID).Scan(&mi) == nil {
				f.MediaInfo = mediainfo.Decode(mi)
			}
		}
	}
	return e, true, nil
}

// EpisodeFileQualitiesBySeries lists the recorded quality of every episode
// file, by series - what the TV page needs to count files below the cutoff.
func EpisodeFileQualitiesBySeries(ctx context.Context, q Queryer) (map[int64][]releaseparse.FileQuality, error) {
	rows, err := q.QueryContext(ctx, `SELECT e.series_id, ef.quality FROM episodes e JOIN episode_files ef ON ef.id = e.episode_file_id`)
	if err != nil {
		return nil, fmt.Errorf("episode file qualities: %w", err)
	}
	defer rows.Close()
	out := map[int64][]releaseparse.FileQuality{}
	for rows.Next() {
		var seriesID int64
		var raw string
		if err := rows.Scan(&seriesID, &raw); err != nil {
			return nil, err
		}
		out[seriesID] = append(out[seriesID], unmarshalQuality(raw))
	}
	return out, rows.Err()
}

// UpdateEpisodeDetails refreshes one episode's name, summary, air date and
// runtime from merged metadata, leaving everything else (its files,
// monitoring, numbering) alone. It reports whether anything changed, and
// never replaces a real name with a placeholder like "Episode 5".
func UpdateEpisodeDetails(ctx context.Context, q Queryer, episodeID int64, e metadata.EpisodeMetadata) (bool, error) {
	var currentTitle string
	var currentOverview, currentAir sql.NullString
	var currentRuntime sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT title, overview, air_date, runtime FROM episodes WHERE id = ?`, episodeID).
		Scan(&currentTitle, &currentOverview, &currentAir, &currentRuntime); err != nil {
		return false, fmt.Errorf("read episode %d: %w", episodeID, err)
	}
	title := currentTitle
	if e.Title.Value != "" && !placeholderTitle(e.Title.Value) {
		title = e.Title.Value
	} else if placeholderTitle(currentTitle) || currentTitle == "" {
		title = e.Title.Value
	}
	overview := currentOverview.String
	if e.Overview.Value != "" {
		overview = e.Overview.Value
	}
	changed := title != currentTitle || overview != currentOverview.String
	if _, err := q.ExecContext(ctx, `UPDATE episodes SET title = ?, overview = ? WHERE id = ?`, title, overview, episodeID); err != nil {
		return false, fmt.Errorf("update episode %d: %w", episodeID, err)
	}
	if e.AirDate.Value != nil {
		if _, err := q.ExecContext(ctx, `UPDATE episodes SET air_date = ? WHERE id = ?`, *e.AirDate.Value, episodeID); err != nil {
			return false, fmt.Errorf("update episode %d air date: %w", episodeID, err)
		}
	}
	if e.Runtime.Value > 0 {
		if _, err := q.ExecContext(ctx, `UPDATE episodes SET runtime = ? WHERE id = ?`, e.Runtime.Value, episodeID); err != nil {
			return false, fmt.Errorf("update episode %d runtime: %w", episodeID, err)
		}
	}
	if changed {
		_ = upsertEpisodeProvenance(ctx, q, episodeID, e)
	}
	return changed && title != "" && !placeholderTitle(title), nil
}

// placeholderTitle matches the stand-in names providers use before the real
// ones are announced, e.g. "Episode 5" or "TBA".
func placeholderTitle(title string) bool { return placeholderTitlePattern.MatchString(title) }

var placeholderTitlePattern = regexp.MustCompile(`(?i)^\s*(episode|ep\.?|chapter|part)\s*#?\s*\d+\s*$|^\s*(tba|tbd|to be announced)\s*$`)

// SeriesWithUnnamedEpisodes lists series that still have episodes with no
// name or only a placeholder - what the episode name search looks at.
func SeriesWithUnnamedEpisodes(ctx context.Context, q Queryer) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT DISTINCT se.series_id
		FROM episodes e JOIN seasons se ON se.id = e.season_id
		WHERE e.title = '' OR e.title IS NULL
		   OR e.title GLOB 'Episode [0-9]*' OR e.title GLOB 'Ep [0-9]*'
		   OR e.title IN ('TBA', 'TBD')
		ORDER BY se.series_id`)
	if err != nil {
		return nil, fmt.Errorf("series with unnamed episodes: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
