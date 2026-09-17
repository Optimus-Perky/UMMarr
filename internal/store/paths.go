package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/pathbuilder"
)

// variousArtistsMBID is MusicBrainz's real "Various Artists" artist
// entity - the schema has no is_various_artists flag, so detection is by
// comparing an artist's musicbrainz external id against this constant
// (via GetExternalID) rather than a fragile name match or a new migration.
const variousArtistsMBID = "89ad4ac3-39f7-470e-963a-56509c546377"

// NamingConfig mirrors one naming_config row (migration 00001, extended by
// 00012 for the two *_file_format columns and 00017 for rename_files).
type NamingConfig struct {
	MovieFolderFormat    sql.NullString
	SeriesFolderFormat   sql.NullString
	SeasonFolderFormat   sql.NullString
	ArtistFolderFormat   sql.NullString
	AlbumFolderFormat    sql.NullString
	VASeriesFolderFormat sql.NullString
	TrackFileFormat      sql.NullString
	MovieFileFormat      sql.NullString
	EpisodeFileFormat    sql.NullString
	// RenameFiles false keeps an imported file's own name instead of the
	// file name template.
	RenameFiles bool
}

// GetNamingConfig reads the single naming_config row for mediaType
// ("movie"/"series"/"music") - always present, seeded by migration 00001.
func GetNamingConfig(ctx context.Context, q Queryer, mediaType string) (NamingConfig, error) {
	var c NamingConfig
	err := q.QueryRowContext(ctx, `
		SELECT movie_folder_format, series_folder_format, season_folder_format,
		       artist_folder_format, album_folder_format, va_series_folder_format,
		       track_file_format, movie_file_format, episode_file_format, rename_files
		FROM naming_config WHERE media_type = ?
	`, mediaType).Scan(&c.MovieFolderFormat, &c.SeriesFolderFormat, &c.SeasonFolderFormat,
		&c.ArtistFolderFormat, &c.AlbumFolderFormat, &c.VASeriesFolderFormat, &c.TrackFileFormat,
		&c.MovieFileFormat, &c.EpisodeFileFormat, &c.RenameFiles)
	if err != nil {
		return NamingConfig{}, fmt.Errorf("get naming_config for %s: %w", mediaType, err)
	}
	return c, nil
}

// GetRootFolderPath reads a root folder's filesystem path by id.
func GetRootFolderPath(ctx context.Context, q Queryer, rootFolderID int64) (string, error) {
	var path string
	if err := q.QueryRowContext(ctx, `SELECT path FROM root_folders WHERE id = ?`, rootFolderID).Scan(&path); err != nil {
		return "", fmt.Errorf("get root folder %d: %w", rootFolderID, err)
	}
	return path, nil
}

// MovieFolderPath is the folder movieID's files belong in: the one saved when
// the movie was added. Anything that puts files into or looks for files in an
// existing item's folder must use this (or its siblings below), never the
// Resolve*Path functions - those follow the current folder template, so after
// a template change they point at a folder the item doesn't live in, and a
// download sent there is invisible to Refresh, which prunes its entry.
func MovieFolderPath(ctx context.Context, q Queryer, movieID int64) (string, error) {
	return savedFolderPath(ctx, q, "movies", movieID, func() (string, error) { return ResolveMoviePath(ctx, q, movieID) })
}

// SeriesFolderPath is MovieFolderPath for a series.
func SeriesFolderPath(ctx context.Context, q Queryer, seriesID int64) (string, error) {
	return savedFolderPath(ctx, q, "series", seriesID, func() (string, error) { return ResolveSeriesPath(ctx, q, seriesID) })
}

// ArtistFolderPath is MovieFolderPath for an artist.
func ArtistFolderPath(ctx context.Context, q Queryer, artistID int64) (string, error) {
	return savedFolderPath(ctx, q, "artists", artistID, func() (string, error) { return ResolveArtistPath(ctx, q, artistID) })
}

// AlbumFolderPath is MovieFolderPath for an album.
func AlbumFolderPath(ctx context.Context, q Queryer, albumID int64) (string, error) {
	return savedFolderPath(ctx, q, "albums", albumID, func() (string, error) { return ResolveAlbumPath(ctx, q, albumID) })
}

// savedFolderPath reads table's saved path for id. An item with no saved path
// yet gets one resolved from the template and saved, so later lookups - and
// Refresh, which reads the column directly - agree on where it lives.
// table is always one of the fixed names above, never user input.
func savedFolderPath(ctx context.Context, q Queryer, table string, id int64, resolve func() (string, error)) (string, error) {
	var path sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT path FROM `+table+` WHERE id = ?`, id).Scan(&path); err != nil {
		return "", fmt.Errorf("get saved path for %s %d: %w", table, id, err)
	}
	if path.Valid && path.String != "" {
		return path.String, nil
	}
	resolved, err := resolve()
	if err != nil {
		return "", err
	}
	if _, err := q.ExecContext(ctx, `UPDATE `+table+` SET path = ? WHERE id = ? AND (path IS NULL OR path = '')`, resolved, id); err != nil {
		return "", fmt.Errorf("save path for %s %d: %w", table, id, err)
	}
	return resolved, nil
}

// ResolveMoviePath computes the full path a movie should live at, from
// its root folder and the seeded movie naming template. Read-only - does
// not write movies.path; see UpsertMovie for where the result gets
// persisted (on first creation only).
func ResolveMoviePath(ctx context.Context, q Queryer, movieID int64) (string, error) {
	var rootFolderID int64
	var title string
	var year sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT m.root_folder_id, mm.title, mm.year
		FROM movies m JOIN movie_metadata mm ON mm.id = m.movie_metadata_id
		WHERE m.id = ?
	`, movieID).Scan(&rootFolderID, &title, &year)
	if err != nil {
		return "", fmt.Errorf("resolve movie path %d: %w", movieID, err)
	}

	root, err := GetRootFolderPath(ctx, q, rootFolderID)
	if err != nil {
		return "", err
	}
	config, err := GetNamingConfig(ctx, q, "movie")
	if err != nil {
		return "", err
	}
	opts, err := pathOptions(ctx, q)
	if err != nil {
		return "", err
	}

	tokens := map[string]string{"Movie Title": title}
	if year.Valid {
		tokens["Release Year"] = strconv.FormatInt(year.Int64, 10)
	}
	segments := pathbuilder.ResolveTemplatePath(config.MovieFolderFormat.String, tokens, opts)
	return pathbuilder.JoinSegments(append([]string{root}, segments...)...), nil
}

// ResolveSeriesPath computes the full path a series should live at.
func ResolveSeriesPath(ctx context.Context, q Queryer, seriesID int64) (string, error) {
	var rootFolderID int64
	var title string
	err := q.QueryRowContext(ctx, `
		SELECT s.root_folder_id, sm.title
		FROM series s JOIN series_metadata sm ON sm.id = s.series_metadata_id
		WHERE s.id = ?
	`, seriesID).Scan(&rootFolderID, &title)
	if err != nil {
		return "", fmt.Errorf("resolve series path %d: %w", seriesID, err)
	}

	root, err := GetRootFolderPath(ctx, q, rootFolderID)
	if err != nil {
		return "", err
	}
	config, err := GetNamingConfig(ctx, q, "series")
	if err != nil {
		return "", err
	}
	opts, err := pathOptions(ctx, q)
	if err != nil {
		return "", err
	}

	segments := pathbuilder.ResolveTemplatePath(config.SeriesFolderFormat.String, map[string]string{"Series Title": title}, opts)
	return pathbuilder.JoinSegments(append([]string{root}, segments...)...), nil
}

// ResolveSeasonPath computes a season's path given its parent series'
// already-resolved path. Pure (no DB access) since a season has no path
// column of its own to read config for beyond what the caller already has
// - override, if non-nil and non-empty, wins outright (a user-set path
// override); otherwise the subfolder is appended only if seasonFolder is
// true, matching Sonarr's per-series season-folder toggle.
func ResolveSeasonPath(seriesPath, seasonFolderFormat string, seasonNumber int, seasonFolder bool, override *string, opts pathbuilder.Options) string {
	if override != nil && *override != "" {
		return *override
	}
	if !seasonFolder {
		return seriesPath
	}
	segments := pathbuilder.ResolveTemplatePath(seasonFolderFormat, map[string]string{"season": strconv.Itoa(seasonNumber)}, opts)
	return pathbuilder.JoinSegments(append([]string{seriesPath}, segments...)...)
}

// ResolveEpisodeFolderPath computes the folder an episode file for
// (seriesID, seasonNumber) should live in: the series' saved folder (see
// SeriesFolderPath) plus the season subfolder from the current template.
//
// seasons.season_folder_override is typed TEXT, not BOOLEAN, unlike every
// other flag in this schema - and nothing in this codebase has ever read
// or written it before this function. This establishes the convention:
// NULL means no override (use the series' own season_folder); a non-NULL
// value of "1" or "true" (case-insensitive) means force season folders
// on, anything else means force them off.
//
// If no seasons row exists for this season number - e.g. a season absent
// from every metadata provider's known season list, so UpsertSeason never
// created one - this falls back to the series' own path directly (no
// season subfolder, no override applied), on the theory that an
// unrecognized season number is better placed loosely under the series
// than to hard-fail an otherwise-successful import.
func ResolveEpisodeFolderPath(ctx context.Context, q Queryer, seriesID int64, seasonNumber int) (string, error) {
	seriesPath, err := SeriesFolderPath(ctx, q, seriesID)
	if err != nil {
		return "", err
	}

	var seasonFolder bool
	if err := q.QueryRowContext(ctx, `SELECT season_folder FROM series WHERE id = ?`, seriesID).Scan(&seasonFolder); err != nil {
		return "", fmt.Errorf("resolve episode folder path: get season_folder for series %d: %w", seriesID, err)
	}
	config, err := GetNamingConfig(ctx, q, "series")
	if err != nil {
		return "", err
	}
	opts, err := pathOptions(ctx, q)
	if err != nil {
		return "", err
	}

	var override, pathOverride sql.NullString
	err = q.QueryRowContext(ctx, `
		SELECT season_folder_override, path_override FROM seasons
		WHERE series_id = ? AND season_number = ?
	`, seriesID, seasonNumber).Scan(&override, &pathOverride)
	switch {
	case err == nil:
		effectiveFolder := seasonFolder
		if override.Valid {
			effectiveFolder = override.String == "1" || strings.EqualFold(override.String, "true")
		}
		var pathOverridePtr *string
		if pathOverride.Valid {
			pathOverridePtr = &pathOverride.String
		}
		return ResolveSeasonPath(seriesPath, config.SeasonFolderFormat.String, seasonNumber, effectiveFolder, pathOverridePtr, opts), nil
	case errors.Is(err, sql.ErrNoRows):
		return seriesPath, nil
	default:
		return "", fmt.Errorf("resolve episode folder path: get season override for series %d season %d: %w", seriesID, seasonNumber, err)
	}
}

// ResolveArtistPath computes the full path an artist should live at.
func ResolveArtistPath(ctx context.Context, q Queryer, artistID int64) (string, error) {
	var rootFolderID int64
	var name string
	err := q.QueryRowContext(ctx, `
		SELECT a.root_folder_id, am.name
		FROM artists a JOIN artist_metadata am ON am.id = a.artist_metadata_id
		WHERE a.id = ?
	`, artistID).Scan(&rootFolderID, &name)
	if err != nil {
		return "", fmt.Errorf("resolve artist path %d: %w", artistID, err)
	}

	root, err := GetRootFolderPath(ctx, q, rootFolderID)
	if err != nil {
		return "", err
	}
	config, err := GetNamingConfig(ctx, q, "music")
	if err != nil {
		return "", err
	}
	opts, err := pathOptions(ctx, q)
	if err != nil {
		return "", err
	}

	segments := pathbuilder.ResolveTemplatePath(config.ArtistFolderFormat.String, map[string]string{"Artist Name": name}, opts)
	return pathbuilder.JoinSegments(append([]string{root}, segments...)...), nil
}

// ResolveAlbumPath computes the full path an album should live at,
// branching on whether its artist is Various Artists and, if so, whether
// the album is linked to a compilation series - see the plan's
// folder-path-modeling section for the four cases this implements. A
// normal album goes under its artist's saved folder (see ArtistFolderPath),
// so an album added after the artist template changed still lands beside
// that artist's other albums.
func ResolveAlbumPath(ctx context.Context, q Queryer, albumID int64) (string, error) {
	var artistMetadataID int64
	var artistID sql.NullInt64
	var title string
	var year sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT al.artist_metadata_id, ar.id, al.title,
		       CAST(strftime('%Y', al.release_date) AS INTEGER)
		FROM albums al
		LEFT JOIN artists ar ON ar.artist_metadata_id = al.artist_metadata_id
		WHERE al.id = ?
	`, albumID).Scan(&artistMetadataID, &artistID, &title, &year)
	if err != nil {
		return "", fmt.Errorf("resolve album path %d: %w", albumID, err)
	}

	config, err := GetNamingConfig(ctx, q, "music")
	if err != nil {
		return "", err
	}
	opts, err := pathOptions(ctx, q)
	if err != nil {
		return "", err
	}
	albumTokens := map[string]string{"Album Title": title}
	if year.Valid {
		albumTokens["Release Year"] = strconv.FormatInt(year.Int64, 10)
	}
	albumSegments := pathbuilder.ResolveTemplatePath(config.AlbumFolderFormat.String, albumTokens, opts)

	isVA, err := isVariousArtists(ctx, q, artistMetadataID)
	if err != nil {
		return "", err
	}

	if !isVA {
		if !artistID.Valid {
			return "", fmt.Errorf("resolve album path %d: artist %d is not a tracked artist (no artists row)", albumID, artistMetadataID)
		}
		artistFolder, err := ArtistFolderPath(ctx, q, artistID.Int64)
		if err != nil {
			return "", err
		}
		return pathbuilder.JoinSegments(append([]string{artistFolder}, albumSegments...)...), nil
	}

	// Various Artists: needs its own root, not the (possibly untracked)
	// VA "artist"'s root folder - fall back to any music root folder.
	root, err := getAnyRootFolder(ctx, q, "music")
	if err != nil {
		return "", err
	}

	seriesName, found, err := findCompilationSeriesName(ctx, q, albumID)
	if err != nil {
		return "", err
	}
	if found {
		seriesSegments := pathbuilder.ResolveTemplatePath(config.VASeriesFolderFormat.String, map[string]string{"Series Name": seriesName}, opts)
		return pathbuilder.JoinSegments(append(append([]string{root}, seriesSegments...), albumSegments...)...), nil
	}

	// No series link: literal fallback, not template-driven - the seeded
	// va_series_folder_format has no defined behavior for a missing
	// {Series Name}, see the plan.
	return pathbuilder.JoinSegments(append([]string{root, "Various Artists"}, albumSegments...)...), nil
}

func isVariousArtists(ctx context.Context, q Queryer, artistMetadataID int64) (bool, error) {
	mbid, found, err := GetExternalID(ctx, q, "artist", artistMetadataID, "musicbrainz")
	if err != nil {
		return false, err
	}
	return found && mbid == variousArtistsMBID, nil
}

func findCompilationSeriesName(ctx context.Context, q Queryer, albumID int64) (name string, found bool, err error) {
	row := q.QueryRowContext(ctx, `
		SELECT cs.name FROM compilation_series_albums csa
		JOIN compilation_series cs ON cs.id = csa.compilation_series_id
		WHERE csa.album_id = ?
	`, albumID)
	if err := row.Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("find compilation series for album %d: %w", albumID, err)
	}
	return name, true, nil
}

func getAnyRootFolder(ctx context.Context, q Queryer, mediaType string) (string, error) {
	var path string
	err := q.QueryRowContext(ctx, `SELECT path FROM root_folders WHERE media_type = ? ORDER BY id LIMIT 1`, mediaType).Scan(&path)
	if err != nil {
		return "", fmt.Errorf("get any %s root folder: %w", mediaType, err)
	}
	return path, nil
}

// UpdateMovieNamingConfig saves the movie folder/file naming templates -
// takes effect immediately, since GetNamingConfig is re-read from the DB
// on every path/filename resolution rather than cached anywhere.
func UpdateMovieNamingConfig(ctx context.Context, q Queryer, folderFormat, fileFormat string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE naming_config SET movie_folder_format = ?, movie_file_format = ? WHERE media_type = 'movie'
	`, folderFormat, fileFormat)
	if err != nil {
		return fmt.Errorf("update movie naming config: %w", err)
	}
	return nil
}

// UpdateSeriesNamingConfig saves the series/season/episode naming templates.
func UpdateSeriesNamingConfig(ctx context.Context, q Queryer, seriesFolderFormat, seasonFolderFormat, episodeFileFormat string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE naming_config SET series_folder_format = ?, season_folder_format = ?, episode_file_format = ?
		WHERE media_type = 'series'
	`, seriesFolderFormat, seasonFolderFormat, episodeFileFormat)
	if err != nil {
		return fmt.Errorf("update series naming config: %w", err)
	}
	return nil
}

// UpdateRenameFiles turns renaming on or off for one media type
// ("movie"/"series"/"music").
func UpdateRenameFiles(ctx context.Context, q Queryer, mediaType string, rename bool) error {
	if _, err := q.ExecContext(ctx, `UPDATE naming_config SET rename_files = ? WHERE media_type = ?`, rename, mediaType); err != nil {
		return fmt.Errorf("update rename files for %s: %w", mediaType, err)
	}
	return nil
}

// UpdateMusicNamingConfig saves the artist/album/VA-series/track naming templates.
func UpdateMusicNamingConfig(ctx context.Context, q Queryer, artistFolderFormat, albumFolderFormat, vaSeriesFolderFormat, trackFileFormat string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE naming_config SET artist_folder_format = ?, album_folder_format = ?,
		       va_series_folder_format = ?, track_file_format = ?
		WHERE media_type = 'music'
	`, artistFolderFormat, albumFolderFormat, vaSeriesFolderFormat, trackFileFormat)
	if err != nil {
		return fmt.Errorf("update music naming config: %w", err)
	}
	return nil
}
