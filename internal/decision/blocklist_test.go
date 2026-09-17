package decision

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestBlocklist_Torrent(t *testing.T) {
	release := torrent("Inception 2010 1080p BluRay")
	e := engine()
	e.SetBlocklist([]store.BlocklistEntry{{MovieID: sql.NullInt64{Int64: 7, Valid: true}, SourceTitle: "inception 2010 1080p bluray", Protocol: "torrent", Indexer: "Tracker"}})

	wantRejected(t, e.Movie(inception(), []newznab.Release{release})[0], "Release is blocklisted")

	other := inception()
	other.ID = 8
	if d := e.Movie(other, []newznab.Release{release})[0]; !d.Approved() {
		t.Errorf("want a release blocklisted for another movie still approved, got %q", reasons(d))
	}

	elsewhere := release
	elsewhere.Indexer = "Other Tracker"
	wantApproved(t, e.Movie(inception(), []newznab.Release{elsewhere})[0])

	// With an infohash on both sides, the hash decides - not the indexer.
	e.SetBlocklist([]store.BlocklistEntry{{MovieID: sql.NullInt64{Int64: 7, Valid: true}, SourceTitle: release.Title, Protocol: "torrent", Indexer: "Tracker",
		InfoHash: sql.NullString{String: "aaaa", Valid: true}}})
	hashed := release
	hashed.InfoHash = "AAAA"
	wantRejected(t, e.Movie(inception(), []newznab.Release{hashed})[0], "Release is blocklisted")
	hashed.InfoHash = "bbbb"
	wantApproved(t, e.Movie(inception(), []newznab.Release{hashed})[0])
}

func TestBlocklist_Usenet(t *testing.T) {
	e := engine()
	e.Protocols[newznab.ProtocolUsenet] = true
	release := newznab.Release{Title: "Breaking Bad S01E01 720p HDTV", Protocol: newznab.ProtocolUsenet, IndexerID: 2, Indexer: "NZBs",
		Size: 1 << 30, PublishDate: now.Add(-24 * time.Hour), Seeders: -1, Peers: -1}
	e.SetBlocklist([]store.BlocklistEntry{{SeriesID: sql.NullInt64{Int64: 3, Valid: true}, SourceTitle: release.Title, Protocol: "usenet",
		Size: sql.NullInt64{Int64: 1 << 30, Valid: true}, Published: sql.NullTime{Time: release.PublishDate.Add(time.Minute), Valid: true}}})

	d := e.Series(breakingBad(), SeriesScope{}, []newznab.Release{release})[0]
	wantRejected(t, d, "Release is blocklisted")

	reposted := release
	reposted.PublishDate = release.PublishDate.Add(-time.Hour)
	if d := e.Series(breakingBad(), SeriesScope{}, []newznab.Release{reposted})[0]; hasReason(d, "Release is blocklisted") {
		t.Errorf("want a repost with a different publish date not blocklisted, got %q", reasons(d))
	}
	resized := release
	resized.Size++
	if d := e.Series(breakingBad(), SeriesScope{}, []newznab.Release{resized})[0]; hasReason(d, "Release is blocklisted") {
		t.Errorf("want a different size not blocklisted, got %q", reasons(d))
	}
}

func TestBlocklist_Album(t *testing.T) {
	e := engine()
	a := store.WantedAlbum{ID: 9, Artist: "Daft Punk", Title: "Homework", Monitored: true, ReleaseDate: date(1997, 1, 20)}
	release := torrent("Daft Punk - Homework (1997) [FLAC]")
	e.SetBlocklist([]store.BlocklistEntry{{AlbumID: sql.NullInt64{Int64: 9, Valid: true}, SourceTitle: release.Title, Protocol: "torrent", Indexer: "Tracker"}})
	wantRejected(t, e.Album(a, []newznab.Release{release})[0], "Release is blocklisted")
}

func hasReason(d Decision, reason string) bool {
	for _, r := range d.Rejections {
		if r == reason {
			return true
		}
	}
	return false
}
