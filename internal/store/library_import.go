package store

import (
	"context"
	"fmt"
)

// GetRootFolder reads one library folder.
func GetRootFolder(ctx context.Context, q Queryer, id int64) (RootFolder, error) {
	f := RootFolder{ID: id}
	if err := q.QueryRowContext(ctx, `SELECT path, media_type FROM root_folders WHERE id = ?`, id).Scan(&f.Path, &f.MediaType); err != nil {
		return RootFolder{}, fmt.Errorf("get root folder %d: %w", id, err)
	}
	return f, nil
}

// The library import points items at the folders they already live in.

func SetSeriesPath(ctx context.Context, q Queryer, seriesID int64, path string) error {
	if _, err := q.ExecContext(ctx, `UPDATE series SET path = ? WHERE id = ?`, path, seriesID); err != nil {
		return fmt.Errorf("set series %d path: %w", seriesID, err)
	}
	return nil
}

func SetArtistPath(ctx context.Context, q Queryer, artistID int64, path string) error {
	if _, err := q.ExecContext(ctx, `UPDATE artists SET path = ? WHERE id = ?`, path, artistID); err != nil {
		return fmt.Errorf("set artist %d path: %w", artistID, err)
	}
	return nil
}

func SetAlbumPathTo(ctx context.Context, q Queryer, albumID int64, path string) error {
	if _, err := q.ExecContext(ctx, `UPDATE albums SET path = ? WHERE id = ?`, path, albumID); err != nil {
		return fmt.Errorf("set album %d path: %w", albumID, err)
	}
	return nil
}
