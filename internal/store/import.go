package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
)

// ErrNoAlbumRelease is returned by FindImportRelease when albumID has no
// album_releases row at all to import against.
var ErrNoAlbumRelease = errors.New("store: album has no album_releases row to import against")

// addFileQualityTokens fills {Quality Title}, {Video Codec}, {Release Group}
// and {Custom Formats} from the source file's own name - a renamed library
// file usually no longer carries them.

func addFileQualityTokens(ctx context.Context, q Queryer, tokens map[string]string, sourcePath string) {
	quality := releaseparse.Parse(filepath.Base(sourcePath))
	tokens["Quality Title"] = quality.String()
	tokens["Video Codec"] = quality.Codec
	tokens["Release Group"] = quality.ReleaseGroup
	tokens["Custom Formats"] = CustomFormatNames(ctx, q, filepath.Base(sourcePath))
}

// ResolveMovieFileName computes the file name (no directory) the file at
// sourcePath - a download or a file already in the library - should have: the
// movie file template filled in, keeping sourcePath's extension. With renaming
// turned off for movies it's sourcePath's own name.
func ResolveMovieFileName(ctx context.Context, q Queryer, movieID int64, sourcePath string) (string, error) {
	var title string
	var year sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT mm.title, mm.year
		FROM movies m JOIN movie_metadata mm ON mm.id = m.movie_metadata_id
		WHERE m.id = ?
	`, movieID).Scan(&title, &year)
	if err != nil {
		return "", fmt.Errorf("resolve movie file name %d: %w", movieID, err)
	}
	config, err := GetNamingConfig(ctx, q, "movie")
	if err != nil {
		return "", err
	}
	if !config.RenameFiles {
		return filepath.Base(sourcePath), nil
	}
	tokens := map[string]string{"Movie Title": title}
	if year.Valid {
		tokens["Release Year"] = strconv.FormatInt(year.Int64, 10)
	}
	addFileQualityTokens(ctx, q, tokens, sourcePath)
	addMediaInfoTokens(ctx, tokens, config.MovieFileFormat.String, sourcePath)
	name, err := resolveFileName(ctx, q, config.MovieFileFormat.String, tokens)
	if err != nil {
		return "", err
	}
	return name + filepath.Ext(sourcePath), nil
}

// ResolveEpisodeFileName is ResolveMovieFileName for an episode.
// Tokens: "Series Title", "season"/"episode" (use {season:00}/{episode:00}
// for zero-padding), "Episode Title", plus the file quality tokens.

// ResolveTrackFileName is ResolveMovieFileName for a track.
// Tokens: "Artist Name", "Album Title", "track"/"medium" (numeric
// zero-padding via {track:00} only applies when track_number happens to
// be numeric - migration 00007 stores it as TEXT since MusicBrainz track
// numbers can be non-numeric, e.g. "A1"; pathbuilder.ResolveTemplate
// already falls back to the raw value when padding doesn't apply), "Track
// Title".
func ResolveTrackFileName(ctx context.Context, q Queryer, trackID int64, sourcePath string) (string, error) {
	var artistName, albumTitle, trackNumber, trackTitle string
	var mediumNumber int
	err := q.QueryRowContext(ctx, `
		SELECT am.name, al.title, t.track_number, t.medium_number, t.title
		FROM tracks t
		JOIN artist_metadata am ON am.id = t.artist_metadata_id
		JOIN album_releases ar ON ar.id = t.album_release_id
		JOIN albums al ON al.id = ar.album_id
		WHERE t.id = ?
	`, trackID).Scan(&artistName, &albumTitle, &trackNumber, &mediumNumber, &trackTitle)
	if err != nil {
		return "", fmt.Errorf("resolve track file name %d: %w", trackID, err)
	}
	config, err := GetNamingConfig(ctx, q, "music")
	if err != nil {
		return "", err
	}
	if !config.RenameFiles {
		return filepath.Base(sourcePath), nil
	}
	tokens := map[string]string{
		"Artist Name": artistName, "Album Title": albumTitle,
		"track": trackNumber, "medium": strconv.Itoa(mediumNumber), "Track Title": trackTitle,
	}
	addMediaInfoTokens(ctx, tokens, config.TrackFileFormat.String, sourcePath)
	name, err := resolveFileName(ctx, q, config.TrackFileFormat.String, tokens)
	if err != nil {
		return "", err
	}
	return name + filepath.Ext(sourcePath), nil
}

// FindEpisode looks up an episode by its natural key
// (series_id, season_number, episode_number) - migration 00005's
// UNIQUE(series_id, season_number, episode_number) backs this.
func FindEpisode(ctx context.Context, q Queryer, seriesID int64, seasonNumber, episodeNumber int) (episodeID int64, found bool, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT id FROM episodes WHERE series_id = ? AND season_number = ? AND episode_number = ?
	`, seriesID, seasonNumber, episodeNumber).Scan(&episodeID)
	if err == nil {
		return episodeID, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return 0, false, fmt.Errorf("find episode (series %d s%02de%02d): %w", seriesID, seasonNumber, episodeNumber, err)
}

// FindEpisodeMissingFile is FindEpisode narrowed to episodes that don't
// already have a file - so a library scan re-run doesn't reprocess/
// re-copy episodes it (or a grab) already imported.
func FindEpisodeMissingFile(ctx context.Context, q Queryer, seriesID int64, seasonNumber, episodeNumber int) (episodeID int64, found bool, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT id FROM episodes
		WHERE series_id = ? AND season_number = ? AND episode_number = ? AND episode_file_id IS NULL
	`, seriesID, seasonNumber, episodeNumber).Scan(&episodeID)
	if err == nil {
		return episodeID, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return 0, false, fmt.Errorf("find episode missing file (series %d s%02de%02d): %w", seriesID, seasonNumber, episodeNumber, err)
}

// InsertMovieFile records a newly imported file for movieID. relativePath
// is relative to the movie's own folder (movies.path) - just the filename
// in every case this pass produces, since no movie subfolder structure is
// introduced. No upsert: movie_files has no natural-key/UNIQUE constraint
// (migration 00004), and an Import call only ever inserts once.
func InsertMovieFile(ctx context.Context, q Queryer, movieID int64, relativePath string, size int64) (int64, error) {
	res, err := q.ExecContext(ctx, `
		INSERT INTO movie_files (movie_id, relative_path, size) VALUES (?, ?, ?)
	`, movieID, relativePath, size)
	if err != nil {
		return 0, fmt.Errorf("insert movie_file for movie %d: %w", movieID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted movie_file id: %w", err)
	}
	return id, nil
}

// AttachEpisodeFile records a newly imported file for episodeID and
// points episodes.episode_file_id at it - two statements, callers must
// wrap this in store.WithTx. relativePath is relative to the SERIES'
// folder (series.path), e.g. "Season 01/Show - S01E01 - Title.mkv" - not
// relative to the season subfolder, since series.path is the only path
// column episode_files has any relationship to.
func AttachEpisodeFile(ctx context.Context, q Queryer, episodeID int64, relativePath string, size int64) (fileID int64, err error) {
	// An episode has one file. Re-importing it (a repack, or the same pack
	// imported twice) used to insert a second row and just repoint the
	// episode at it, leaving the old row behind with nothing referring to
	// it - counted in the library's file totals forever.
	if _, err := q.ExecContext(ctx, `DELETE FROM episode_files WHERE episode_id = ?`, episodeID); err != nil {
		return 0, fmt.Errorf("replace episode_files for episode %d: %w", episodeID, err)
	}
	res, err := q.ExecContext(ctx, `
		INSERT INTO episode_files (episode_id, relative_path, size) VALUES (?, ?, ?)
	`, episodeID, relativePath, size)
	if err != nil {
		return 0, fmt.Errorf("insert episode_file for episode %d: %w", episodeID, err)
	}
	fileID, err = res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted episode_file id: %w", err)
	}
	if _, err := q.ExecContext(ctx, `UPDATE episodes SET episode_file_id = ? WHERE id = ?`, fileID, episodeID); err != nil {
		return 0, fmt.Errorf("attach episode_file %d to episode %d: %w", fileID, episodeID, err)
	}
	return fileID, nil
}

// AttachTrackFile records a newly imported file for trackID and points
// tracks.track_file_id at it. relativePath is relative to the album's own
// folder (albums.path) - just the filename, mirroring InsertMovieFile,
// since track_files carries no album/artist FK of its own to make a
// deeper relative path reconstructible later.
func AttachTrackFile(ctx context.Context, q Queryer, trackID int64, relativePath string, size int64) (fileID int64, err error) {
	res, err := q.ExecContext(ctx, `
		INSERT INTO track_files (relative_path, size) VALUES (?, ?)
	`, relativePath, size)
	if err != nil {
		return 0, fmt.Errorf("insert track_file for track %d: %w", trackID, err)
	}
	fileID, err = res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted track_file id: %w", err)
	}
	if _, err := q.ExecContext(ctx, `UPDATE tracks SET track_file_id = ? WHERE id = ?`, fileID, trackID); err != nil {
		return 0, fmt.Errorf("attach track_file %d to track %d: %w", fileID, trackID, err)
	}
	return fileID, nil
}

// UpdateMovieFileQuality records the parsed quality/release-group for an
// already-inserted movie_files row (see internal/releaseparse.Parse) -
// called best-effort right after InsertMovieFile, not merged into it, so
// InsertMovieFile's existing call sites (including in tests) don't need
// to change - quality is cosmetic metadata, a failure to store it must
// never fail an otherwise-successful import.
func UpdateMovieFileQuality(ctx context.Context, q Queryer, movieFileID int64, quality releaseparse.FileQuality) error {
	j, err := marshalJSON(quality, "{}")
	if err != nil {
		return fmt.Errorf("marshal movie file quality: %w", err)
	}
	if _, err := q.ExecContext(ctx, `UPDATE movie_files SET quality = ? WHERE id = ?`, j, movieFileID); err != nil {
		return fmt.Errorf("update movie_file %d quality: %w", movieFileID, err)
	}
	return nil
}

// UpdateEpisodeFileQuality is UpdateMovieFileQuality's episode_files
// equivalent - see AttachEpisodeFile's returned fileID.
func UpdateEpisodeFileQuality(ctx context.Context, q Queryer, episodeFileID int64, quality releaseparse.FileQuality) error {
	j, err := marshalJSON(quality, "{}")
	if err != nil {
		return fmt.Errorf("marshal episode file quality: %w", err)
	}
	if _, err := q.ExecContext(ctx, `UPDATE episode_files SET quality = ? WHERE id = ?`, j, episodeFileID); err != nil {
		return fmt.Errorf("update episode_file %d quality: %w", episodeFileID, err)
	}
	return nil
}

// UpdateTrackFileQuality is UpdateMovieFileQuality's track_files
// equivalent - see AttachTrackFile's returned fileID.
func UpdateTrackFileQuality(ctx context.Context, q Queryer, trackFileID int64, quality releaseparse.FileQuality) error {
	j, err := marshalJSON(quality, "{}")
	if err != nil {
		return fmt.Errorf("marshal track file quality: %w", err)
	}
	if _, err := q.ExecContext(ctx, `UPDATE track_files SET quality = ? WHERE id = ?`, j, trackFileID); err != nil {
		return fmt.Errorf("update track_file %d quality: %w", trackFileID, err)
	}
	return nil
}

// TrackImportInfo is one track queued for import, in play order.
type TrackImportInfo struct {
	ID      int64
	Title   string
	HasFile bool // track_file_id IS NOT NULL - used by ScanMusicLibrary to skip partially-imported releases
	// MBID, Medium and Number are what a tagged file is matched against:
	// its MUSICBRAINZ_RELEASETRACKID, or failing that the disc and track
	// number it claims. Empty MBID means this track was synced before
	// UMMarr stored them - a metadata refresh fills it in.
	MBID   string
	Medium int
	Number string
}

// FindImportRelease resolves which album_releases row to import
// trackFiles against for albumID, and returns its tracks ordered by
// (medium_number, track_number).
//
// Ambiguity being resolved: migration 00006 allows multiple
// album_releases rows per album (distinct pressings/editions), each
// independently "monitored", with no schema-level "one canonical
// release" flag. This picks, in order: (1) a monitored release,
// tie-broken by whichever has the most known tracks (proxy for "most
// complete/likely intended edition"); (2) if none are monitored, any
// release for the album at all, same tie-break; ties beyond that broken
// by lowest id for determinism.
//
// track_number sorts as TEXT (migration 00007 - MusicBrainz numbers can
// be non-numeric, e.g. "A1"), so this is a lexicographic, not numeric,
// sort. Acceptable because the result only feeds a POSITIONAL match
// against naturally-path-sorted audio files (see importAlbum in
// internal/sync/import.go), not an exact numeric join.
func FindImportRelease(ctx context.Context, q Queryer, albumID int64) (releaseID int64, tracks []TrackImportInfo, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT ar.id
		FROM album_releases ar
		WHERE ar.album_id = ?
		ORDER BY ar.monitored DESC,
		         (SELECT COUNT(*) FROM tracks t WHERE t.album_release_id = ar.id) DESC,
		         ar.id ASC
		LIMIT 1
	`, albumID).Scan(&releaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, fmt.Errorf("find import release for album %d: %w", albumID, ErrNoAlbumRelease)
	}
	if err != nil {
		return 0, nil, fmt.Errorf("find import release for album %d: %w", albumID, err)
	}

	rows, err := q.QueryContext(ctx, `
		SELECT t.id, t.title, t.track_file_id IS NOT NULL, t.medium_number, t.track_number,
		       COALESCE(t.musicbrainz_id, '')
		FROM tracks t
		WHERE t.album_release_id = ?
		ORDER BY t.medium_number ASC, t.track_number ASC
`, releaseID)
	if err != nil {
		return 0, nil, fmt.Errorf("list tracks for release %d: %w", releaseID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var ti TrackImportInfo
		if err := rows.Scan(&ti.ID, &ti.Title, &ti.HasFile, &ti.Medium, &ti.Number, &ti.MBID); err != nil {
			return 0, nil, fmt.Errorf("scan track import info: %w", err)
		}
		tracks = append(tracks, ti)
	}
	return releaseID, tracks, rows.Err()
}

// FileRef identifies one *_files row and where its file is expected on
// disk, relative to the movie/series/album folder that owns it.
type FileRef struct {
	ID           int64
	RelativePath string
	// OwnerID is the movie, episode or track the file belongs to.
	OwnerID int64
}

func scanFileRefs(rows *sql.Rows, what string) ([]FileRef, error) {
	defer rows.Close()
	var refs []FileRef
	for rows.Next() {
		var ref FileRef
		if err := rows.Scan(&ref.ID, &ref.RelativePath, &ref.OwnerID); err != nil {
			return nil, fmt.Errorf("scan %s: %w", what, err)
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

// ListMovieFilesForMovie returns every movie_files row recorded for
// movieID, for checking whether those files are still on disk.
func ListMovieFilesForMovie(ctx context.Context, q Queryer, movieID int64) ([]FileRef, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, relative_path, movie_id FROM movie_files WHERE movie_id = ?`, movieID)
	if err != nil {
		return nil, fmt.Errorf("list movie files for movie %d: %w", movieID, err)
	}
	return scanFileRefs(rows, "movie file ref")
}

// ListEpisodeFilesForSeries returns every episode_files row belonging to
// seriesID, with paths relative to the series folder.
func ListEpisodeFilesForSeries(ctx context.Context, q Queryer, seriesID int64) ([]FileRef, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT ef.id, ef.relative_path, ef.episode_id
		FROM episode_files ef
		JOIN episodes e ON e.id = ef.episode_id
		WHERE e.series_id = ?
	`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("list episode files for series %d: %w", seriesID, err)
	}
	return scanFileRefs(rows, "episode file ref")
}

// ListTrackFilesForAlbum returns every track_files row belonging to any
// release of albumID, with paths relative to the album folder. Unlike
// movie_files/episode_files, track_files has no column pointing back at
// its owner - the only link is tracks.track_file_id - so the join has to
// start from tracks.
func ListTrackFilesForAlbum(ctx context.Context, q Queryer, albumID int64) ([]FileRef, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT tf.id, tf.relative_path, t.id
		FROM tracks t
		JOIN track_files tf ON tf.id = t.track_file_id
		JOIN album_releases ar ON ar.id = t.album_release_id
		WHERE ar.album_id = ?
	`, albumID)
	if err != nil {
		return nil, fmt.Errorf("list track files for album %d: %w", albumID, err)
	}
	return scanFileRefs(rows, "track file ref")
}

// DeleteMovieFile removes one movie_files row. Deleting the row is what
// makes the movie read as Missing again - movies has no forward pointer
// column of its own (unlike episodes/tracks, whose pointers are cleared
// by the triggers in migration 00016).
func DeleteMovieFile(ctx context.Context, q Queryer, fileID int64) error {
	_, err := q.ExecContext(ctx, `DELETE FROM movie_files WHERE id = ?`, fileID)
	return err
}

func DeleteEpisodeFile(ctx context.Context, q Queryer, fileID int64) error {
	_, err := q.ExecContext(ctx, `DELETE FROM episode_files WHERE id = ?`, fileID)
	return err
}

func DeleteTrackFile(ctx context.Context, q Queryer, fileID int64) error {
	_, err := q.ExecContext(ctx, `DELETE FROM track_files WHERE id = ?`, fileID)
	return err
}

func fileQuality(ctx context.Context, q Queryer, table string, fileID int64) (releaseparse.FileQuality, error) {
	var raw string
	if err := q.QueryRowContext(ctx, `SELECT quality FROM `+table+` WHERE id = ?`, fileID).Scan(&raw); err != nil {
		return releaseparse.FileQuality{}, fmt.Errorf("get %s %d quality: %w", table, fileID, err)
	}
	var quality releaseparse.FileQuality
	_ = json.Unmarshal([]byte(raw), &quality)
	return quality, nil
}

// MovieFileQuality reads a movie file's recorded quality.
func MovieFileQuality(ctx context.Context, q Queryer, fileID int64) (releaseparse.FileQuality, error) {
	return fileQuality(ctx, q, "movie_files", fileID)
}

// EpisodeFileQuality reads an episode file's recorded quality.
func EpisodeFileQuality(ctx context.Context, q Queryer, fileID int64) (releaseparse.FileQuality, error) {
	return fileQuality(ctx, q, "episode_files", fileID)
}

// TrackFileQuality reads a track file's recorded quality.
func TrackFileQuality(ctx context.Context, q Queryer, fileID int64) (releaseparse.FileQuality, error) {
	return fileQuality(ctx, q, "track_files", fileID)
}

// CurrentEpisodeFile is the file an episode currently has, if any.
func CurrentEpisodeFile(ctx context.Context, q Queryer, episodeID int64) (FileRef, bool, error) {
	var ref FileRef
	err := q.QueryRowContext(ctx, `
		SELECT ef.id, ef.relative_path, e.id FROM episodes e JOIN episode_files ef ON ef.id = e.episode_file_id WHERE e.id = ?`, episodeID).
		Scan(&ref.ID, &ref.RelativePath, &ref.OwnerID)
	if errors.Is(err, sql.ErrNoRows) {
		return FileRef{}, false, nil
	}
	if err != nil {
		return FileRef{}, false, fmt.Errorf("episode %d file: %w", episodeID, err)
	}
	return ref, true, nil
}

// ResolveEpisodeFileName is ResolveEpisodesFileName for a single episode.
func ResolveEpisodeFileName(ctx context.Context, q Queryer, episodeID int64, sourcePath string) (string, error) {
	return ResolveEpisodesFileName(ctx, q, []int64{episodeID}, sourcePath)
}

// ResolveEpisodesFileName names a file holding one or more episodes of a
// series. Several episodes name as Sonarr's prefixed range - "S07E23-E24" -
// with their titles joined by " + " ("Hit (1) + Run (2)").
func ResolveEpisodesFileName(ctx context.Context, q Queryer, episodeIDs []int64, sourcePath string) (string, error) {
	if len(episodeIDs) == 0 {
		return "", fmt.Errorf("resolve episode file name: no episodes")
	}
	var seriesTitle string
	var seasonNumber int
	var numbers []int
	var titles []string
	for _, episodeID := range episodeIDs {
		var episodeNumber int
		var episodeTitle sql.NullString
		err := q.QueryRowContext(ctx, `
			SELECT sm.title, e.season_number, e.episode_number, e.title
			FROM episodes e
			JOIN series s ON s.id = e.series_id
			JOIN series_metadata sm ON sm.id = s.series_metadata_id
			WHERE e.id = ?
		`, episodeID).Scan(&seriesTitle, &seasonNumber, &episodeNumber, &episodeTitle)
		if err != nil {
			return "", fmt.Errorf("resolve episode file name %d: %w", episodeID, err)
		}
		numbers = append(numbers, episodeNumber)
		if t := strings.TrimSpace(episodeTitle.String); t != "" {
			titles = append(titles, t)
		}
	}
	config, err := GetNamingConfig(ctx, q, "series")
	if err != nil {
		return "", err
	}
	if !config.RenameFiles {
		return filepath.Base(sourcePath), nil
	}
	sort.Ints(numbers)
	episode := strconv.Itoa(numbers[0])
	if len(numbers) > 1 {
		// Not a number, so {episode:00} keeps it as written: "23-E24".
		episode = fmt.Sprintf("%02d-E%02d", numbers[0], numbers[len(numbers)-1])
	}
	tokens := map[string]string{
		"Series Title":  seriesTitle,
		"season":        strconv.Itoa(seasonNumber),
		"episode":       episode,
		"Episode Title": strings.Join(titles, " + "),
	}
	addFileQualityTokens(ctx, q, tokens, sourcePath)
	addMediaInfoTokens(ctx, tokens, config.EpisodeFileFormat.String, sourcePath)
	name, err := resolveFileName(ctx, q, config.EpisodeFileFormat.String, tokens)
	if err != nil {
		return "", err
	}
	return name + filepath.Ext(sourcePath), nil
}
