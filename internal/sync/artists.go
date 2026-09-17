package sync

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Artist-level actions behind the artist page's toolbar: refresh, scan,
// search and delete, matching what movies and series already have.

// RefreshArtist re-fetches an artist from MusicBrainz using its existing id.
func (s *MusicService) RefreshArtist(ctx context.Context, artistID int64) error {
	var metadataID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT artist_metadata_id FROM artists WHERE id = ?`, artistID).Scan(&metadataID); err != nil {
		return fmt.Errorf("find artist %d: %w", artistID, err)
	}
	mbid, found, err := store.GetExternalID(ctx, s.DB, "artist", metadataID, "musicbrainz")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("artist %d has no MusicBrainz id to refresh from", artistID)
	}
	return s.FixMatchArtist(ctx, artistID, mbid)
}

// ScanArtist looks through the folders of an artist's albums for files the
// library doesn't have yet, and drops records whose files have gone.
func (s *ImportService) ScanArtist(ctx context.Context, artistID int64) (imported int, err error) {
	artist, found, err := store.GetArtistDetail(ctx, s.DB, artistID)
	if err != nil || !found {
		return 0, err
	}
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return 0, err
	}
	albums, err := store.ListAlbumsForArtist(ctx, s.DB, artist.ArtistMetadataID)
	if err != nil {
		return 0, err
	}
	for _, album := range albums {
		if !album.Path.Valid {
			continue
		}
		s.reconcileAlbum(ctx, ms, album.ID, album.Path.String)
		imported += s.scanAlbumFolder(ctx, ms, album.ID, album.Path.String, true)
	}
	return imported, nil
}

// DeleteArtist removes an artist from the library, with its files when asked.
func (s *ImportService) DeleteArtist(ctx context.Context, artistID int64, deleteFiles bool) error {
	artist, found, err := store.GetArtistDetail(ctx, s.DB, artistID)
	if err != nil || !found {
		return err
	}
	deleted := store.Event{Event: store.EventDeleted, MediaType: "music", Title: artist.Name, Detail: "Artist removed, files kept"}
	if deleteFiles {
		deleted.Detail = "Artist and files removed"
	}
	if err := store.WithTx(ctx, s.DB, func(tx *sql.Tx) error { return store.DeleteArtist(ctx, tx, artistID) }); err != nil {
		return err
	}
	s.Events.Record(ctx, deleted)
	if !deleteFiles || !artist.Path.Valid {
		return nil
	}
	return s.removeInsideLibrary(ctx, "music", artist.Path.String)
}

// SearchArtist searches for every monitored album of an artist that has no
// files, and grabs what the decision engine accepts.
func (s *SearchService) SearchArtist(ctx context.Context, artistID int64) (SearchReport, error) {
	var report SearchReport
	artist, found, err := store.GetArtistDetail(ctx, s.DB, artistID)
	if err != nil {
		return report, err
	}
	if !found {
		return report, fmt.Errorf("artist %d isn't in the library", artistID)
	}
	albums, err := store.ListAlbumsForArtist(ctx, s.DB, artist.ArtistMetadataID)
	if err != nil {
		return report, err
	}
	for _, album := range albums {
		if !album.Monitored {
			continue
		}
		wanted, err := store.GetWantedAlbum(ctx, s.DB, album.ID)
		if err != nil || wanted.FileCount() > 0 {
			continue
		}
		albumReport, err := s.SearchAlbum(ctx, album.ID)
		if err != nil {
			report.GrabErrors = append(report.GrabErrors, fmt.Sprintf("%s: %v", album.Title, err))
			continue
		}
		report.Searched = max(report.Searched, albumReport.Searched)
		report.Releases += albumReport.Releases
		report.Grabbed = append(report.Grabbed, albumReport.Grabbed...)
		report.Errors = append(report.Errors, albumReport.Errors...)
		report.GrabErrors = append(report.GrabErrors, albumReport.GrabErrors...)
	}
	return report, nil
}
