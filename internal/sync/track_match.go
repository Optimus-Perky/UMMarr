package sync

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Matching audio files to tracks.
//
// Until now this was purely positional: the folder's files sorted by name,
// zipped against the release's tracks in order. That is the only thing
// possible for a bare download, but it breaks on a real library - one
// missing track shifts every file after it onto the wrong title, and a
// folder holding two editions of the album is hopeless.
//
// A tagged file doesn't need guessing. MusicBrainz Picard writes the track's
// own id into the file (MUSICBRAINZ_RELEASETRACKID), and nearly every file
// carries at least a disc and track number. So: match by id, then by the
// disc/track the file claims, and only fall back to position where that is
// the best available answer - a fresh download of a known release.

// MatchHow says how a file was paired with its track, for the message the
// scan or import reports.
const (
	MatchMBID     = "musicbrainz" // the file's own MusicBrainz track id
	MatchNumbers  = "tags"        // the disc and track number it claims
	MatchPosition = "position"    // its place in the folder
)

// TrackMatch is one audio file paired with the track it belongs to.
type TrackMatch struct {
	File  importer.File
	Track store.TrackImportInfo
	How   string
}

// MatchReport counts how a folder's files were matched.
type MatchReport struct {
	ByMBID     int
	ByNumbers  int
	ByPosition int
	// Unmatched are files nothing could identify. They are left where they
	// are rather than attached to a guess.
	Unmatched []importer.File
}

// Summary is the report in one line, or "" when there is nothing to say.
func (r MatchReport) Summary() string {
	var parts []string
	if r.ByMBID > 0 {
		parts = append(parts, strconv.Itoa(r.ByMBID)+" by MusicBrainz id")
	}
	if r.ByNumbers > 0 {
		parts = append(parts, strconv.Itoa(r.ByNumbers)+" by disc/track tags")
	}
	if r.ByPosition > 0 {
		parts = append(parts, strconv.Itoa(r.ByPosition)+" by position")
	}
	if n := len(r.Unmatched); n > 0 {
		parts = append(parts, strconv.Itoa(n)+" left for manual matching")
	}
	return strings.Join(parts, ", ")
}

// fileTags reads one file's tags. A file that can't be probed simply has
// none, which sends it down the next matching rule.
func (s *ImportService) fileTags(ctx context.Context, path string) mediainfo.AudioTags {
	if s.Probe == nil {
		return mediainfo.AudioTags{}
	}
	info, err := s.Probe(ctx, path)
	if err != nil || info.Tags == nil {
		return mediainfo.AudioTags{}
	}
	return *info.Tags
}

// trackNumber reads a track's stored number, which is text because
// MusicBrainz numbers can be "A1" on a vinyl.
func trackNumber(t store.TrackImportInfo) int {
	n, err := strconv.Atoi(strings.TrimSpace(t.Number))
	if err != nil {
		return 0
	}
	return n
}

// MatchTracks pairs audio files with a release's tracks. allowPosition is
// for a download of a known release, where the files arrive in order and
// position is a reasonable last resort; a library scan passes false, so a
// file nothing can identify is left alone and reported instead of being
// attached to whichever track happened to line up.
func (s *ImportService) MatchTracks(ctx context.Context, dir string, files []importer.File, tracks []store.TrackImportInfo, allowPosition bool) ([]TrackMatch, MatchReport) {
	var report MatchReport
	var matches []TrackMatch

	// With no way to read tags there is no information to be had, so
	// refusing to match would just stop importing music altogether. Fall
	// back to how UMMarr worked before tags were read.
	if s.Probe == nil {
		allowPosition = true
	}

	taken := make(map[int64]bool, len(tracks))
	byMBID := map[string]store.TrackImportInfo{}
	byNumbers := map[[2]int]store.TrackImportInfo{}
	for _, t := range tracks {
		if t.MBID != "" {
			byMBID[strings.ToLower(t.MBID)] = t
		}
		if n := trackNumber(t); n > 0 {
			medium := t.Medium
			if medium == 0 {
				medium = 1
			}
			byNumbers[[2]int{medium, n}] = t
		}
	}

	claim := func(file importer.File, track store.TrackImportInfo, how string) bool {
		if taken[track.ID] {
			return false
		}
		taken[track.ID] = true
		matches = append(matches, TrackMatch{File: file, Track: track, How: how})
		return true
	}

	var leftover []importer.File
	for _, file := range files {
		tags := s.fileTags(ctx, filepath.Join(dir, file.Path))
		if id := strings.ToLower(tags.TrackMBID); id != "" {
			if track, ok := byMBID[id]; ok && claim(file, track, MatchMBID) {
				report.ByMBID++
				continue
			}
		}
		if tags.TrackNumber > 0 {
			medium := tags.DiscNumber
			if medium == 0 {
				medium = 1
			}
			if track, ok := byNumbers[[2]int{medium, tags.TrackNumber}]; ok && claim(file, track, MatchNumbers) {
				report.ByNumbers++
				continue
			}
		}
		leftover = append(leftover, file)
	}

	if !allowPosition {
		report.Unmatched = leftover
		return matches, report
	}
	// Whatever is left goes onto the tracks nothing claimed, in order.
	var free []store.TrackImportInfo
	for _, t := range tracks {
		if !taken[t.ID] {
			free = append(free, t)
		}
	}
	for i, file := range leftover {
		if i >= len(free) {
			report.Unmatched = append(report.Unmatched, leftover[i:]...)
			break
		}
		claim(file, free[i], MatchPosition)
		report.ByPosition++
	}
	return matches, report
}

// UnmatchedAlbumFiles lists audio files sitting in an album's folder that
// no track claims - what a scan left alone because nothing identified them.
// They are what Manage Track Files offers to attach by hand.
func (s *ImportService) UnmatchedAlbumFiles(ctx context.Context, albumID int64) ([]importer.File, error) {
	folder, err := store.AlbumFolderPath(ctx, s.DB, albumID)
	if err != nil {
		return nil, err
	}
	files, err := importer.ScanDirectory(folder)
	if err != nil {
		return nil, err
	}
	attached := map[string]bool{}
	refs, err := store.ListTrackFilesForAlbum(ctx, s.DB, albumID)
	if err != nil {
		return nil, err
	}
	for _, ref := range refs {
		attached[filepath.Clean(ref.RelativePath)] = true
	}
	var out []importer.File
	for _, file := range importer.AudioFilesSorted(files) {
		if !attached[filepath.Clean(file.Path)] {
			out = append(out, file)
		}
	}
	return out, nil
}

// AttachAlbumFile points one of those files at a track, leaving it where it
// is on disk. The counterpart of RemapTrackFile for a file the library
// doesn't know about yet.
func (s *ImportService) AttachAlbumFile(ctx context.Context, albumID, trackID int64, relativePath string) error {
	candidates, err := s.UnmatchedAlbumFiles(ctx, albumID)
	if err != nil {
		return err
	}
	var file *importer.File
	for i := range candidates {
		if filepath.Clean(candidates[i].Path) == filepath.Clean(relativePath) {
			file = &candidates[i]
			break
		}
	}
	if file == nil {
		return fmt.Errorf("%s isn't an unmatched file of this album", relativePath)
	}
	if _, err := store.FindTrackOnAlbum(ctx, s.DB, albumID, trackID); err != nil {
		return err
	}
	var trackFileID int64
	if err := store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		var err error
		trackFileID, err = store.AttachTrackFile(ctx, tx, trackID, file.Path, file.Size)
		return err
	}); err != nil {
		return err
	}
	_ = store.UpdateTrackFileQuality(ctx, s.DB, trackFileID, s.trackFileQuality(ctx, trackID, file.Path))
	e := albumEvent(ctx, s.DB, albumID, store.EventImported)
	e.Detail, e.Source = file.Path+" matched by hand", "manage track files"
	s.Events.Record(ctx, e)
	return nil
}
