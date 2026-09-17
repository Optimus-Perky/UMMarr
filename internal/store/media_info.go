package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
)

// MediaFile is a library file to analyze.
type MediaFile struct {
	Kind string // movie, episode or track
	ID   int64
	Path string // absolute, as UMMarr sees it
}

var mediaFileTables = map[string]string{"movie": "movie_files", "episode": "episode_files", "track": "track_files"}

// validMediaInfo reads a media_info column as JSON even when it holds
// something that isn't (json_extract would fail on it).
func validMediaInfo(column string) string {
	return fmt.Sprintf(`(CASE WHEN json_valid(%[1]s) THEN %[1]s ELSE '{}' END)`, column)
}

// needsAnalysis is true for a file never analyzed, or analyzed by an older
// UMMarr. A file that failed is not retried until it's reset.
func needsAnalysis(column string) string {
	return fmt.Sprintf(`COALESCE(json_extract(%s, '$.schema'), 0) < %d`, validMediaInfo(column), mediainfo.Schema)
}

// filePath joins a folder and a file's stored relative path; a stored
// absolute path is used as it is.
func filePath(folder, relative string) string {
	return fmt.Sprintf(`CASE WHEN %[2]s LIKE '/%%' THEN %[2]s ELSE %[1]s || '/' || %[2]s END`, folder, relative)
}

const (
	movieFilesSQL   = `FROM movie_files mf JOIN movies m ON m.id = mf.movie_id WHERE m.path IS NOT NULL`
	episodeFilesSQL = `FROM episode_files ef JOIN episodes e ON e.id = ef.episode_id JOIN series s ON s.id = e.series_id WHERE s.path IS NOT NULL`
	trackFilesSQL   = `FROM track_files tf JOIN tracks t ON t.track_file_id = tf.id JOIN album_releases ar ON ar.id = t.album_release_id JOIN albums al ON al.id = ar.album_id WHERE al.path IS NOT NULL`
)

// ListFilesToAnalyze lists up to limit files waiting for analysis (limit 0
// lists them all): movie and episode files when video is set, track files
// when audio is.
func ListFilesToAnalyze(ctx context.Context, q Queryer, video, audio bool, limit int) ([]MediaFile, error) {
	var parts []string
	if video {
		parts = append(parts,
			`SELECT 'movie', mf.id, `+filePath("m.path", "mf.relative_path")+` `+movieFilesSQL+` AND `+needsAnalysis("mf.media_info"),
			`SELECT 'episode', ef.id, `+filePath("s.path", "ef.relative_path")+` `+episodeFilesSQL+` AND `+needsAnalysis("ef.media_info"))
	}
	if audio {
		parts = append(parts, `SELECT 'track', tf.id, MIN(`+filePath("al.path", "tf.relative_path")+`) `+trackFilesSQL+` AND `+needsAnalysis("tf.media_info")+` GROUP BY tf.id`)
	}
	if len(parts) == 0 {
		return nil, nil
	}
	query := strings.Join(parts, " UNION ALL ")
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list files to analyze: %w", err)
	}
	defer rows.Close()
	var files []MediaFile
	for rows.Next() {
		var f MediaFile
		if err := rows.Scan(&f.Kind, &f.ID, &f.Path); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// ListMediaFilePaths is every library file's absolute path.
func ListMediaFilePaths(ctx context.Context, q Queryer) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT `+filePath("m.path", "mf.relative_path")+` `+movieFilesSQL+`
		UNION SELECT `+filePath("s.path", "ef.relative_path")+` `+episodeFilesSQL+`
		UNION SELECT `+filePath("al.path", "tf.relative_path")+` `+trackFilesSQL)
	if err != nil {
		return nil, fmt.Errorf("list media file paths: %w", err)
	}
	defer rows.Close()
	paths := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths[filepath.Clean(p)] = true
	}
	return paths, rows.Err()
}

// SaveMediaInfo stores a file's media information. For a movie or episode
// file whose name didn't say its resolution or video codec, the analysis
// fills them in, as Sonarr does.
func SaveMediaInfo(ctx context.Context, q Queryer, kind string, id int64, info mediainfo.Info) error {
	table, ok := mediaFileTables[kind]
	if !ok {
		return fmt.Errorf("unknown media file kind %q", kind)
	}
	if _, err := q.ExecContext(ctx, `UPDATE `+table+` SET media_info = ? WHERE id = ?`, info.Encode(), id); err != nil {
		return fmt.Errorf("save media info for %s file %d: %w", kind, id, err)
	}
	if kind == "track" || !info.Analyzed() || info.Resolution() == "" {
		return nil
	}
	var raw string
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(quality, '{}') FROM `+table+` WHERE id = ?`, id).Scan(&raw); err != nil {
		return nil
	}
	fq := unmarshalFileQuality(raw)
	changed := false
	if fq.Resolution == "" {
		fq.Resolution, changed = info.Resolution(), true
	}
	if fq.Codec == "" && info.VideoCodec != "" {
		fq.Codec, changed = info.VideoCodec, true
	}
	if !changed {
		return nil
	}
	data, _ := json.Marshal(fq)
	_, err := q.ExecContext(ctx, `UPDATE `+table+` SET quality = ? WHERE id = ?`, string(data), id)
	return err
}

// ResetMediaInfo marks the files of a movie, series or album for analysis
// again - Refresh & Scan does this. Returns how many files were reset.
func ResetMediaInfo(ctx context.Context, q Queryer, owner string, id int64) (int64, error) {
	var query string
	switch owner {
	case "movie":
		query = `UPDATE movie_files SET media_info = '{}' WHERE movie_id = ?`
	case "series":
		query = `UPDATE episode_files SET media_info = '{}' WHERE episode_id IN (SELECT id FROM episodes WHERE series_id = ?)`
	case "album":
		query = `UPDATE track_files SET media_info = '{}' WHERE id IN (SELECT t.track_file_id FROM tracks t JOIN album_releases ar ON ar.id = t.album_release_id WHERE ar.album_id = ?)`
	default:
		return 0, fmt.Errorf("unknown media owner %q", owner)
	}
	res, err := q.ExecContext(ctx, query, id)
	if err != nil {
		return 0, fmt.Errorf("reset media info for %s %d: %w", owner, id, err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// MediaInfoCounts is how far analysis has got across the library.
type MediaInfoCounts struct {
	Total, Analyzed, Failed, FromPlex int
}

// CountMediaInfo counts analyzed, failed and Plex-sourced files.
func CountMediaInfo(ctx context.Context, q Queryer) (MediaInfoCounts, error) {
	var c MediaInfoCounts
	mi := validMediaInfo("media_info")
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*),
		       COALESCE(SUM(COALESCE(json_extract(mi, '$.schema'), 0) >= 1 AND json_extract(mi, '$.error') IS NULL), 0),
		       COALESCE(SUM(json_extract(mi, '$.error') IS NOT NULL), 0),
		       COALESCE(SUM(json_extract(mi, '$.source') = 'plex' AND json_extract(mi, '$.error') IS NULL), 0)
		FROM (SELECT `+mi+` AS mi FROM movie_files UNION ALL SELECT `+mi+` FROM episode_files UNION ALL SELECT `+mi+` FROM track_files)`).
		Scan(&c.Total, &c.Analyzed, &c.Failed, &c.FromPlex)
	if err != nil {
		return c, fmt.Errorf("count media info: %w", err)
	}
	return c, nil
}

// PlexConnection is the server URL and token of the Plex connection under
// Settings → Connect, an enabled one first. found is false when there is
// none with both set.
func PlexConnection(ctx context.Context, q Queryer) (baseURL, token string, found bool, err error) {
	list, err := ListNotifications(ctx, q)
	if err != nil {
		return "", "", false, err
	}
	for _, wantEnabled := range []bool{true, false} {
		for _, n := range list {
			if n.Implementation != NotifyPlex || n.Enabled != wantEnabled {
				continue
			}
			if u, t := strings.TrimSpace(n.Settings["server_url"]), n.Settings["token"]; u != "" && t != "" {
				return u, t, true, nil
			}
		}
	}
	return "", "", false, nil
}

// MediaInfoTokens, when set, reads a file for the {MediaInfo ...} naming
// tokens. It is only called for templates that use one.
var MediaInfoTokens func(ctx context.Context, path string) map[string]string

func addMediaInfoTokens(ctx context.Context, tokens map[string]string, template, sourcePath string) {
	if MediaInfoTokens == nil || !strings.Contains(template, "{MediaInfo") {
		return
	}
	for k, v := range MediaInfoTokens(ctx, sourcePath) {
		tokens[k] = v
	}
}
