package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/downloadclient"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Failed download handling, as in Sonarr and Radarr: a release whose
// download fails is blocklisted so no search picks it again, then -
// depending on Settings -> Download Clients - removed from its client and
// replaced by a fresh search.

// ErrReleaseBlocklisted is a grab refused because the torrent's infohash is
// on the blocklist and its indexer rejects blocklisted hashes.
var ErrReleaseBlocklisted = errors.New("release is blocklisted")

// Blocklist choices when removing a grab from the queue - Sonarr's Remove
// dialog.
const (
	BlocklistNone      = "none"   // just remove it
	BlocklistAndSearch = "search" // blocklist, then search for another release
	BlocklistOnly      = "only"   // blocklist without searching
)

// releaseHash is the infohash of a torrent release: the indexer's when it
// gave one, otherwise read from the fetched magnet link or .torrent file.
func releaseHash(release newznab.Release, fetched *newznab.FetchedRelease) string {
	if release.InfoHash != "" {
		return release.InfoHash
	}
	if fetched == nil {
		return ""
	}
	var (
		hash string
		err  error
	)
	switch fetched.Kind {
	case newznab.KindMagnet:
		hash, err = downloadclient.MagnetInfoHash(fetched.MagnetURI)
	case newznab.KindTorrentFile:
		hash, err = downloadclient.TorrentInfoHash(fetched.Data)
	}
	if err != nil {
		return ""
	}
	return hash
}

// rejectBlocklistedHash refuses a torrent whose infohash is blocklisted for
// the same item, when its indexer has "Reject Blocklisted Torrent Hashes
// While Grabbing" on.
func (s *DownloadService) rejectBlocklistedHash(ctx context.Context, g store.Grab, release newznab.Release, hash string) error {
	if hash == "" || release.Protocol != newznab.ProtocolTorrent || release.IndexerID == 0 {
		return nil
	}
	ix, err := store.GetIndexer(ctx, s.DB, release.IndexerID)
	if err != nil || !ix.RejectBlocklisted {
		return nil
	}
	blocked, err := store.HashBlocklisted(ctx, s.DB, g, hash)
	if err != nil {
		return err
	}
	if blocked {
		return ErrReleaseBlocklisted
	}
	return nil
}

// blocklistGrab puts a grab's release on the blocklist.
func (s *DownloadService) blocklistGrab(ctx context.Context, g store.Grab, message string) error {
	quality := releaseparse.Parse(g.ReleaseTitle).Key()
	_, err := store.AddBlocklist(ctx, s.DB, store.BlocklistForGrab(g, quality, message))
	return err
}

// removeFromClient deletes a grab's download, and its data, from its client.
func (s *DownloadService) removeFromClient(ctx context.Context, g store.Grab) error {
	if !g.DownloadClientID.Valid {
		return nil
	}
	client, err := s.clientForGrab(ctx, g)
	if err != nil {
		return err
	}
	return client.Remove(ctx, g.DownloadClientID.String, true)
}

// clientRemovesFailed says whether the client a grab went to has Remove
// Failed on.
func (s *DownloadService) clientRemovesFailed(ctx context.Context, g store.Grab) bool {
	if !g.DownloadClientRef.Valid || s.DB == nil {
		return false
	}
	dc, err := store.GetDownloadClient(ctx, s.DB, g.DownloadClientRef.Int64)
	return err == nil && dc.RemoveFailed
}

// redownload starts a search for another release for whatever g was for,
// in the background, when Redownload is on or the user asked for it.
func (s *DownloadService) redownload(g store.Grab) {
	if s.Redownload == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		s.Redownload(ctx, g)
	}()
}

// downloadFailed runs when a client reports a download failed: blocklist it,
// remove it from the client if that client says so, and search again if
// Redownload is on.
func (s *DownloadService) downloadFailed(ctx context.Context, g store.Grab, message string) {
	if err := s.blocklistGrab(ctx, g, message); err != nil {
		log.Printf("failed download %d (%s): blocklist: %v", g.ID, g.ReleaseTitle, err)
		return
	}
	if s.clientRemovesFailed(ctx, g) {
		if err := s.removeFromClient(ctx, g); err != nil {
			log.Printf("failed download %d (%s): remove from client: %v", g.ID, g.ReleaseTitle, err)
		}
	}
	if handling, err := store.GetDownloadHandling(ctx, s.DB); err == nil && handling.RedownloadFailed {
		s.redownload(g)
	}
}

// RemoveGrab is the queue's Remove: optionally delete the download from its
// client, and blocklist the release (searching again or not). The grab is
// left in the history as removed, or failed when it was blocklisted.
func (s *DownloadService) RemoveGrab(ctx context.Context, g store.Grab, fromClient bool, blocklist string) (store.Grab, error) {
	if fromClient {
		if err := s.removeFromClient(ctx, g); err != nil {
			return g, fmt.Errorf("remove from download client: %w", err)
		}
	}
	switch blocklist {
	case BlocklistAndSearch, BlocklistOnly:
		const message = "Manually marked as failed"
		if err := s.blocklistGrab(ctx, g, message); err != nil {
			return g, err
		}
		g = s.persistGrabStatus(ctx, g, "failed", message)
		if blocklist == BlocklistAndSearch {
			s.redownload(g)
		}
		return g, nil
	}
	return s.persistGrabStatus(ctx, g, "removed", ""), nil
}

// Redownload searches for another release for the item a failed grab was
// for: its movie, album, episode, season or series.
func (s *SearchService) Redownload(ctx context.Context, g store.Grab) {
	var (
		report SearchReport
		err    error
	)
	switch {
	case g.MovieID.Valid:
		report, err = s.SearchMovie(ctx, g.MovieID.Int64)
	case g.AlbumID.Valid:
		report, err = s.SearchAlbum(ctx, g.AlbumID.Int64)
	case g.SeriesID.Valid && g.SeasonNumber.Valid && g.EpisodeNumber.Valid:
		report, err = s.searchEpisodeNumber(ctx, g.SeriesID.Int64, int(g.SeasonNumber.Int64), int(g.EpisodeNumber.Int64))
	case g.SeriesID.Valid && g.SeasonNumber.Valid:
		season := int(g.SeasonNumber.Int64)
		report, err = s.SearchSeries(ctx, g.SeriesID.Int64, &season)
	case g.SeriesID.Valid:
		report, err = s.SearchSeries(ctx, g.SeriesID.Int64, nil)
	default:
		return
	}
	if err != nil {
		log.Printf("redownload after failed %q: %v", g.ReleaseTitle, err)
		return
	}
	log.Printf("redownload after failed %q: %s", g.ReleaseTitle, report.Summary())
}

func (s *SearchService) searchEpisodeNumber(ctx context.Context, seriesID int64, season, episode int) (SearchReport, error) {
	series, err := store.GetWantedSeries(ctx, s.DB, seriesID)
	if err != nil {
		return SearchReport{}, err
	}
	for _, ep := range series.Episodes {
		if ep.SeasonNumber == season && ep.EpisodeNumber == episode {
			return s.SearchEpisode(ctx, seriesID, ep.ID)
		}
	}
	return SearchReport{}, fmt.Errorf("S%02dE%02d isn't part of series %d", season, episode, seriesID)
}

func nullTime(t time.Time) sql.NullTime {
	return sql.NullTime{Time: t, Valid: !t.IsZero()}
}

// GrabFolder is where a grab's download sits on disk, as UMMarr sees it: the
// folder, and the one file to take from it when the download is a single
// file sitting in the client's shared download folder.
func (s *DownloadService) GrabFolder(ctx context.Context, g store.Grab) (folder, onlyFile string, err error) {
	if !g.DownloadClientID.Valid {
		return "", "", fmt.Errorf("the grab has no download client id")
	}
	client, err := s.clientForGrab(ctx, g)
	if err != nil {
		return "", "", err
	}
	statuses, err := client.Statuses(ctx, []string{g.DownloadClientID.String})
	if err != nil {
		return "", "", err
	}
	st, ok := statuses[g.DownloadClientID.String]
	if !ok {
		return "", "", fmt.Errorf("the download is no longer in the client")
	}
	root := filepath.Join(st.SavePath, st.Name)
	info, err := os.Stat(root)
	if err != nil {
		return "", "", fmt.Errorf("download not found on disk at %s", root)
	}
	if info.IsDir() {
		return root, "", nil
	}
	return st.SavePath, st.Name, nil
}
