package store

import (
	"context"
	"database/sql"
	"fmt"
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
		       COALESCE(m.overview, ''), COALESCE(m.genres, '[]'), COALESCE(m.disambiguation, ''), COALESCE(m.images, '[]'),
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
