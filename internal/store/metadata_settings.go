package store

import (
	"context"
	"fmt"
)

// MetadataSettings is the singleton metadata_settings row (migration 00026).
type MetadataSettings struct {
	Enabled      bool // Kodi (XBMC) / Emby
	Jellyfin     bool
	Plex         bool
	MovieNFO     bool
	MovieImages  bool
	SeriesNFO    bool
	EpisodeNFO   bool
	SeriesImages bool
	AlbumNFO     bool
	AlbumImages  bool
}

// GetMetadataSettings reads the row.
func GetMetadataSettings(ctx context.Context, q Queryer) (MetadataSettings, error) {
	var s MetadataSettings
	err := q.QueryRowContext(ctx, `SELECT enabled, jellyfin_enabled, plex_enabled, movie_nfo, movie_images, series_nfo, episode_nfo, series_images, album_nfo, album_images FROM metadata_settings WHERE id = 1`).
		Scan(&s.Enabled, &s.Jellyfin, &s.Plex, &s.MovieNFO, &s.MovieImages, &s.SeriesNFO, &s.EpisodeNFO, &s.SeriesImages, &s.AlbumNFO, &s.AlbumImages)
	if err != nil {
		return MetadataSettings{}, fmt.Errorf("get metadata settings: %w", err)
	}
	return s, nil
}

// UpdateMetadataSettings saves the row.
func UpdateMetadataSettings(ctx context.Context, q Queryer, s MetadataSettings) error {
	_, err := q.ExecContext(ctx, `UPDATE metadata_settings SET enabled = ?, jellyfin_enabled = ?, plex_enabled = ?, movie_nfo = ?, movie_images = ?, series_nfo = ?, episode_nfo = ?, series_images = ?, album_nfo = ?, album_images = ? WHERE id = 1`,
		s.Enabled, s.Jellyfin, s.Plex, s.MovieNFO, s.MovieImages, s.SeriesNFO, s.EpisodeNFO, s.SeriesImages, s.AlbumNFO, s.AlbumImages)
	if err != nil {
		return fmt.Errorf("update metadata settings: %w", err)
	}
	return nil
}

// ItemImages lists the metadata images (poster first, then backdrop) of a
// movie, series or album.
func ItemImages(ctx context.Context, q Queryer, mediaType string, id int64) []string {
	var query string
	switch mediaType {
	case "movie":
		query = `SELECT mm.images FROM movies m JOIN movie_metadata mm ON mm.id = m.movie_metadata_id WHERE m.id = ?`
	case "series":
		query = `SELECT sm.images FROM series s JOIN series_metadata sm ON sm.id = s.series_metadata_id WHERE s.id = ?`
	case "artist":
		query = `SELECT am.images FROM artists a JOIN artist_metadata am ON am.id = a.artist_metadata_id WHERE a.id = ?`
	case "album":
		query = `SELECT images FROM albums WHERE id = ?`
	default:
		return nil
	}
	var raw string
	if err := q.QueryRowContext(ctx, query, id).Scan(&raw); err != nil {
		return nil
	}
	return unmarshalStringSlice(raw)
}

// Any reports whether any provider is on.
func (s MetadataSettings) Any() bool { return s.Enabled || s.Jellyfin || s.Plex }

// NFO reports whether a provider that reads .nfo files is on (Plex doesn't).
func (s MetadataSettings) NFO() bool { return s.Enabled || s.Jellyfin }
