package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Fix match: an item added against the wrong title is pointed at the right
// metadata, keeping its folder, files, monitoring and profile.

// ErrAlreadyTracked means the chosen title is already another library item.
var ErrAlreadyTracked = errors.New("that title is already in the library")

func relink(ctx context.Context, q Queryer, table, column string, itemID, newMetadataID int64) (oldMetadataID int64, err error) {
	if err := q.QueryRowContext(ctx, `SELECT `+column+` FROM `+table+` WHERE id = ?`, itemID).Scan(&oldMetadataID); err != nil {
		return 0, fmt.Errorf("find %s %d: %w", table, itemID, err)
	}
	if oldMetadataID == newMetadataID {
		return oldMetadataID, nil
	}
	var otherID int64
	err = q.QueryRowContext(ctx, `SELECT id FROM `+table+` WHERE `+column+` = ? AND id <> ?`, newMetadataID, itemID).Scan(&otherID)
	if err == nil {
		return 0, ErrAlreadyTracked
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("check %s for metadata %d: %w", table, newMetadataID, err)
	}
	if _, err := q.ExecContext(ctx, `UPDATE `+table+` SET `+column+` = ? WHERE id = ?`, newMetadataID, itemID); err != nil {
		return 0, fmt.Errorf("relink %s %d: %w", table, itemID, err)
	}
	return oldMetadataID, nil
}

// dropOrphanMetadata removes a metadata row nothing refers to any more, so
// the old title doesn't linger; a row still in use is left alone.
func dropOrphanMetadata(ctx context.Context, q Queryer, entityType, table string, metadataID int64) {
	if _, err := q.ExecContext(ctx, `DELETE FROM `+table+` WHERE id = ?`, metadataID); err != nil {
		return
	}
	_, _ = q.ExecContext(ctx, `DELETE FROM external_ids WHERE entity_type = ? AND entity_id = ?`, entityType, metadataID)
	_, _ = q.ExecContext(ctx, `DELETE FROM metadata_field_provenance WHERE entity_type = ? AND entity_id = ?`, entityType, metadataID)
}

// RelinkMovieMetadata points movieID at newMetadataID.
func RelinkMovieMetadata(ctx context.Context, q Queryer, movieID, newMetadataID int64) error {
	old, err := relink(ctx, q, "movies", "movie_metadata_id", movieID, newMetadataID)
	if err != nil {
		return err
	}
	if old != newMetadataID {
		dropOrphanMetadata(ctx, q, "movie", "movie_metadata", old)
	}
	return nil
}

// RelinkSeriesMetadata points seriesID at newMetadataID and drops the
// episodes that have no file, so the new title's own episodes replace them
// (the caller upserts those next). Episodes with files stay.
func RelinkSeriesMetadata(ctx context.Context, q Queryer, seriesID, newMetadataID int64) error {
	old, err := relink(ctx, q, "series", "series_metadata_id", seriesID, newMetadataID)
	if err != nil {
		return err
	}
	if old == newMetadataID {
		return nil
	}
	for _, stmt := range []string{
		`DELETE FROM external_ids WHERE entity_type = 'episode' AND entity_id IN (SELECT id FROM episodes WHERE series_id = ? AND episode_file_id IS NULL)`,
		`DELETE FROM episodes WHERE series_id = ? AND episode_file_id IS NULL`,
		`DELETE FROM seasons WHERE series_id = ? AND NOT EXISTS (SELECT 1 FROM episodes e WHERE e.season_id = seasons.id)`,
	} {
		if _, err := q.ExecContext(ctx, stmt, seriesID); err != nil {
			return fmt.Errorf("drop unfiled episodes of series %d: %w", seriesID, err)
		}
	}
	dropOrphanMetadata(ctx, q, "series", "series_metadata", old)
	return nil
}

// RelinkArtistMetadata points artistID, and the albums and tracks credited
// to its old identity, at newMetadataID.
func RelinkArtistMetadata(ctx context.Context, q Queryer, artistID, newMetadataID int64) error {
	old, err := relink(ctx, q, "artists", "artist_metadata_id", artistID, newMetadataID)
	if err != nil || old == newMetadataID {
		return err
	}
	for _, table := range []string{"albums", "tracks"} {
		if _, err := q.ExecContext(ctx, `UPDATE `+table+` SET artist_metadata_id = ? WHERE artist_metadata_id = ?`, newMetadataID, old); err != nil {
			return fmt.Errorf("relink %s of artist %d: %w", table, artistID, err)
		}
	}
	return nil
}

// ReplaceExternalID gives entity a different provider id - an album moving
// to another MusicBrainz release group - refusing one another entity holds.
func ReplaceExternalID(ctx context.Context, q Queryer, entityType string, entityID int64, provider, externalID string) error {
	if other, found, err := FindEntityIDByExternalID(ctx, q, entityType, provider, externalID); err != nil {
		return err
	} else if found && other != entityID {
		return ErrAlreadyTracked
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM external_ids WHERE entity_type = ? AND entity_id = ? AND provider = ?`, entityType, entityID, provider); err != nil {
		return fmt.Errorf("replace external id: %w", err)
	}
	return UpsertExternalID(ctx, q, entityType, entityID, provider, externalID)
}

// DeleteUnfiledAlbumReleases drops albumID's releases whose tracks have no
// files, so a corrected album's own track listing can take their place.
func DeleteUnfiledAlbumReleases(ctx context.Context, q Queryer, albumID int64) error {
	for _, stmt := range []string{
		`DELETE FROM external_ids WHERE entity_type = 'track' AND entity_id IN (SELECT t.id FROM tracks t JOIN album_releases r ON r.id = t.album_release_id WHERE r.album_id = ? AND NOT EXISTS (SELECT 1 FROM tracks x WHERE x.album_release_id = r.id AND x.track_file_id IS NOT NULL))`,
		`DELETE FROM tracks WHERE album_release_id IN (SELECT r.id FROM album_releases r WHERE r.album_id = ? AND NOT EXISTS (SELECT 1 FROM tracks x WHERE x.album_release_id = r.id AND x.track_file_id IS NOT NULL))`,
		`DELETE FROM external_ids WHERE entity_type = 'release' AND entity_id IN (SELECT r.id FROM album_releases r WHERE r.album_id = ? AND NOT EXISTS (SELECT 1 FROM tracks x WHERE x.album_release_id = r.id))`,
		`DELETE FROM album_releases WHERE album_id = ? AND NOT EXISTS (SELECT 1 FROM tracks x WHERE x.album_release_id = album_releases.id)`,
	} {
		if _, err := q.ExecContext(ctx, stmt, albumID); err != nil {
			return fmt.Errorf("drop unfiled releases of album %d: %w", albumID, err)
		}
	}
	return nil
}
