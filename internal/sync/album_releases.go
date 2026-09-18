package sync

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/musicbrainz"
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
	MBID  string
	Title string
	// Disambiguation is what MusicBrainz calls this pressing apart from the
	// others: "Australian version", "20th anniversary edition".
	Disambiguation string
	Status         string
	Country        string
	Date           string
	Format         string
	TrackCount     int
	// Current is the release the album tracks now.
	Current bool
	// Tagged means the album's own files name this release.
	Tagged bool
	// Files is how many of the album's files name it.
	Files int
}

// ReleaseChoicesReport is the picker: the releases of this album, and
// whether the files think they belong to a different album altogether.
type ReleaseChoicesReport struct {
	Choices []ReleaseChoice
	// Stray is how many of the album's files name a release that is not one
	// of these - which means the album itself is matched to the wrong
	// release group, not that the wrong pressing was chosen.
	Stray int
}

// ReleaseChoices lists a release group's releases, marking the one in use
// and the one the files claim.
func (s *MusicService) ReleaseChoices(ctx context.Context, albumID int64) ([]ReleaseChoice, error) {
	report, err := s.ReleaseChoicesReport(ctx, albumID)
	return report.Choices, err
}

// ReleaseChoicesReport is ReleaseChoices plus what the files say.
func (s *MusicService) ReleaseChoicesReport(ctx context.Context, albumID int64) (ReleaseChoicesReport, error) {
	choices, stray, err := s.releaseChoices(ctx, albumID)
	return ReleaseChoicesReport{Choices: choices, Stray: stray}, err
}

func (s *MusicService) releaseChoices(ctx context.Context, albumID int64) ([]ReleaseChoice, int, error) {
	groupMBID, found, err := store.GetExternalID(ctx, s.DB, "album", albumID, "musicbrainz")
	if err != nil {
		return nil, 0, err
	}
	if !found {
		return nil, 0, fmt.Errorf("album %d has no MusicBrainz id", albumID)
	}
	refs, err := s.MusicBrainz.ListReleases(ctx, groupMBID)
	if err != nil {
		return nil, 0, fmt.Errorf("list releases of %s: %w", groupMBID, err)
	}
	current, _ := store.CurrentReleaseMBID(ctx, s.DB, albumID)
	tagged := s.taggedReleaseCounts(ctx, albumID)

	// Order the list the same way adding an album picks one, so what the
	// dialog recommends and what UMMarr would have chosen agree.
	var hint ReleaseHint
	var countries []string
	if folder, err := store.AlbumFolderPath(ctx, s.DB, albumID); err == nil {
		hint = s.FolderReleaseHint(ctx, folder)
	}
	if ms, err := store.GetMediaSettings(ctx, s.DB); err == nil {
		countries = ms.ReleaseCountries()
	}

	choices := make([]ReleaseChoice, 0, len(refs))
	for _, ref := range refs {
		choice := ReleaseChoice{
			MBID: ref.ID, Title: ref.Title, Disambiguation: ref.Disambiguation, Status: ref.Status, Country: ref.Country,
			Date: ref.Date, Format: ref.Format(), TrackCount: ref.TrackCount(),
			Current: ref.ID == current,
		}
		if n := tagged[ref.ID]; n > 0 {
			choice.Tagged, choice.Files = true, n
		}
		choices = append(choices, choice)
	}
	// Files naming a release this group does not contain mean the album is
	// matched to the wrong release group entirely - High Voltage exists as
	// both a 1975 Australian album and a 1976 international one.
	inGroup := make(map[string]bool, len(refs))
	for _, ref := range refs {
		inGroup[ref.ID] = true
	}
	stray := 0
	for id, n := range tagged {
		if !inGroup[id] {
			stray += n
		}
	}

	ranks := make(map[string][4]int, len(refs))
	for _, ref := range refs {
		ranks[ref.ID] = rankRelease(ref, hint, countries)
	}
	sort.SliceStable(choices, func(i, j int) bool {
		a, b := ranks[choices[i].MBID], ranks[choices[j].MBID]
		if a != b {
			return lessRank(a, b)
		}
		if choices[i].Current != choices[j].Current {
			return choices[i].Current
		}
		return choices[i].Date < choices[j].Date
	})
	return choices, stray, nil
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
	releaseID, err := s.syncRelease(ctx, albumID, releaseMBID)
	if err != nil {
		return err
	}
	// The files have to come across before the tidy-up, or there is
	// nothing to come across to: a release nothing is attached to counts
	// as unfiled, so dropping the old releases first would drop the one
	// just chosen and leave the album exactly where it started.
	if _, err := s.carryFilesToRelease(ctx, albumID, releaseID); err != nil {
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

// carryFilesToRelease moves an album's files onto the tracks of the
// release just chosen, and reports how many made it.
//
// A file is placed by the MusicBrainz track id it carries, then by the
// disc and track number it claims, and failing both by where it sat on
// the release being left behind - the same order the scanner uses, with
// the old position standing in for tags a file doesn't have. What nothing
// places is left attached to nothing, which is what Manage Track Files
// lists as waiting to be matched: a 16-track Japanese edition has three
// tracks a UK CD simply does not, and inventing a home for them would be
// worse than saying so.
func (s *MusicService) carryFilesToRelease(ctx context.Context, albumID, releaseID int64) (int, error) {
	files, err := store.AlbumAttachedFiles(ctx, s.DB, albumID)
	if err != nil {
		return 0, err
	}
	tracks, err := store.ReleaseTracks(ctx, s.DB, releaseID)
	if err != nil {
		return 0, err
	}
	index := newTrackIndex(tracks)

	type move struct {
		fileID  int64
		trackID int64
	}
	var moves []move
	var stranded []int64
	taken := map[int64]bool{}
	for _, f := range files {
		if f.ReleaseID == releaseID {
			// Already where it belongs, which is the ordinary case when
			// the chosen release is the one in use.
			taken[f.TrackID] = true
			continue
		}
		track, how := store.TrackImportInfo{}, ""
		if f.Tags != nil {
			track, how = index.lookup(*f.Tags)
		}
		if how == "" {
			// No usable tags: the disc and track it sat on before is the
			// best thing left to go on.
			if n, err := strconv.Atoi(strings.TrimSpace(f.Number)); err == nil {
				track, how = index.byNumbers[[2]int{medium(f.Medium), n}], MatchNumbers
			}
		}
		if track.ID == 0 || taken[track.ID] {
			// Nowhere to go on the chosen release. The row goes, so the
			// release being left behind is left holding nothing and can
			// be dropped; the file on disk is untouched and shows up as
			// waiting to be matched.
			stranded = append(stranded, f.FileID)
			continue
		}
		taken[track.ID] = true
		moves = append(moves, move{fileID: f.FileID, trackID: track.ID})
	}
	if len(moves) == 0 && len(stranded) == 0 {
		return 0, nil
	}

	err = store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		// Detach everything first, so a file landing on a track another
		// file is leaving doesn't collide with it.
		for _, m := range moves {
			if _, err := tx.ExecContext(ctx, `UPDATE tracks SET track_file_id = NULL WHERE track_file_id = ?`, m.fileID); err != nil {
				return fmt.Errorf("detach file %d: %w", m.fileID, err)
			}
		}
		for _, m := range moves {
			if _, err := tx.ExecContext(ctx, `UPDATE tracks SET track_file_id = ? WHERE id = ? AND track_file_id IS NULL`, m.fileID, m.trackID); err != nil {
				return fmt.Errorf("attach file %d: %w", m.fileID, err)
			}
		}
		for _, id := range stranded {
			if _, err := tx.ExecContext(ctx, `UPDATE tracks SET track_file_id = NULL WHERE track_file_id = ?`, id); err != nil {
				return fmt.Errorf("detach file %d: %w", id, err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM track_files WHERE id = ?`, id); err != nil {
				return fmt.Errorf("drop file row %d: %w", id, err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(moves), nil
}

// ReleaseHint is what an album's own folder says about which release it is,
// used when adding an album from a library scan.
type ReleaseHint struct {
	// ReleaseMBID is the release the files name outright
	// (MUSICBRAINZ_ALBUMID). Nothing beats it.
	ReleaseMBID string
	// HighestTrack is the largest track number seen. Deliberately not the
	// number of files: a rip missing tracks 4, 7 and 8 still has a track
	// numbered 13, and it is a 13-track release, not a 10-track one.
	HighestTrack int
	// Files is how many audio files the folder holds, the fallback when
	// nothing is tagged.
	Files int
}

// FolderReleaseHint reads what a folder's files say about their release.
func (s *MusicService) FolderReleaseHint(ctx context.Context, dir string) ReleaseHint {
	var hint ReleaseHint
	files, err := importer.ScanDirectory(dir)
	if err != nil {
		return hint
	}
	audio := importer.AudioFilesSorted(files)
	hint.Files = len(audio)
	if s.Probe == nil {
		return hint
	}
	releases := map[string]int{}
	for _, f := range audio {
		info, err := s.Probe(ctx, filepath.Join(dir, f.Path))
		if err != nil || info.Tags == nil {
			continue
		}
		if n := info.Tags.TrackNumber; n > hint.HighestTrack {
			hint.HighestTrack = n
		}
		if id := strings.TrimSpace(info.Tags.ReleaseMBID); id != "" {
			releases[id]++
		}
	}
	best := 0
	for id, n := range releases {
		if n > best {
			hint.ReleaseMBID, best = id, n
		}
	}
	return hint
}

// wantedTracks is the track count a release should have to match the
// folder: the highest track number when the files are numbered, otherwise
// how many there are.
func (h ReleaseHint) wantedTracks() int {
	if h.HighestTrack > 0 {
		return h.HighestTrack
	}
	return h.Files
}

// rankRelease scores one release against the hint and the preferred
// countries, lower being better, so sorting puts the best first.
func rankRelease(ref musicbrainz.ReleaseRef, hint ReleaseHint, countries []string) [4]int {
	var rank [4]int
	// 1. The release the files name outright.
	if hint.ReleaseMBID != "" && strings.EqualFold(ref.ID, hint.ReleaseMBID) {
		return rank // all zeroes: nothing sorts above this
	}
	rank[0] = 1
	// 2. The track count the folder implies.
	if want := hint.wantedTracks(); want > 0 && ref.TrackCount() == want {
		rank[1] = 0
	} else {
		rank[1] = 1
	}
	// 3. Country order, unlisted countries last.
	rank[2] = len(countries)
	for i, c := range countries {
		if strings.EqualFold(ref.Country, c) {
			rank[2] = i
			break
		}
	}
	// 4. Official over promo, bootleg and withdrawn.
	if !strings.EqualFold(ref.Status, "Official") {
		rank[3] = 1
	}
	return rank
}

func lessRank(a, b [4]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// PickRelease chooses which release of a group to track: the one the files
// name, then one with as many tracks as the folder implies, then the
// preferred countries in order, then an Official one - and the earliest
// date to break a tie.
func PickRelease(refs []musicbrainz.ReleaseRef, hint ReleaseHint, countries []string) *musicbrainz.ReleaseRef {
	if len(refs) == 0 {
		return nil
	}
	ordered := make([]musicbrainz.ReleaseRef, len(refs))
	copy(ordered, refs)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := rankRelease(ordered[i], hint, countries), rankRelease(ordered[j], hint, countries)
		if a != b {
			return lessRank(a, b)
		}
		return ordered[i].Date < ordered[j].Date
	})
	return &ordered[0]
}
