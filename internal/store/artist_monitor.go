package store

import (
	"context"
	"fmt"
)

// Artist monitoring, as Lidarr's "Monitor" dropdown does it for albums -
// the music answer to Sonarr's Series Monitoring (see MonitorOptions in
// series_edit.go), which the Music mass editor and the artist page both
// apply.

// AlbumMonitorOptions are the choices offered for an artist's albums.
var AlbumMonitorOptions = []MonitorOption{
	{"all", "All Albums", "Monitor every album by this artist"},
	{"future", "Future Albums", "Monitor albums that have not been released yet"},
	{"missing", "Missing Albums", "Monitor albums with no files, and albums not released yet"},
	{"existing", "Existing Albums", "Monitor albums that have files, and albums not released yet"},
	{"latest", "Latest Album", "Monitor only the most recently released album"},
	{"first", "First Album", "Monitor only the earliest album"},
	{"none", "None", "No albums will be monitored"},
}

// ValidAlbumMonitorOption reports whether option names one of them.
func ValidAlbumMonitorOption(option string) bool {
	for _, o := range AlbumMonitorOptions {
		if o.Value == option {
			return true
		}
	}
	return false
}

// hasTrackFiles is true for an album with at least one imported track.
const hasTrackFiles = `EXISTS (SELECT 1 FROM album_releases r JOIN tracks t ON t.album_release_id = r.id
                               WHERE r.album_id = albums.id AND t.track_file_id IS NOT NULL)`

// albumReleased is true once the release date has passed. An album with no
// date at all counts as unreleased: MusicBrainz leaves the date off
// announced-but-unissued records, and those are exactly the ones a Future
// or Missing choice is meant to keep watching.
const albumReleased = `(albums.release_date IS NOT NULL AND albums.release_date <= CURRENT_TIMESTAMP)`

// albumMonitorCondition is the SQL deciding which of an artist's albums end
// up monitored, plus how many times it needs the artist_metadata_id.
func albumMonitorCondition(option string) (sql string, ids int) {
	switch option {
	case "all":
		return "1", 0
	case "future":
		return "NOT " + albumReleased, 0
	case "missing":
		return "NOT " + hasTrackFiles, 0
	case "existing":
		return "(" + hasTrackFiles + " OR NOT " + albumReleased + ")", 0
	case "latest":
		return `albums.id = (SELECT id FROM albums WHERE artist_metadata_id = ?
		                     ORDER BY release_date IS NULL, release_date DESC, id DESC LIMIT 1)`, 1
	case "first":
		return `albums.id = (SELECT id FROM albums WHERE artist_metadata_id = ?
		                     ORDER BY release_date IS NULL, release_date ASC, id ASC LIMIT 1)`, 1
	}
	return "0", 0 // none
}

// ApplyAlbumMonitorOption sets every album of artistID to monitored or not,
// the way Lidarr's Monitor dropdown does. The artist's own monitored flag is
// left alone - that is a separate switch, as in the arr apps.
func ApplyAlbumMonitorOption(ctx context.Context, q Queryer, artistID int64, option string) error {
	if !ValidAlbumMonitorOption(option) {
		return fmt.Errorf("unknown monitor option %q", option)
	}
	var metadataID int64
	if err := q.QueryRowContext(ctx, `SELECT artist_metadata_id FROM artists WHERE id = ?`, artistID).Scan(&metadataID); err != nil {
		return fmt.Errorf("find artist %d: %w", artistID, err)
	}
	condition, ids := albumMonitorCondition(option)
	args := make([]any, 0, ids+1)
	for i := 0; i < ids; i++ {
		args = append(args, metadataID) // the condition's own subquery
	}
	args = append(args, metadataID) // the WHERE
	query := `UPDATE albums SET monitored = CASE WHEN ` + condition + ` THEN 1 ELSE 0 END
	          WHERE artist_metadata_id = ?`
	if _, err := q.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("apply monitor option %q to artist %d: %w", option, artistID, err)
	}
	return nil
}
