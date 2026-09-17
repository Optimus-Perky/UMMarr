package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/titleutil"
)

// ErrNoAnchorExternalID is returned when a merged *Metadata has no id
// under its expected anchor provider - Upsert<Entity>Metadata can't
// decide insert-vs-update or persist anything meaningful without one.
var ErrNoAnchorExternalID = errors.New("store: no external id from the anchor provider")

// UpsertMovieMetadata creates or updates the movie_metadata row for m,
// keyed on its TMDB id (the only provider a movie can be added by in this
// phase). sort_title/clean_title are computed here from m.Title, not
// accepted as parameters, so there's no way for a caller to pass a
// stale/mismatched value.
func UpsertMovieMetadata(ctx context.Context, q Queryer, m metadata.MovieMetadata) (int64, error) {
	tmdbID, ok := m.ExternalIDs["tmdb"]
	if !ok {
		return 0, fmt.Errorf("upsert movie_metadata: %w", ErrNoAnchorExternalID)
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

	existingID, found, err := FindEntityIDByExternalID(ctx, q, "movie", "tmdb", tmdbID)
	if err != nil {
		return 0, err
	}

	var metadataID int64
	if found {
		_, err = q.ExecContext(ctx, `
			UPDATE movie_metadata SET
				title = ?, sort_title = ?, clean_title = ?, original_title = ?,
				status = ?, year = ?, runtime = ?, in_cinemas = ?,
				physical_release = ?, digital_release = ?, certification = ?,
				overview = ?, studio = ?, collection_title = ?,
				ratings = ?, genres = ?, images = ?, last_info_sync = CURRENT_TIMESTAMP
			WHERE id = ?
		`, m.Title.Value, sortTitle, cleanTitle, m.OriginalTitle.Value,
			m.Status.Value, m.Year.Value, m.Runtime.Value, dateOrNull(m.InCinemas.Value),
			dateOrNull(m.PhysicalRelease.Value), dateOrNull(m.DigitalRelease.Value), m.Certification.Value,
			m.Overview.Value, m.Studio.Value, m.CollectionTitle.Value,
			ratings, genres, images, existingID)
		if err != nil {
			return 0, fmt.Errorf("update movie_metadata: %w", err)
		}
		metadataID = existingID
	} else {
		res, err := q.ExecContext(ctx, `
			INSERT INTO movie_metadata (
				title, sort_title, clean_title, original_title,
				status, year, runtime, in_cinemas,
				physical_release, digital_release, certification,
				overview, studio, collection_title,
				ratings, genres, images, last_info_sync
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		`, m.Title.Value, sortTitle, cleanTitle, m.OriginalTitle.Value,
			m.Status.Value, m.Year.Value, m.Runtime.Value, dateOrNull(m.InCinemas.Value),
			dateOrNull(m.PhysicalRelease.Value), dateOrNull(m.DigitalRelease.Value), m.Certification.Value,
			m.Overview.Value, m.Studio.Value, m.CollectionTitle.Value,
			ratings, genres, images)
		if err != nil {
			return 0, fmt.Errorf("insert movie_metadata: %w", err)
		}
		metadataID, err = res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("get inserted movie_metadata id: %w", err)
		}
	}

	if err := upsertExternalIDs(ctx, q, "movie", metadataID, m.ExternalIDs); err != nil {
		return 0, err
	}
	provenance := map[string]string{
		"title": m.Title.Provider, "original_title": m.OriginalTitle.Provider,
		"status": m.Status.Provider, "year": m.Year.Provider, "runtime": m.Runtime.Provider,
		"in_cinemas": m.InCinemas.Provider, "certification": m.Certification.Provider,
		"overview": m.Overview.Provider, "studio": m.Studio.Provider,
		"collection_title": m.CollectionTitle.Provider, "genres": m.Genres.Provider,
		"images": m.Images.Provider,
	}
	if err := upsertProvenance(ctx, q, "movie", metadataID, provenance); err != nil {
		return 0, err
	}

	return metadataID, nil
}

// UpsertMovie ensures a movies (per-instance tracked) row exists for
// metadataID, returning its id. If one already exists, it's returned
// unchanged - monitored/quality_profile_id/root_folder_id are user-owned
// settings a metadata refresh must never silently overwrite; they're only
// ever set here on first creation.
func UpsertMovie(ctx context.Context, q Queryer, metadataID, qualityProfileID, rootFolderID int64, monitored bool) (int64, error) {
	var existingID int64
	err := q.QueryRowContext(ctx, `SELECT id FROM movies WHERE movie_metadata_id = ?`, metadataID).Scan(&existingID)
	if err == nil {
		return existingID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("find movie by metadata id: %w", err)
	}

	res, err := q.ExecContext(ctx, `
		INSERT INTO movies (movie_metadata_id, monitored, quality_profile_id, root_folder_id)
		VALUES (?, ?, ?, ?)
	`, metadataID, monitored, qualityProfileID, rootFolderID)
	if err != nil {
		return 0, fmt.Errorf("insert movie: %w", err)
	}
	movieID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted movie id: %w", err)
	}

	// Path is resolved and stored only here, on first creation - a later
	// refresh must never silently move a file the user may have
	// reorganized by hand, same reasoning as monitored above.
	path, err := ResolveMoviePath(ctx, q, movieID)
	if err != nil {
		return 0, fmt.Errorf("resolve path for movie %d: %w", movieID, err)
	}
	config, err := GetNamingConfig(ctx, q, "movie")
	if err != nil {
		return 0, err
	}
	if _, err := q.ExecContext(ctx, `UPDATE movies SET path = ?, path_template = ? WHERE id = ?`,
		path, config.MovieFolderFormat.String, movieID); err != nil {
		return 0, fmt.Errorf("set movie path: %w", err)
	}

	return movieID, nil
}

// MovieSummary is a read-side view of one movie for library listing pages
// - a thin projection of the movies+movie_metadata join, not the
// write-side metadata.MovieMetadata shape used by the sync layer.
type MovieSummary struct {
	ID               int64
	RootFolderID     int64
	QualityProfileID sql.NullInt64
	Title            string
	Year             sql.NullInt64
	Path             sql.NullString
	Monitored        bool
	Added            time.Time
	PosterURL        string
	Slug             string // address segment, e.g. the-fast-and-the-furious-2001

	// For the poster grid's options.
	QualityProfileName  string
	HasFile             bool
	MinimumAvailability string
	InCinemas           sql.NullTime
	PhysicalRelease     sql.NullTime
	DigitalRelease      sql.NullTime
	Ratings             map[string]float64
	SizeOnDisk          int64
	FileQuality         releaseparse.FileQuality // of the latest file, when there is one
	Overview            string
}

const movieSummaryQuery = `
	SELECT m.id, m.root_folder_id, m.quality_profile_id, mm.title, mm.year, m.path, m.monitored, m.added, mm.images,
	       COALESCE(qp.name, ''), EXISTS (SELECT 1 FROM movie_files f WHERE f.movie_id = m.id),
	       m.minimum_availability, mm.in_cinemas, mm.physical_release, mm.digital_release, mm.ratings,
	       COALESCE((SELECT SUM(f.size) FROM movie_files f WHERE f.movie_id = m.id), 0),
	       COALESCE((SELECT f.quality FROM movie_files f WHERE f.movie_id = m.id ORDER BY f.id DESC LIMIT 1), '{}'),
	       COALESCE(mm.overview, '')
	FROM movies m JOIN movie_metadata mm ON mm.id = m.movie_metadata_id
	LEFT JOIN quality_profiles qp ON qp.id = m.quality_profile_id
`

// ListMovies lists every tracked movie, newest first.
func ListMovies(ctx context.Context, q Queryer) ([]MovieSummary, error) {
	return queryMovieSummaries(ctx, q, movieSummaryQuery+` ORDER BY m.added DESC`)
}

// ListMoviesMissingFile lists movies with zero movie_files rows - the
// library scan's movie candidate list (internal/sync's
// ImportService.ScanMovieLibrary).
func ListMoviesMissingFile(ctx context.Context, q Queryer) ([]MovieSummary, error) {
	return queryMovieSummaries(ctx, q, movieSummaryQuery+`
		LEFT JOIN movie_files mf ON mf.movie_id = m.id
		WHERE mf.id IS NULL
	`)
}

// ListRecentMovies lists the most recently added movies, for the home
// dashboard's "recently added" strip.
func ListRecentMovies(ctx context.Context, q Queryer, limit int) ([]MovieSummary, error) {
	return queryMovieSummaries(ctx, q, movieSummaryQuery+` ORDER BY m.added DESC LIMIT ?`, limit)
}

// MovieFileInfo is the one file (if any) tracked for a movie - movies get
// at most one file today (no multi-cut/edition support), unlike series
// which have one file per episode.
type MovieFileInfo struct {
	RelativePath string
	Size         sql.NullInt64
	DateAdded    time.Time
	Quality      releaseparse.FileQuality
	MediaInfo    mediainfo.Info
}

// MovieDetail is the full read-side view of one movie for its detail page
// - unlike MovieSummary (the library-grid projection), this includes every
// descriptive metadata field the detail page renders, plus its file if any.
type MovieDetail struct {
	MovieSummary
	Overview           sql.NullString
	Certification      sql.NullString
	Studio             sql.NullString
	CollectionTitle    sql.NullString
	Runtime            sql.NullInt64
	Genres             []string
	Ratings            map[string]float64
	PosterURL          string
	QualityProfileName sql.NullString
	File               *MovieFileInfo // nil if no file tracked yet
}

// GetMovieDetail fetches movieID's full detail view. found is false if no
// movie with that id exists.
func GetMovieDetail(ctx context.Context, q Queryer, movieID int64) (MovieDetail, bool, error) {
	var d MovieDetail
	var genres, images, ratings string
	err := q.QueryRowContext(ctx, `
		SELECT m.id, m.root_folder_id, m.quality_profile_id, mm.title, mm.year, m.path, m.monitored, m.added,
		       mm.overview, mm.certification, mm.studio, mm.collection_title, mm.runtime,
		       mm.genres, mm.images, mm.ratings, qp.name
		FROM movies m
		JOIN movie_metadata mm ON mm.id = m.movie_metadata_id
		LEFT JOIN quality_profiles qp ON qp.id = m.quality_profile_id
		WHERE m.id = ?
	`, movieID).Scan(&d.ID, &d.RootFolderID, &d.QualityProfileID, &d.Title, &d.Year, &d.Path, &d.Monitored, &d.Added,
		&d.Overview, &d.Certification, &d.Studio, &d.CollectionTitle, &d.Runtime,
		&genres, &images, &ratings, &d.QualityProfileName)
	if errors.Is(err, sql.ErrNoRows) {
		return MovieDetail{}, false, nil
	}
	if err != nil {
		return MovieDetail{}, false, fmt.Errorf("get movie detail %d: %w", movieID, err)
	}
	d.Genres = unmarshalStringSlice(genres)
	d.Ratings = unmarshalRatings(ratings)
	d.PosterURL = firstOrEmpty(unmarshalStringSlice(images))
	if url := PosterOverride(ctx, q, "movie", movieID); url != "" {
		d.PosterURL = url
	}

	var relPath sql.NullString
	var size sql.NullInt64
	var dateAdded sql.NullTime
	var mediaInfo string
	var quality string
	err = q.QueryRowContext(ctx, `SELECT relative_path, size, date_added, quality, COALESCE(media_info, '{}') FROM movie_files WHERE movie_id = ?`, movieID).
		Scan(&relPath, &size, &dateAdded, &quality, &mediaInfo)
	if err == nil && relPath.Valid {
		d.File = &MovieFileInfo{
			RelativePath: relPath.String, Size: size, DateAdded: dateAdded.Time,
			Quality: unmarshalFileQuality(quality), MediaInfo: mediainfo.Decode(mediaInfo),
		}
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return MovieDetail{}, false, fmt.Errorf("get movie file for movie %d: %w", movieID, err)
	}

	return d, true, nil
}

// UpdateMovieMonitored sets movieID's monitored flag - the movie detail
// page's clickable Monitored/Unmonitored chip. Unlike UpsertMovie (which
// only sets monitored on first creation), this is the one place it's
// allowed to change afterward, by explicit user action.
func UpdateMovieMonitored(ctx context.Context, q Queryer, movieID int64, monitored bool) error {
	if _, err := q.ExecContext(ctx, `UPDATE movies SET monitored = ? WHERE id = ?`, monitored, movieID); err != nil {
		return fmt.Errorf("update movie %d monitored: %w", movieID, err)
	}
	return nil
}

func queryMovieSummaries(ctx context.Context, q Queryer, query string, args ...any) ([]MovieSummary, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list movies: %w", err)
	}
	defer rows.Close()

	var movies []MovieSummary
	for rows.Next() {
		var m MovieSummary
		var images string
		var ratings, fileQuality string
		if err := rows.Scan(&m.ID, &m.RootFolderID, &m.QualityProfileID, &m.Title, &m.Year, &m.Path, &m.Monitored, &m.Added, &images,
			&m.QualityProfileName, &m.HasFile, &m.MinimumAvailability, &m.InCinemas, &m.PhysicalRelease, &m.DigitalRelease, &ratings, &m.SizeOnDisk, &fileQuality, &m.Overview); err != nil {
			return nil, fmt.Errorf("scan movie summary: %w", err)
		}
		m.PosterURL = firstOrEmpty(unmarshalStringSlice(images))
		m.Slug = titleutil.SlugWithYear(m.Title, m.Year.Int64)
		m.Ratings = unmarshalRatings(ratings)
		m.FileQuality = unmarshalQuality(fileQuality)
		movies = append(movies, m)
	}
	applyPosters(ctx, q, "movie", len(movies), func(i int) int64 { return movies[i].ID }, func(i int, url string) { movies[i].PosterURL = url })
	return movies, rows.Err()
}

// FindMovieIDBySlug resolves an address like the-fast-and-the-furious-2001
// (or just the title part) to a movie. With more than one match, the
// oldest entry wins.
func FindMovieIDBySlug(ctx context.Context, q Queryer, slug string) (int64, bool, error) {
	clean := titleutil.CleanTitle(slug)
	var id int64
	err := q.QueryRowContext(ctx, `
		SELECT m.id FROM movies m JOIN movie_metadata mm ON mm.id = m.movie_metadata_id
		WHERE mm.clean_title = ? OR mm.clean_title || COALESCE(mm.year, '') = ?
		ORDER BY m.id LIMIT 1`, clean, clean).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("find movie by slug %q: %w", slug, err)
	}
	return id, true, nil
}

// SetMoviePath points a movie at the folder it already lives in on disk -
// the library import's case, where the folder was named before UMMarr.
func SetMoviePath(ctx context.Context, q Queryer, movieID int64, path string) error {
	if _, err := q.ExecContext(ctx, `UPDATE movies SET path = ? WHERE id = ?`, path, movieID); err != nil {
		return fmt.Errorf("set movie %d path: %w", movieID, err)
	}
	return nil
}

// DeleteMovie removes a movie and its file rows and grab history from the
// library. Files on disk are the caller's business.
func DeleteMovie(ctx context.Context, q Queryer, movieID int64) error {
	for _, stmt := range []string{
		`DELETE FROM grabs WHERE movie_id = ?`,
		`DELETE FROM movie_files WHERE movie_id = ?`,
		`DELETE FROM movies WHERE id = ?`,
	} {
		if _, err := q.ExecContext(ctx, stmt, movieID); err != nil {
			return fmt.Errorf("delete movie %d: %w", movieID, err)
		}
	}
	return nil
}
