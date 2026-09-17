package decision

import (
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// blocklistKey says which library item a blocklist entry belongs to.
type blocklistKey struct {
	kind string // movie, series or album
	id   int64
}

func indexBlocklist(entries []store.BlocklistEntry) map[blocklistKey][]store.BlocklistEntry {
	out := map[blocklistKey][]store.BlocklistEntry{}
	for _, b := range entries {
		var k blocklistKey
		switch {
		case b.MovieID.Valid:
			k = blocklistKey{"movie", b.MovieID.Int64}
		case b.SeriesID.Valid:
			k = blocklistKey{"series", b.SeriesID.Int64}
		case b.AlbumID.Valid:
			k = blocklistKey{"album", b.AlbumID.Int64}
		default:
			continue
		}
		out[k] = append(out[k], b)
	}
	return out
}

// blocklisted follows Sonarr's BlocklistService: an entry for the same item
// with the same title, then for a torrent the same infohash when the release
// has one (otherwise the same indexer), and for usenet the same publish date
// (within two minutes) and size.
func (e *Engine) blocklisted(kind string, id int64, r newznab.Release) bool {
	for _, b := range e.blocklist[blocklistKey{kind, id}] {
		if !strings.EqualFold(b.SourceTitle, r.Title) {
			continue
		}
		if r.Protocol == newznab.ProtocolTorrent {
			if r.InfoHash != "" {
				if strings.EqualFold(r.InfoHash, b.InfoHash.String) {
					return true
				}
				continue
			}
			if strings.EqualFold(b.Indexer, r.Indexer) {
				return true
			}
			continue
		}
		if sameNZB(b, r) {
			return true
		}
	}
	return false
}

func sameNZB(b store.BlocklistEntry, r newznab.Release) bool {
	if b.Published.Valid {
		if r.PublishDate.IsZero() {
			return false
		}
		if d := b.Published.Time.Sub(r.PublishDate); d < -2*time.Minute || d > 2*time.Minute {
			return false
		}
	}
	if b.Size.Valid && b.Size.Int64 != r.Size {
		return false
	}
	return true
}

func (e *Engine) blocklistRule(d *Decision, kind string, id int64) {
	if e.blocklisted(kind, id, d.Release) {
		d.reject("Release is blocklisted")
	}
}
