package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// checkGrabLimit refuses a grab once an indexer's Grab Limit for the day is
// used up, before anything is asked of the tracker - trackers with a daily
// download cap (IPTorrents: 150) just serve an error page once it's hit,
// which looks like a broken indexer.
func (s *DownloadService) checkGrabLimit(ctx context.Context, release newznab.Release) error {
	if s == nil || release.IndexerID == 0 {
		return nil
	}
	ix, err := store.GetIndexer(ctx, s.DB, release.IndexerID)
	if err != nil || ix.GrabLimit <= 0 {
		return nil
	}
	used, since, err := store.GrabUsage(ctx, s.DB, ix, time.Now())
	if err != nil || used < ix.GrabLimit {
		return nil
	}
	return fmt.Errorf("%s's grab limit of %d is used up (%d grabbed since %s); it resets at %02d:00 UTC",
		ix.Name, ix.GrabLimit, used, since.Format("15:04 UTC on 2 Jan"), ix.GrabLimitResetHour)
}
