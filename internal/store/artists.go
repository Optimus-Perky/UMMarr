package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
)

// ArtistDetail is one artist's page: the summary plus what the library has.
type ArtistDetail struct {
	ArtistSummary
	Overview           string
	Genres             []string
	Disambiguation     string
	QualityProfileName string
	AlbumCount         int
	AlbumsWithFiles    int
	TrackCount         int
	TracksWithFiles    int
	SizeOnDisk         int64
}

// GetArtistDetail reads one artist.
func GetArtistDetail(ctx context.Context, q Queryer, artistID int64) (ArtistDetail, bool, error) {
	var d ArtistDetail
	var genres, images sql.NullString
	err := q.QueryRowContext(ctx, `
		SELECT a.id, a.artist_metadata_id, a.root_folder_id, a.quality_profile_id, m.name, a.path, a.monitored, a.added,
		       COALESCE(m.overview, ''), COALESCE(m.genres, '[]'), COALESCE(m.disambiguation, ''),
		       CASE WHEN a.cover_path IS NOT NULL THEN json_array('/music/artists/' || a.id || '/cover') ELSE COALESCE(m.images, '[]') END,
		       COALESCE((SELECT p.name FROM quality_profiles p WHERE p.id = a.quality_profile_id), '')
		FROM artists a JOIN artist_metadata m ON m.id = a.artist_metadata_id WHERE a.id = ?`, artistID).
		Scan(&d.ID, &d.ArtistMetadataID, &d.RootFolderID, &d.QualityProfileID, &d.Name, &d.Path, &d.Monitored, &d.Added,
			&d.Overview, &genres, &d.Disambiguation, &images, &d.QualityProfileName)
	if err == sql.ErrNoRows {
		return d, false, nil
	}
	if err != nil {
		return d, false, fmt.Errorf("get artist %d: %w", artistID, err)
	}
	d.Genres = unmarshalStringSlice(genres.String)
	d.PosterURL = firstOrEmpty(unmarshalStringSlice(images.String))
	if url := PosterOverride(ctx, q, "artist", artistID); url != "" {
		d.PosterURL = url
	}
	err = q.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(has_file), 0) FROM (
			SELECT al.id, EXISTS (SELECT 1 FROM album_releases r JOIN tracks t ON t.album_release_id = r.id
			                      WHERE r.album_id = al.id AND t.track_file_id IS NOT NULL) AS has_file
			FROM albums al WHERE al.artist_metadata_id = ?)`, d.ArtistMetadataID).Scan(&d.AlbumCount, &d.AlbumsWithFiles)
	if err != nil {
		return d, false, fmt.Errorf("count albums for artist %d: %w", artistID, err)
	}
	err = q.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(t.track_file_id IS NOT NULL), 0), COALESCE(SUM(tf.size), 0)
		FROM albums al JOIN album_releases r ON r.album_id = al.id JOIN tracks t ON t.album_release_id = r.id
		LEFT JOIN track_files tf ON tf.id = t.track_file_id
		WHERE al.artist_metadata_id = ?`, d.ArtistMetadataID).Scan(&d.TrackCount, &d.TracksWithFiles, &d.SizeOnDisk)
	if err != nil {
		return d, false, fmt.Errorf("count tracks for artist %d: %w", artistID, err)
	}
	return d, true, nil
}

// ListAlbumsForArtist lists an artist's albums, newest first.
func ListAlbumsForArtist(ctx context.Context, q Queryer, artistMetadataID int64) ([]AlbumSummary, error) {
	return queryAlbumSummaries(ctx, q, albumSummaryQuery+` WHERE al.artist_metadata_id = ? ORDER BY al.release_date DESC, al.title`, artistMetadataID)
}

// UpdateArtistMonitored sets whether an artist is monitored.
func UpdateArtistMonitored(ctx context.Context, q Queryer, artistID int64, monitored bool) error {
	if _, err := q.ExecContext(ctx, `UPDATE artists SET monitored = ? WHERE id = ?`, monitored, artistID); err != nil {
		return fmt.Errorf("update artist %d monitored: %w", artistID, err)
	}
	return nil
}

// EditArtist saves the artist Edit dialog; nil fields are left alone.
type ArtistEdit struct {
	Monitored        *bool
	QualityProfileID *int64
	Path             *string
	// MonitorAlbums also sets every album's monitored flag to Monitored.
	MonitorAlbums bool
}

// EditArtists applies an edit to each artist.
func EditArtists(ctx context.Context, q Queryer, ids []int64, e ArtistEdit) error {
	for _, id := range ids {
		if e.Monitored != nil {
			if err := UpdateArtistMonitored(ctx, q, id, *e.Monitored); err != nil {
				return err
			}
			if e.MonitorAlbums {
				if _, err := q.ExecContext(ctx, `UPDATE albums SET monitored = ?
					WHERE artist_metadata_id = (SELECT artist_metadata_id FROM artists WHERE id = ?)`, *e.Monitored, id); err != nil {
					return fmt.Errorf("update albums monitored for artist %d: %w", id, err)
				}
			}
		}
		if e.QualityProfileID != nil {
			if _, err := q.ExecContext(ctx, `UPDATE artists SET quality_profile_id = ? WHERE id = ?`, *e.QualityProfileID, id); err != nil {
				return fmt.Errorf("update artist %d profile: %w", id, err)
			}
		}
		if e.Path != nil {
			if _, err := q.ExecContext(ctx, `UPDATE artists SET path = ? WHERE id = ?`, *e.Path, id); err != nil {
				return fmt.Errorf("update artist %d path: %w", id, err)
			}
		}
	}
	return nil
}

// DeleteArtist removes an artist and its albums, releases, tracks and file
// rows. Files on disk are the caller's business, as with series.
func DeleteArtist(ctx context.Context, q Queryer, artistID int64) error {
	var metadataID int64
	if err := q.QueryRowContext(ctx, `SELECT artist_metadata_id FROM artists WHERE id = ?`, artistID).Scan(&metadataID); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return fmt.Errorf("delete artist %d: %w", artistID, err)
	}
	rows, err := q.QueryContext(ctx, `SELECT id FROM albums WHERE artist_metadata_id = ?`, metadataID)
	if err != nil {
		return fmt.Errorf("delete artist %d: %w", artistID, err)
	}
	var albumIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		albumIDs = append(albumIDs, id)
	}
	rows.Close()
	for _, id := range albumIDs {
		if err := DeleteAlbum(ctx, q, id); err != nil {
			return err
		}
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM artists WHERE id = ?`, artistID); err != nil {
		return fmt.Errorf("delete artist %d: %w", artistID, err)
	}
	return nil
}

// ArtistLibrary is one row of the Music library page: an artist with the
// counts the cards, filters and sorts need. GetArtistDetail answers the
// same questions for one artist, but a library of hundreds can't afford
// three queries each, so the aggregates are joined here instead.
type ArtistLibrary struct {
	ArtistSummary
	QualityProfileName string
	AlbumCount         int
	AlbumsWithFiles    int
	// MissingAlbums counts monitored, released albums with no files - what
	// "Search all missing" would look for.
	MissingAlbums int
	TrackCount    int
	TracksWithF   int
	SizeOnDisk    int64
	// Status is MusicBrainz's life-span as one word - "active", "ended",
	// or "" when it hasn't been fetched since status was added. It is the
	// music equivalent of a series being Continuing or Ended, and what the
	// library's status filter reads.
	Status string
}

// Ended reports whether the artist has finished (split up, died).
func (a ArtistLibrary) Ended() bool { return a.Status == "ended" }

// StatusLabel is the artist's status as the cards show it.
func (a ArtistLibrary) StatusLabel() string {
	switch a.Status {
	case "ended":
		return "Ended"
	case "active":
		return "Active"
	}
	return ""
}

// ListArtistLibrary lists every artist with its album/track counts.
func ListArtistLibrary(ctx context.Context, q Queryer) ([]ArtistLibrary, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT a.id, a.artist_metadata_id, a.root_folder_id, a.quality_profile_id, am.name, a.path, a.monitored, a.added,
		       CASE WHEN a.cover_path IS NOT NULL THEN json_array('/music/artists/' || a.id || '/cover') ELSE am.images END,
		       COALESCE((SELECT p.name FROM quality_profiles p WHERE p.id = a.quality_profile_id), ''),
		       COALESCE(am.status, ''),
		       (SELECT COUNT(*) FROM albums al WHERE al.artist_metadata_id = a.artist_metadata_id),
		       (SELECT COUNT(*) FROM albums al WHERE al.artist_metadata_id = a.artist_metadata_id
		          AND EXISTS (SELECT 1 FROM album_releases r JOIN tracks t ON t.album_release_id = r.id
		                      WHERE r.album_id = al.id AND t.track_file_id IS NOT NULL)),
		       (SELECT COUNT(*) FROM albums al WHERE al.artist_metadata_id = a.artist_metadata_id
		          AND al.monitored = 1
		          AND (al.release_date IS NULL OR al.release_date <= CURRENT_TIMESTAMP)
		          AND NOT EXISTS (SELECT 1 FROM album_releases r JOIN tracks t ON t.album_release_id = r.id
		                          WHERE r.album_id = al.id AND t.track_file_id IS NOT NULL)),
		       COALESCE((SELECT COUNT(*) FROM albums al JOIN album_releases r ON r.album_id = al.id
		                 JOIN tracks t ON t.album_release_id = r.id WHERE al.artist_metadata_id = a.artist_metadata_id), 0),
		       COALESCE((SELECT COUNT(*) FROM albums al JOIN album_releases r ON r.album_id = al.id
		                 JOIN tracks t ON t.album_release_id = r.id
		                 WHERE al.artist_metadata_id = a.artist_metadata_id AND t.track_file_id IS NOT NULL), 0),
		       COALESCE((SELECT SUM(tf.size) FROM albums al JOIN album_releases r ON r.album_id = al.id
		                 JOIN tracks t ON t.album_release_id = r.id JOIN track_files tf ON tf.id = t.track_file_id
		                 WHERE al.artist_metadata_id = a.artist_metadata_id), 0)
		FROM artists a JOIN artist_metadata am ON am.id = a.artist_metadata_id
		ORDER BY a.added DESC`)
	if err != nil {
		return nil, fmt.Errorf("list artist library: %w", err)
	}
	defer rows.Close()

	var out []ArtistLibrary
	for rows.Next() {
		var a ArtistLibrary
		var images string
		if err := rows.Scan(&a.ID, &a.ArtistMetadataID, &a.RootFolderID, &a.QualityProfileID, &a.Name, &a.Path, &a.Monitored, &a.Added, &images,
			&a.QualityProfileName, &a.Status, &a.AlbumCount, &a.AlbumsWithFiles, &a.MissingAlbums, &a.TrackCount, &a.TracksWithF, &a.SizeOnDisk); err != nil {
			return nil, fmt.Errorf("scan artist library row: %w", err)
		}
		a.PosterURL = firstOrEmpty(unmarshalStringSlice(images))
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	applyPosters(ctx, q, "artist", len(out), func(i int) int64 { return out[i].ID }, func(i int, url string) { out[i].PosterURL = url })
	return out, nil
}

// TrackFileQualitiesByArtist lists the recorded quality of every track
// file, by artist id - what the Music page needs to count files below
// their profile's cutoff, the way the TV page counts episodes.
func TrackFileQualitiesByArtist(ctx context.Context, q Queryer) (map[int64][]releaseparse.FileQuality, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT a.id, tf.quality
		FROM artists a
		JOIN albums al ON al.artist_metadata_id = a.artist_metadata_id
		JOIN album_releases r ON r.album_id = al.id
		JOIN tracks t ON t.album_release_id = r.id
		JOIN track_files tf ON tf.id = t.track_file_id`)
	if err != nil {
		return nil, fmt.Errorf("track file qualities: %w", err)
	}
	defer rows.Close()
	out := map[int64][]releaseparse.FileQuality{}
	for rows.Next() {
		var artistID int64
		var raw string
		if err := rows.Scan(&artistID, &raw); err != nil {
			return nil, err
		}
		out[artistID] = append(out[artistID], unmarshalQuality(raw))
	}
	return out, rows.Err()
}
