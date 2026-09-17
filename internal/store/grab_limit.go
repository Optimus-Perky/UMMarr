package store

import (
	"context"
	"fmt"
	"time"
)

// GrabWindowStart is when an indexer's daily grab counter last reset: the
// most recent resetHour (UTC) at or before now. Trackers like IPTorrents
// reset the whole allowance at a fixed time of day rather than rolling.
func GrabWindowStart(now time.Time, resetHour int) time.Time {
	if resetHour < 0 || resetHour > 23 {
		resetHour = 0
	}
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), resetHour, 0, 0, 0, time.UTC)
	if start.After(now) {
		start = start.AddDate(0, 0, -1)
	}
	return start
}

// CountGrabsSince counts what UMMarr has grabbed from an indexer since a
// point in time - every grab counts, whatever became of the download.
func CountGrabsSince(ctx context.Context, q Queryer, indexerID int64, since time.Time) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM grabs WHERE indexer_id = ? AND added >= ?`,
		indexerID, since.UTC().Format("2006-01-02 15:04:05")).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count grabs for indexer %d: %w", indexerID, err)
	}
	return n, nil
}

// GrabUsage is how much of an indexer's Grab Limit is used in the current
// day, for the Indexers page and the limit check.
func GrabUsage(ctx context.Context, q Queryer, ix Indexer, now time.Time) (used int, since time.Time, err error) {
	since = GrabWindowStart(now, ix.GrabLimitResetHour)
	used, err = CountGrabsSince(ctx, q, ix.ID, since)
	return used, since, err
}
