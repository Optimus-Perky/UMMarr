package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
)

// Manage Track Files, the music counterpart of episode_file_edit.go.
//
// It deliberately offers less than Manage Episodes. The quality catalog
// (WEBDL-1080p and friends) describes video, and audio files have nothing
// to say about it: in a real library every track file's recorded quality
// is empty, and the few release groups parsed out of audio filenames are
// fragments of track titles rather than groups. What a music file actually
// is - FLAC, 16-bit, 44.1kHz - comes from FFprobe, so the dialog shows
// that and doesn't pretend it's editable.
//
// What IS worth fixing by hand is which track a file sits on: albums match
// their files positionally (see FindImportRelease), so one missing track in
// a rip shifts every file after it onto the wrong title.

// TrackFileDetail is one track file as the dialog shows it.
type TrackFileDetail struct {
	ID           int64
	TrackID      int64
	TrackNumber  string
	TrackTitle   string
	RelativePath string
	Size         int64
	// Audio is FFprobe's summary ("FLAC, 16-bit, 44.1kHz"), empty until
	// the file has been analyzed.
	Audio string
	// Quality is the recorded catalog key, kept only so a file imported
	// from a tagged release still shows what it was grabbed as.
	Quality string
}

// ListTrackFileDetails lists every track file of an album, in track order.
func ListTrackFileDetails(ctx context.Context, q Queryer, albumID int64) ([]TrackFileDetail, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT tf.id, t.id, t.track_number, t.title, tf.relative_path, COALESCE(tf.size, 0),
		       COALESCE(tf.quality, '{}'), COALESCE(tf.media_info, '{}')
		FROM tracks t
		JOIN track_files tf ON tf.id = t.track_file_id
		JOIN album_releases ar ON ar.id = t.album_release_id
		WHERE ar.album_id = ?
		ORDER BY t.medium_number, CAST(t.track_number AS INTEGER), tf.id`, albumID)
	if err != nil {
		return nil, fmt.Errorf("list track files for album %d: %w", albumID, err)
	}
	defer rows.Close()
	var files []TrackFileDetail
	for rows.Next() {
		var f TrackFileDetail
		var quality, info string
		if err := rows.Scan(&f.ID, &f.TrackID, &f.TrackNumber, &f.TrackTitle, &f.RelativePath, &f.Size, &quality, &info); err != nil {
			return nil, err
		}
		var fq releaseparse.FileQuality
		_ = json.Unmarshal([]byte(quality), &fq)
		if key := fq.Key(); key != "Unknown" {
			f.Quality = key
		}
		f.Audio = mediainfo.Decode(info).TrackSummary()
		files = append(files, f)
	}
	return files, rows.Err()
}

// RemapTrackFile points a file at a different track of the same album -
// what to do when positional matching put a file on the wrong track. The
// track it currently sits on is left with no file, and any file already on
// the target track is detached first, so two tracks never share one file
// row.
func RemapTrackFile(ctx context.Context, q Queryer, albumID, fileID, trackID int64) error {
	var onAlbum int
	if err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM tracks t JOIN album_releases ar ON ar.id = t.album_release_id
		WHERE t.id = ? AND ar.album_id = ?`, trackID, albumID).Scan(&onAlbum); err != nil {
		return fmt.Errorf("check track %d: %w", trackID, err)
	}
	if onAlbum == 0 {
		return fmt.Errorf("track %d isn't on album %d", trackID, albumID)
	}
	if _, err := q.ExecContext(ctx, `UPDATE tracks SET track_file_id = NULL WHERE track_file_id = ?`, fileID); err != nil {
		return fmt.Errorf("detach file %d: %w", fileID, err)
	}
	if _, err := q.ExecContext(ctx, `UPDATE tracks SET track_file_id = ? WHERE id = ?`, fileID, trackID); err != nil {
		return fmt.Errorf("attach file %d to track %d: %w", fileID, trackID, err)
	}
	return nil
}
