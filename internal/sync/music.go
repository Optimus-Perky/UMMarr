package sync

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/merge"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/musicbrainz"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// MusicService adds/refreshes artists and albums.
type MusicService struct {
	DB          *sql.DB
	MusicBrainz *musicbrainz.Client
	// Probe reads a file with FFprobe, so the release picker can say which
	// release the album's own files claim to be. Nil just leaves that
	// unknown.
	Probe func(context.Context, string) (mediainfo.Info, error)
}

// AddArtistByMBID fetches an artist from MusicBrainz and persists both
// the artist_metadata and per-instance artists row in one transaction.
func (s *MusicService) AddArtistByMBID(ctx context.Context, mbid string, rootFolderID, qualityProfileID int64) (int64, error) {
	mbArtist, err := s.MusicBrainz.GetArtist(ctx, mbid)
	if err != nil {
		return 0, fmt.Errorf("fetch musicbrainz artist %s: %w", mbid, err)
	}

	merged, _, _ := merge.MergeArtistFromProvider(mbArtist)

	var artistID int64
	err = store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		metadataID, err := store.UpsertArtistMetadata(ctx, tx, merged)
		if err != nil {
			return err
		}
		artistID, err = store.UpsertArtist(ctx, tx, metadataID, qualityProfileID, rootFolderID, true)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("add artist (mbid %s): %w", mbid, err)
	}
	return artistID, nil
}

// AddAlbumByMBID fetches a release-group from MusicBrainz (including its
// series relationships), merges it, persists the albums row under
// artistID, links it to a compilation_series if MusicBrainz has modeled
// it as part of one, then fetches a representative release for the real
// track listing - attributing each track to its own credited artist
// (creating identity-only artist_metadata rows for any not already
// tracked), which is what makes Various Artists compilations resolve
// correctly per-track.
func (s *MusicService) AddAlbumByMBID(ctx context.Context, releaseGroupMBID string, artistID int64) (int64, error) {
	return s.AddAlbumByMBIDWithHint(ctx, releaseGroupMBID, artistID, ReleaseHint{})
}

// AddAlbumByMBIDWithHint is AddAlbumByMBID told what the folder it came
// from looks like, so the right pressing is chosen rather than whichever
// release MusicBrainz listed first (see album_releases.go).
func (s *MusicService) AddAlbumByMBIDWithHint(ctx context.Context, releaseGroupMBID string, artistID int64, hint ReleaseHint) (int64, error) {
	rg, err := s.MusicBrainz.GetReleaseGroup(ctx, releaseGroupMBID)
	if err != nil {
		return 0, fmt.Errorf("fetch musicbrainz release-group %s: %w", releaseGroupMBID, err)
	}
	mergedAlbum, _, _ := merge.MergeAlbumFromProvider(rg)

	var artistMetadataID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT artist_metadata_id FROM artists WHERE id = ?`, artistID).Scan(&artistMetadataID); err != nil {
		return 0, fmt.Errorf("find artist %d: %w", artistID, err)
	}

	var albumID int64
	err = store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		var created bool
		var err error
		albumID, created, err = store.UpsertAlbum(ctx, tx, artistMetadataID, mergedAlbum)
		if err != nil {
			return err
		}
		if mergedAlbum.Series != nil {
			seriesID, err := store.UpsertCompilationSeries(ctx, tx, mergedAlbum.Series.Name, mergedAlbum.Series.Name, mergedAlbum.Series.MusicBrainzSeriesID)
			if err != nil {
				return err
			}
			if err := store.LinkAlbumToCompilationSeries(ctx, tx, seriesID, albumID, mergedAlbum.Series.SequenceNumber); err != nil {
				return err
			}
		}
		// Path resolution happens only on first creation, and only after
		// any compilation_series link above is in place - a VA album's
		// path depends on that link (see UpsertAlbum's comment).
		if created {
			if err := store.SetAlbumPath(ctx, tx, albumID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("add album (mbid %s): %w", releaseGroupMBID, err)
	}

	if err := s.syncRepresentativeRelease(ctx, rg, albumID, hint); err != nil {
		// The album itself is saved; a release/track sync failure (e.g. a
		// transient MusicBrainz error) shouldn't undo that - surfaced as
		// an error so the caller knows tracks didn't sync, but the album
		// add as a whole isn't rolled back.
		return albumID, fmt.Errorf("album added but release/track sync failed: %w", err)
	}
	return albumID, nil
}

// syncRepresentativeRelease picks one release from the release-group
// (preferring Status == "Official") and syncs its full track listing.
func (s *MusicService) syncRepresentativeRelease(ctx context.Context, rg *musicbrainz.ReleaseGroup, albumID int64, hint ReleaseHint) error {
	countries := []string{}
	if ms, err := store.GetMediaSettings(ctx, s.DB); err == nil {
		countries = ms.ReleaseCountries()
	}
	releaseRef := PickRelease(rg.Releases, hint, countries)
	if releaseRef == nil {
		return nil // no releases listed - nothing to sync tracks from
	}
	_, err := s.syncRelease(ctx, albumID, releaseRef.ID)
	return err
}

// syncRelease writes one specific release's tracks onto an album and
// returns the release row it wrote.
func (s *MusicService) syncRelease(ctx context.Context, albumID int64, releaseMBID string) (int64, error) {
	release, err := s.MusicBrainz.GetRelease(ctx, releaseMBID)
	if err != nil {
		return 0, fmt.Errorf("fetch musicbrainz release %s: %w", releaseMBID, err)
	}
	mergedRelease, tracks, _ := merge.MergeReleaseFromProvider(release)

	// A track with no credit of its own belongs to the album's artist.
	var albumArtistID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT artist_metadata_id FROM albums WHERE id = ?`, albumID).Scan(&albumArtistID); err != nil {
		return 0, fmt.Errorf("find album %d: %w", albumID, err)
	}

	var releaseID int64
	err = store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		var err error
		releaseID, err = store.UpsertAlbumRelease(ctx, tx, albumID, mergedRelease)
		if err != nil {
			return err
		}

		// Dedup artist_metadata lookups/inserts within this transaction -
		// the same credited artist can appear on many tracks of a
		// compilation, and each only needs upserting once.
		artistMetadataIDByMBID := map[string]int64{}
		for _, track := range tracks {
			trackArtistID := albumArtistID
			if len(track.ArtistCredits) > 0 {
				credit := track.ArtistCredits[0]
				id, err := s.resolveTrackArtist(ctx, tx, credit, artistMetadataIDByMBID)
				if err != nil {
					return err
				}
				trackArtistID = id
			}
			if _, err := store.UpsertTrack(ctx, tx, releaseID, trackArtistID, track); err != nil {
				return err
			}
		}
		return nil
	})
	return releaseID, err
}

func (s *MusicService) resolveTrackArtist(ctx context.Context, tx *sql.Tx, credit metadata.ArtistCreditRef, cache map[string]int64) (int64, error) {
	if id, ok := cache[credit.MusicBrainzArtistID]; ok {
		return id, nil
	}
	id, found, err := store.FindEntityIDByExternalID(ctx, tx, "artist", "musicbrainz", credit.MusicBrainzArtistID)
	if err != nil {
		return 0, err
	}
	if !found {
		id, err = store.UpsertArtistMetadata(ctx, tx, metadata.ArtistMetadata{
			Name:        metadata.Field[string]{Value: credit.Name, Provider: "musicbrainz"},
			ExternalIDs: map[string]string{"musicbrainz": credit.MusicBrainzArtistID},
		})
		if err != nil {
			return 0, err
		}
	}
	cache[credit.MusicBrainzArtistID] = id
	return id, nil
}

// FixMatchArtist points artistID at MusicBrainz artist mbid instead, along
// with the albums and tracks credited to it.
func (s *MusicService) FixMatchArtist(ctx context.Context, artistID int64, mbid string) error {
	mbArtist, err := s.MusicBrainz.GetArtist(ctx, mbid)
	if err != nil {
		return fmt.Errorf("fetch musicbrainz artist %s: %w", mbid, err)
	}
	merged, _, _ := merge.MergeArtistFromProvider(mbArtist)
	return store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		metadataID, err := store.UpsertArtistMetadata(ctx, tx, merged)
		if err != nil {
			return err
		}
		return store.RelinkArtistMetadata(ctx, tx, artistID, metadataID)
	})
}

// FixMatchAlbum makes albumID the MusicBrainz release group mbid instead:
// its details are replaced, releases whose tracks have no files are
// replaced by the new group's representative release, and files stay put.
func (s *MusicService) FixMatchAlbum(ctx context.Context, albumID int64, releaseGroupMBID string) error {
	rg, err := s.MusicBrainz.GetReleaseGroup(ctx, releaseGroupMBID)
	if err != nil {
		return fmt.Errorf("fetch musicbrainz release-group %s: %w", releaseGroupMBID, err)
	}
	mergedAlbum, _, _ := merge.MergeAlbumFromProvider(rg)
	var artistMetadataID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT artist_metadata_id FROM albums WHERE id = ?`, albumID).Scan(&artistMetadataID); err != nil {
		return fmt.Errorf("find album %d: %w", albumID, err)
	}
	err = store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		// With the album's id moved to the new group, UpsertAlbum updates
		// this row rather than adding another.
		if err := store.ReplaceExternalID(ctx, tx, "album", albumID, "musicbrainz", releaseGroupMBID); err != nil {
			return err
		}
		if _, _, err := store.UpsertAlbum(ctx, tx, artistMetadataID, mergedAlbum); err != nil {
			return err
		}
		return store.DeleteUnfiledAlbumReleases(ctx, tx, albumID)
	})
	if err != nil {
		return err
	}
	return s.syncRepresentativeRelease(ctx, rg, albumID, ReleaseHint{})
}
