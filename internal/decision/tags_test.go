package decision

import (
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// An indexer tagged "anime" is only for anime-tagged items; an untagged
// indexer is for everything; and a tag id UMMarr doesn't know (one synced
// from Prowlarr) restricts nothing.
func TestTagRestrictedIndexers(t *testing.T) {
	e := engine()
	e.TagLabels = map[int64]string{5: "anime"}
	e.Indexers[2] = store.Indexer{ID: 2, Name: "Nyaa", Tags: []int{5}}
	e.Indexers[3] = store.Indexer{ID: 3, Name: "Synced", Tags: []int{99}}

	open := torrent("Inception 2010 1080p BluRay open")
	restricted := torrent("Inception 2010 1080p BluRay restricted")
	restricted.IndexerID = 2
	unknownTag := torrent("Inception 2010 1080p BluRay unknown tag")
	unknownTag.IndexerID = 3

	m := inception()
	got := map[string]Decision{}
	for _, d := range e.Movie(m, []newznab.Release{open, restricted, unknownTag}) {
		got[d.Release.GUID] = d
	}
	wantApproved(t, got[open.GUID])
	wantRejected(t, got[restricted.GUID], "Nyaa is only used for items tagged anime")
	wantApproved(t, got[unknownTag.GUID])

	m.Tags = []int64{5}
	wantApproved(t, e.Movie(m, []newznab.Release{restricted})[0])
}
