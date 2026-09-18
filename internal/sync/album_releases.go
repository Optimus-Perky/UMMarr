package sync

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Choosing which release (pressing/edition) of an album the library tracks.
//
// A release group is the album; a release is one issue of it, and they
// differ - the UK CD of Avril Lavigne has 13 tracks, the Japanese one 16.
// UMMarr used to take the first release MusicBrainz called Official, which
// is arbitrary: for that album it picked the Taiwanese 16-track pressing
// and left three tracks permanently missing.
//
// The files usually know better than any heuristic. Picard writes the
// release's own id into every file (MUSICBRAINZ_ALBUMID), so the picker
// marks the release the files themselves name.

// ReleaseChoice is one release of an album, as the picker lists it.
type ReleaseChoice struct {
	MBID       string
	Title      string
	Status     string
	Country    string
	Date       string
	Format     string
	TrackCount int
	// Current is the release the album tracks now.
	Current bool
	// Tagged means the album's own files name this release.
	Tagged bool
	// Files is how many of the album's files name it.
	Files int
}

// ReleaseChoices lists a release group's releases, marking the one in use
// and the one the files claim.
func (s *MusicService) ReleaseChoices(ctx context.Context, albumID int64) ([]ReleaseChoice, error) {
	groupMBID, found, err := store.GetExternalID(ctx, s.DB, "album", albumID, "musicbrainz")
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("album %d has no MusicBrainz id", albumID)
	}
	refs, err := s.MusicBrainz.ListReleases(ctx, groupMBID)
	if err != nil {
		return nil, fmt.Errorf("list releases of %s: %w", groupMBID, err)
	}
	current, _ := store.CurrentReleaseMBID(ctx, s.DB, albumID)
	tagged := s.taggedReleaseCounts(ctx, albumID)

	choices := make([]ReleaseChoice, 0, len(refs))
	for _, ref := range refs {
		choice := ReleaseChoice{
			MBID: ref.ID, Title: ref.Title, Status: ref.Status, Country: ref.Country,
			Date: ref.Date, Format: ref.Format(), TrackCount: ref.TrackCount(),
			Current: ref.ID == current,
		}
		if n := tagged[ref.ID]; n > 0 {
			choice.Tagged, choice.Files = true, n
		}
		choices = append(choices, choice)
	}
	// The one the files name first, then the one in use, then by date.
	sort.SliceStable(choices, func(i, j int) bool {
		if choices[i].Files != choices[j].Files {
			return choices[i].Files > choices[j].Files
		}
		if choices[i].Current != choices[j].Current {
			return choices[i].Current
		}
		return choices[i].Date < choices[j].Date
	})
	return choices, nil
}

// taggedReleaseCounts counts which release the album's files say they are.
func (s *MusicService) taggedReleaseCounts(ctx context.Context, albumID int64) map[string]int {
	counts := map[string]int{}
	if s.Probe == nil {
		return counts
	}
	folder, err := store.AlbumFolderPath(ctx, s.DB, albumID)
	if err != nil {
		return counts
	}
	files, err := store.ListTrackFileDetails(ctx, s.DB, albumID)
	if err != nil {
		return counts
	}
	for _, f := range files {
		info, err := s.Probe(ctx, filepath.Join(folder, f.RelativePath))
		if err != nil || info.Tags == nil {
			continue
		}
		if id := strings.TrimSpace(info.Tags.ReleaseMBID); id != "" {
			counts[id]++
		}
	}
	return counts
}

// ChooseRelease points an album at a different release: its tracks are
// synced, the files are re-matched against them from their own tags, and
// any other release with no files left is dropped. Files are never
// detached - a file whose track disappears keeps its row until something
// claims it, and shows up in Manage Track Files to be matched by hand.
func (s *MusicService) ChooseRelease(ctx context.Context, albumID int64, releaseMBID string) error {
	if strings.TrimSpace(releaseMBID) == "" {
		return fmt.Errorf("pick a release")
	}
	if err := s.syncRelease(ctx, albumID, releaseMBID); err != nil {
		return err
	}
	if err := store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		return store.DeleteUnfiledAlbumReleases(ctx, tx, albumID)
	}); err != nil {
		return err
	}
	if err := store.SetAlbumPath(ctx, s.DB, albumID); err != nil {
		return err
	}
	return nil
}
