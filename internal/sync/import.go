package sync

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	gosync "sync"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/nfo"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// ImportService copies a finished download's files into the library and
// records them, renaming each to its configured naming template. Takes
// plain []importer.File, not a Deluge type - it's fed either from
// Deluge's own torrent file listing (the automatic polling/webhook path)
// or from a live filesystem scan (the manual "Check again" retry path,
// see DownloadService.RetryImport), and shouldn't care which. Not the
// Add/Refresh upsert shape - an import attempt is a one-shot action, same
// reasoning as DownloadService's grab methods.
type ImportService struct {
	DB *sql.DB
	// Movies adds the movies a library import finds. Nil disables it.
	Movies *MovieService
	Series *SeriesService
	Music  *MusicService
	// Events records imports, upgrades, renames and deletes (nil records nothing).
	Events *Events
	// Metadata writes Kodi/Emby .nfo files and images beside imported files
	// (nil writes nothing).
	Metadata *nfo.Writer
	// Probe reads a file with FFprobe, so a music file can be matched on
	// the MusicBrainz ids and disc/track numbers it carries rather than on
	// its position in the folder (see track_match.go). Nil falls back to
	// position, as UMMarr did before tags were read.
	Probe func(context.Context, string) (mediainfo.Info, error)

	mu      gosync.Mutex
	runMu   gosync.Mutex             // one library import at a time
	imports map[int64]*LibraryImport // by root folder id
}

const archiveNeedsExtractionMessage = "downloaded file is an archive - extract it, then click Check again"

// importOptions turns the saved media settings into how a download is brought
// into the library.
func importOptions(ms store.MediaSettings) importer.ImportOptions {
	return importer.ImportOptions{
		Permissions:      permissions(ms),
		UseHardlinks:     ms.UseHardlinks,
		CheckFreeSpace:   !ms.SkipFreeSpaceCheck,
		MinimumFreeBytes: ms.MinimumFreeSpaceMB * 1024 * 1024,
	}
}

// permissions is Set Permissions from the media settings. A mode or group that
// doesn't parse (both are checked when saved) falls back to mirroring rather
// than blocking an import.
func permissions(ms store.MediaSettings) importer.Permissions {
	var p importer.Permissions
	if !ms.SetPermissions {
		return p
	}
	if mode, err := importer.ParseFolderMode(ms.ChmodFolder); err == nil {
		p.Explicit, p.FolderMode = true, mode
	}
	if gid, err := strconv.Atoi(strings.TrimSpace(ms.ChownGroup)); err == nil {
		p.SetGroup, p.Group = true, gid
	}
	return p
}

func importedMessage(name string, hardlinked bool) string {
	if hardlinked {
		return fmt.Sprintf("imported %s (hardlinked)", name)
	}
	return fmt.Sprintf("imported %s", name)
}

// File dates are cosmetic, so a lookup or chtimes failure never fails an import.

func (s *ImportService) dateMovieFile(ctx context.Context, ms store.MediaSettings, movieID int64, path string) {
	if date, ok, err := store.MovieFileDate(ctx, s.DB, movieID, ms.MovieFileDate); err == nil && ok {
		_ = os.Chtimes(path, date, date)
	}
}

func (s *ImportService) dateEpisodeFile(ctx context.Context, ms store.MediaSettings, episodeID int64, path string) {
	if date, ok, err := store.EpisodeFileDate(ctx, s.DB, episodeID, ms.EpisodeFileDate); err == nil && ok {
		_ = os.Chtimes(path, date, date)
	}
}

func (s *ImportService) dateTrackFile(ctx context.Context, ms store.MediaSettings, trackID int64, path string) {
	if date, ok, err := store.TrackFileDate(ctx, s.DB, trackID, ms.TrackFileDate); err == nil && ok {
		_ = os.Chtimes(path, date, date)
	}
}

type extrasMode int

const (
	extrasRenameAny      extrasMode = iota // movies: any matching file, named after the movie
	extrasRenameMatching                   // episodes: only files named after that episode's download
	extrasKeepNames                        // albums: any matching file, own name kept
)

// importExtras brings a download's extra files (subtitles and the like, per
// the saved extensions) into destFolder beside a main file just imported as
// mainName. An episode only takes files named after its own download file, so
// one episode's subtitles don't land beside another. Best-effort: a missing
// subtitle never fails the import.
func importExtras(ms store.MediaSettings, files []importer.File, main importer.File, savePath, destFolder, mainName string, mode extrasMode) {
	if !ms.ImportExtraFiles {
		return
	}
	exts := importer.ParseExtensions(ms.ExtraFileExtensions)
	opts := importOptions(ms)
	for _, f := range files {
		if f.Path == main.Path || !importer.IsExtraFile(f.Path, exts) {
			continue
		}
		var name string
		switch mode {
		case extrasKeepNames:
			name = importer.KeptExtraName(f.Path)
		case extrasRenameMatching:
			if !importer.BelongsTo(f.Path, main.Path) {
				continue
			}
			name = importer.ExtraName(f.Path, main.Path, mainName)
		default:
			name = importer.ExtraName(f.Path, main.Path, mainName)
		}
		_, _ = importer.ImportFile(filepath.Join(savePath, f.Path), filepath.Join(destFolder, name), opts)
	}
}

// Import attempts to import grab's finished download into the library,
// returning the grab status/message the caller should persist via
// store.UpdateGrabStatus - this method never touches the grabs table
// itself. Returns no Go error: a failed match/parse (or an archive-only
// download needing manual extraction) is a normal, persistable outcome,
// not a caller-level failure.

// importMovie imports the single largest video file in the download as
// movieID's file - the largest-file heuristic naturally excludes
// samples/extras without parsing any filenames.
func (s *ImportService) importMovie(ctx context.Context, movieID int64, files []importer.File, savePath string) (string, string) {
	file, ok := importer.LargestVideoFile(files)
	if !ok {
		if importer.HasArchiveFile(files) {
			return "needs_extraction", archiveNeedsExtractionMessage
		}
		return "import_failed", "no video file found in download"
	}
	_, status, message := s.importMovieFile(ctx, movieID, file, files, savePath)
	return status, message
}

// importMovieFile imports one chosen video file for a movie, bringing along
// any extras among files, and returns the new movie_files row.
func (s *ImportService) importMovieFile(ctx context.Context, movieID int64, file importer.File, files []importer.File, savePath string) (fileID int64, status, message string) {
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return 0, "import_failed", fmt.Sprintf("load media settings: %v", err)
	}
	folder, err := store.MovieFolderPath(ctx, s.DB, movieID)
	if err != nil {
		return 0, "import_failed", fmt.Sprintf("resolve movie path: %v", err)
	}
	name, err := store.ResolveMovieFileName(ctx, s.DB, movieID, file.Path)
	if err != nil {
		return 0, "import_failed", fmt.Sprintf("resolve movie file name: %v", err)
	}
	dest := filepath.Join(folder, name)
	s.recycleExisting(ms, dest)
	hardlinked, err := importer.ImportFile(filepath.Join(savePath, file.Path), dest, importOptions(ms))
	if err != nil {
		return 0, "import_failed", fmt.Sprintf("import file: %v", err)
	}

	fileID, err = store.InsertMovieFile(ctx, s.DB, movieID, name, file.Size)
	if err != nil {
		return 0, "import_failed", fmt.Sprintf("record movie file: %v", err)
	}
	s.replaceMovieFiles(ctx, ms, movieID, folder, fileID, dest)
	_ = store.UpdateMovieFileQuality(ctx, s.DB, fileID, s.movieFileQuality(ctx, movieID, file.Path))
	s.dateMovieFile(ctx, ms, movieID, dest)
	importExtras(ms, files, file, savePath, folder, name, extrasRenameAny)
	s.writeMetadata(func() error { return s.Metadata.WriteMovie(ctx, movieID) })
	return fileID, "imported", importedMessage(name, hardlinked)
}

// importSeries imports every recognizable video file in a season-pack or
// whole-series download, matching each to its episode by parsing
// season/episode straight out of that file's own relative path (see
// internal/importer.ParseEpisode) - required for whole-series grabs
// (grab.SeasonNumber NULL, no single season to assume) but applied
// uniformly to season-pack grabs too, since it's no less correct there
// and keeps this one code path. Files that don't parse, or parse to an
// episode this series doesn't have, are skipped rather than failing the
// whole import; the message reports how many of the total video files
// were actually imported.
func (s *ImportService) importSeries(ctx context.Context, seriesID int64, files []importer.File, savePath string) (string, string) {
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return "import_failed", fmt.Sprintf("load media settings: %v", err)
	}
	seriesPath, err := store.SeriesFolderPath(ctx, s.DB, seriesID)
	if err != nil {
		return "import_failed", fmt.Sprintf("resolve series path: %v", err)
	}
	opts := importOptions(ms)

	imported, total := 0, 0
	// Every skip below reports the same "matched 0 of N" outcome, which
	// reads as a naming problem even when the real cause was something
	// else entirely (an unreadable download directory, say). Keeping the
	// last reason makes the difference visible on the Activity page.
	skipReason := ""
	for _, file := range files {
		if !importer.IsVideoFile(file.Path) {
			continue
		}
		total++

		season, episodes, ok := importer.ParseEpisodes(file.Path)
		if !ok {
			skipReason = fmt.Sprintf("no season/episode in %q", filepath.Base(file.Path))
			continue
		}
		fileIDs, reason := s.importEpisodeFile(ctx, ms, opts, seriesID, seriesPath, season, episodes, file, files, savePath)
		if len(fileIDs) == 0 {
			skipReason = reason
			continue
		}
		imported++
	}

	if total == 0 {
		if importer.HasArchiveFile(files) {
			return "needs_extraction", archiveNeedsExtractionMessage
		}
		return "import_failed", "no video files found in download"
	}
	if imported > 0 {
		s.writeMetadata(func() error { return s.Metadata.WriteSeries(ctx, seriesID) })
	}
	if imported == 0 {
		if skipReason != "" {
			return "import_failed", fmt.Sprintf("matched 0 of %d video files to an episode - %s", total, skipReason)
		}
		return "import_failed", fmt.Sprintf("matched 0 of %d video files to an episode", total)
	}
	if imported < total {
		return "imported", fmt.Sprintf("imported %d of %d video files (%d unmatched)", imported, total, total-imported)
	}
	return "imported", fmt.Sprintf("imported %d video file(s)", imported)
}

// importAlbum imports a positional match of this download's audio files
// against the tracks of whichever album_releases row FindImportRelease
// resolves to (see its doc comment for the release-ambiguity heuristic).
// Positional, not name-parsed, because track filenames in the wild have
// no single reliable numbering convention the way S01E02 does for
// episodes - files sorted naturally by path (matching how a release's own
// track files are usually already numbered/ordered, e.g. "01 -
// Track.flac", "02 - Track.flac") zipped against the DB's track order is
// the same heuristic Lidarr itself falls back on.
func (s *ImportService) importAlbum(ctx context.Context, albumID int64, files []importer.File, savePath string) (string, string) {
	audioFiles := importer.AudioFilesSorted(files)
	if len(audioFiles) == 0 {
		if importer.HasArchiveFile(files) {
			return "needs_extraction", archiveNeedsExtractionMessage
		}
		return "import_failed", "no audio files found in download"
	}

	releaseID, tracks, err := store.FindImportRelease(ctx, s.DB, albumID)
	if err != nil {
		return "import_failed", fmt.Sprintf("find import release: %v", err)
	}
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return "import_failed", fmt.Sprintf("load media settings: %v", err)
	}
	folder, err := store.AlbumFolderPath(ctx, s.DB, albumID)
	if err != nil {
		return "import_failed", fmt.Sprintf("resolve album path: %v", err)
	}
	opts := importOptions(ms)

	// A download is a known release whose files usually arrive in order, so
	// position stays available as the last resort here (unlike a library
	// scan - see scanAlbumFolder).
	matches, matchReport := s.MatchTracks(ctx, savePath, audioFiles, tracks, true)

	imported := 0
	for _, match := range matches {
		file, track := match.File, match.Track

		name, err := store.ResolveTrackFileName(ctx, s.DB, track.ID, file.Path)
		if err != nil {
			continue
		}
		dest := filepath.Join(folder, name)
		s.recycleExisting(ms, dest)
		if _, err := importer.ImportFile(filepath.Join(savePath, file.Path), dest, opts); err != nil {
			continue
		}

		var trackFileID int64
		attachErr := store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
			var err error
			trackFileID, err = store.AttachTrackFile(ctx, tx, track.ID, name, file.Size)
			return err
		})
		if attachErr != nil {
			continue
		}
		_ = store.UpdateTrackFileQuality(ctx, s.DB, trackFileID, s.trackFileQuality(ctx, track.ID, file.Path))
		s.dateTrackFile(ctx, ms, track.ID, dest)
		imported++
	}

	if imported == 0 {
		return "import_failed", "failed to import any tracks"
	}
	importExtras(ms, files, audioFiles[0], savePath, folder, "", extrasKeepNames)
	message := fmt.Sprintf("imported %d track(s) from release %d", imported, releaseID)
	if summary := matchReport.Summary(); summary != "" {
		message += " (" + summary + ")"
	}
	s.writeMetadata(func() error { return s.Metadata.WriteAlbum(ctx, albumID) })
	return "imported", message
}

// importTrack imports the single largest audio file in the download as
// trackID's file - used only for a per-track grab (DownloadService.
// GrabTrack), where importAlbum's positional match against the WHOLE
// release's track list would misattribute a single downloaded file to
// whichever track happens to sort first, not the one actually requested.
func (s *ImportService) importTrack(ctx context.Context, trackID int64, files []importer.File, savePath string) (string, string) {
	file, ok := importer.LargestAudioFile(files)
	if !ok {
		if importer.HasArchiveFile(files) {
			return "needs_extraction", archiveNeedsExtractionMessage
		}
		return "import_failed", "no audio file found in download"
	}
	_, status, message := s.importTrackFile(ctx, trackID, file, files, savePath)
	return status, message
}

// importTrackFile imports one chosen audio file as a track, bringing along
// its extras among files, and returns the new track_files row.
func (s *ImportService) importTrackFile(ctx context.Context, trackID int64, file importer.File, files []importer.File, savePath string) (trackFileID int64, status, message string) {
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return 0, "import_failed", fmt.Sprintf("load media settings: %v", err)
	}
	albumID, err := store.GetAlbumIDForTrack(ctx, s.DB, trackID)
	if err != nil {
		return 0, "import_failed", fmt.Sprintf("resolve album for track: %v", err)
	}
	folder, err := store.AlbumFolderPath(ctx, s.DB, albumID)
	if err != nil {
		return 0, "import_failed", fmt.Sprintf("resolve album path: %v", err)
	}
	name, err := store.ResolveTrackFileName(ctx, s.DB, trackID, file.Path)
	if err != nil {
		return 0, "import_failed", fmt.Sprintf("resolve track file name: %v", err)
	}
	dest := filepath.Join(folder, name)
	s.recycleExisting(ms, dest)
	hardlinked, err := importer.ImportFile(filepath.Join(savePath, file.Path), dest, importOptions(ms))
	if err != nil {
		return 0, "import_failed", fmt.Sprintf("import file: %v", err)
	}

	attachErr := store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		var err error
		trackFileID, err = store.AttachTrackFile(ctx, tx, trackID, name, file.Size)
		return err
	})
	if attachErr != nil {
		return 0, "import_failed", fmt.Sprintf("record track file: %v", attachErr)
	}
	_ = store.UpdateTrackFileQuality(ctx, s.DB, trackFileID, s.trackFileQuality(ctx, trackID, file.Path))
	s.dateTrackFile(ctx, ms, trackID, dest)
	importExtras(ms, files, file, savePath, folder, name, extrasKeepNames)
	return trackFileID, "imported", importedMessage(name, hardlinked)
}

// pruneMissingFiles drops any of refs whose file is no longer on disk
// under folder, using del to remove the row, and returns the refs it dropped.
// A file deleted by hand outside UMMarr otherwise stays recorded forever,
// showing as Downloaded with nothing behind it - so every scan/refresh
// reconciles what is recorded against what is actually there before
// importing anything new.
func pruneMissingFiles(ctx context.Context, folder string, refs []store.FileRef, del func(context.Context, int64) error) (removed []store.FileRef) {
	for _, ref := range refs {
		if _, err := os.Stat(filepath.Join(folder, ref.RelativePath)); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			// Unreadable for some other reason (permissions, an offline
			// mount) - that is not evidence the file is gone, and
			// dropping the row on it would lose real library state.
			continue
		}
		if err := del(ctx, ref.ID); err == nil {
			removed = append(removed, ref)
		}
	}
	return removed
}

func (s *ImportService) pruneMissingMovieFiles(ctx context.Context, m store.MovieSummary) []store.FileRef {
	refs, err := store.ListMovieFilesForMovie(ctx, s.DB, m.ID)
	if err != nil {
		return nil
	}
	return pruneMissingFiles(ctx, m.Path.String, refs, func(ctx context.Context, id int64) error {
		return store.DeleteMovieFile(ctx, s.DB, id)
	})
}

func (s *ImportService) pruneMissingEpisodeFiles(ctx context.Context, series store.SeriesSummary) []store.FileRef {
	refs, err := store.ListEpisodeFilesForSeries(ctx, s.DB, series.ID)
	if err != nil {
		return nil
	}
	return pruneMissingFiles(ctx, series.Path.String, refs, func(ctx context.Context, id int64) error {
		return store.DeleteEpisodeFile(ctx, s.DB, id)
	})
}

func (s *ImportService) pruneMissingTrackFiles(ctx context.Context, albumID int64, albumPath string) []store.FileRef {
	refs, err := store.ListTrackFilesForAlbum(ctx, s.DB, albumID)
	if err != nil {
		return nil
	}
	return pruneMissingFiles(ctx, albumPath, refs, func(ctx context.Context, id int64) error {
		return store.DeleteTrackFile(ctx, s.DB, id)
	})
}

// isRootFolder guards Delete empty folders: an item whose folder template
// resolved to nothing lives directly in its root folder, which must never be
// removed. Treats the folder as a root when the root folders can't be read.
func (s *ImportService) isRootFolder(ctx context.Context, mediaType, folder string) bool {
	roots, err := store.ListRootFolders(ctx, s.DB, mediaType)
	if err != nil {
		return true
	}
	clean := filepath.Clean(folder)
	for _, r := range roots {
		if filepath.Clean(r.Path) == clean {
			return true
		}
	}
	return false
}

// tidyFolder applies Delete empty folders, then Create empty folders, to an
// item's own folder. Deleting first means that with both on, empty
// subfolders go but the item's folder itself is kept.
func (s *ImportService) tidyFolder(ctx context.Context, ms store.MediaSettings, mediaType, folder string) {
	if ms.DeleteEmptyFolders && !s.isRootFolder(ctx, mediaType, folder) {
		_, _ = importer.RemoveEmptyDirs(folder)
	}
	if ms.CreateEmptyFolders {
		_ = importer.EnsureDir(folder, permissions(ms))
	}
}

// reconcileMovie brings a movie's folder in line with what's recorded and with
// the media settings, before anything new is imported: it drops entries whose
// file is gone (unmonitoring the movie if asked to), removes or creates
// folders, and resets tracked files' dates. It returns how many entries it
// dropped.
func (s *ImportService) reconcileMovie(ctx context.Context, ms store.MediaSettings, m store.MovieSummary) int {
	if !m.Path.Valid {
		return 0
	}
	removed := s.pruneMissingMovieFiles(ctx, m)
	if len(removed) > 0 && ms.UnmonitorDeleted {
		_ = store.UpdateMovieMonitored(ctx, s.DB, m.ID, false)
	}
	s.tidyFolder(ctx, ms, "movie", m.Path.String)
	if refs, err := store.ListMovieFilesForMovie(ctx, s.DB, m.ID); err == nil {
		s.backfillQualities(ctx, refs, store.MovieFileQuality, s.movieFileQuality, store.UpdateMovieFileQuality)
	}
	if ms.MovieFileDate != store.FileDateNone {
		if refs, err := store.ListMovieFilesForMovie(ctx, s.DB, m.ID); err == nil {
			for _, ref := range refs {
				s.dateMovieFile(ctx, ms, m.ID, filepath.Join(m.Path.String, ref.RelativePath))
			}
		}
	}
	return len(removed)
}

// reconcileSeries is reconcileMovie for a series; Unmonitor deleted applies to
// each episode whose file went.
func (s *ImportService) reconcileSeries(ctx context.Context, ms store.MediaSettings, series store.SeriesSummary) {
	if !series.Path.Valid {
		return
	}
	removed := s.pruneMissingEpisodeFiles(ctx, series)
	if ms.UnmonitorDeleted {
		for _, ref := range removed {
			_ = store.UpdateEpisodeMonitored(ctx, s.DB, ref.OwnerID, false)
		}
	}
	s.tidyFolder(ctx, ms, "series", series.Path.String)
	if refs, err := store.ListEpisodeFilesForSeries(ctx, s.DB, series.ID); err == nil {
		s.backfillQualities(ctx, refs, store.EpisodeFileQuality, s.episodeFileQuality, store.UpdateEpisodeFileQuality)
	}
	if ms.EpisodeFileDate != store.FileDateNone {
		if refs, err := store.ListEpisodeFilesForSeries(ctx, s.DB, series.ID); err == nil {
			for _, ref := range refs {
				s.dateEpisodeFile(ctx, ms, ref.OwnerID, filepath.Join(series.Path.String, ref.RelativePath))
			}
		}
	}
}

// reconcileAlbum is reconcileMovie for an album; Unmonitor deleted applies once
// the album has no track files left.
func (s *ImportService) reconcileAlbum(ctx context.Context, ms store.MediaSettings, albumID int64, albumPath string) {
	removed := s.pruneMissingTrackFiles(ctx, albumID, albumPath)
	if len(removed) > 0 && ms.UnmonitorDeleted {
		if refs, err := store.ListTrackFilesForAlbum(ctx, s.DB, albumID); err == nil && len(refs) == 0 {
			_ = store.UpdateAlbumMonitored(ctx, s.DB, albumID, false)
		}
	}
	s.tidyFolder(ctx, ms, "music", albumPath)
	if refs, err := store.ListTrackFilesForAlbum(ctx, s.DB, albumID); err == nil {
		s.backfillQualities(ctx, refs, store.TrackFileQuality, s.trackFileQuality, store.UpdateTrackFileQuality)
	}
	if ms.TrackFileDate != store.FileDateNone {
		if refs, err := store.ListTrackFilesForAlbum(ctx, s.DB, albumID); err == nil {
			for _, ref := range refs {
				s.dateTrackFile(ctx, ms, ref.OwnerID, filepath.Join(albumPath, ref.RelativePath))
			}
		}
	}
}

// ScanMovieLibrary imports the largest video file already sitting in
// each file-less tracked movie's own folder - covers a library a
// previous Radarr/manual setup already organized before UMMarr existed.
// Unlike Import, there's no grab and no download-client file listing -
// the "source" is simply the movie's own already-resolved path, scanned
// live with the same importer.ScanDirectory the "Check again" retry
// feature uses.
func (s *ImportService) ScanMovieLibrary(ctx context.Context) (imported int, err error) {
	return s.scanMovieLibrary(ctx, ScanScope{})
}

func (s *ImportService) scanMovieLibrary(ctx context.Context, scope ScanScope) (imported int, err error) {
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return 0, err
	}
	// Reconcile every movie first, not just the ones already known to be
	// missing a file - a movie whose file was deleted by hand still counts
	// as filed, so it would never reach the scan below otherwise.
	if all, err := store.ListMovies(ctx, s.DB); err == nil {
		for _, m := range all {
			if !scope.inRoot(m.RootFolderID) {
				continue
			}
			s.reconcileMovie(ctx, ms, m)
		}
	}

	movies, err := store.ListMoviesMissingFile(ctx, s.DB)
	if err != nil {
		return 0, err
	}
	for _, m := range movies {
		if !scope.inRoot(m.RootFolderID) {
			continue
		}
		if s.scanMovieFolder(ctx, ms, m) {
			imported++
		}
	}
	return imported, nil
}

// ScanMovie rescans movieID's own folder for a file already on disk - the
// same logic ScanMovieLibrary applies library-wide, exposed per-item for
// the movie detail page's "Refresh" button. The practical case this
// covers: a file is sitting directly in the movie's folder (placed by
// hand, or left over from a previous Radarr/manual setup) but UMMarr
// still shows "Missing" because nothing ever told it about the file.
// No-ops (imported=false) if movieID already has a file recorded - never
// re-attaches over an existing movie_files row - or if movieID doesn't
// exist.
func (s *ImportService) ScanMovie(ctx context.Context, movieID int64) (imported bool, err error) {
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return false, err
	}
	detail, found, err := store.GetMovieDetail(ctx, s.DB, movieID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}
	// Reconcile first: if the recorded file was deleted by hand, dropping
	// the stale row is what lets the rescan below look at the folder again
	// instead of short-circuiting on "already has a file".
	removed := s.reconcileMovie(ctx, ms, detail.MovieSummary)
	if detail.File != nil && removed == 0 {
		return false, nil
	}
	return s.scanMovieFolder(ctx, ms, detail.MovieSummary), nil
}

// scanMovieFolder imports the largest video file already in m's own
// folder, if any - shared by ScanMovieLibrary and ScanMovie. Errors at
// any step are treated as "nothing to import here" (false), not
// propagated - matching ScanMovieLibrary's original behavior of silently
// skipping a candidate that doesn't pan out rather than failing the
// whole scan/request over it.
func (s *ImportService) scanMovieFolder(ctx context.Context, ms store.MediaSettings, m store.MovieSummary) bool {
	if !m.Path.Valid {
		return false
	}
	files, err := importer.ScanDirectory(m.Path.String)
	if err != nil {
		return false // folder doesn't exist on disk yet - nothing to scan
	}
	file, ok := importer.LargestVideoFile(files)
	if !ok {
		return false
	}
	name, err := store.ResolveMovieFileName(ctx, s.DB, m.ID, file.Path)
	if err != nil {
		return false
	}
	dest := filepath.Join(m.Path.String, name)
	src := filepath.Join(m.Path.String, file.Path)
	// Already in the movie's own folder - rename it, don't copy it
	// alongside itself.
	if err := importer.RenameFileIfDifferent(src, dest, permissions(ms)); err != nil {
		return false
	}
	fileID, err := store.InsertMovieFile(ctx, s.DB, m.ID, name, file.Size)
	if err != nil {
		return false
	}
	_ = store.UpdateMovieFileQuality(ctx, s.DB, fileID, s.movieFileQuality(ctx, m.ID, file.Path))
	s.dateMovieFile(ctx, ms, m.ID, dest)
	return true
}

// ScanSeriesLibrary is the whole-series-grab import logic (already
// per-file S/E parsing, so a partial library - some episodes already
// tracked, some not - is handled correctly) pointed at each series' own
// folder instead of a download folder, skipping any episode
// FindEpisodeMissingFile doesn't return (already has a file).
func (s *ImportService) ScanSeriesLibrary(ctx context.Context) (imported int, err error) {
	return s.scanSeriesLibrary(ctx, ScanScope{})
}

func (s *ImportService) scanSeriesLibrary(ctx context.Context, scope ScanScope) (imported int, err error) {
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return 0, err
	}
	// See ScanMovieLibrary: a fully-filed series is absent from
	// SeriesWithMissingEpisodes, so reconcile every series first.
	if all, err := store.ListSeries(ctx, s.DB); err == nil {
		for _, series := range all {
			if !scope.inRoot(series.RootFolderID) {
				continue
			}
			s.reconcileSeries(ctx, ms, series)
		}
	}

	seriesList, err := store.SeriesWithMissingEpisodes(ctx, s.DB)
	if err != nil {
		return 0, err
	}
	for _, series := range seriesList {
		if !scope.inRoot(series.RootFolderID) {
			continue
		}
		imported += s.scanSeriesFolder(ctx, ms, series, true)
	}
	return imported, nil
}

// ScanSeries rescans seriesID's own folder for episode files already on
// disk - the same logic ScanSeriesLibrary applies library-wide, exposed
// per-item for the series detail page's "Refresh" button. Safe to call on
// a series with no missing episodes (returns 0) since the per-episode
// FindEpisodeMissingFile check inside scanSeriesFolder already skips
// anything already filed.
func (s *ImportService) ScanSeries(ctx context.Context, seriesID int64) (imported int, err error) {
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return 0, err
	}
	detail, found, err := store.GetSeriesDetail(ctx, s.DB, seriesID)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, nil
	}
	// Drop episodes whose file was deleted by hand before importing, so
	// FindEpisodeMissingFile sees them as missing again.
	s.reconcileSeries(ctx, ms, detail.SeriesSummary)
	return s.scanSeriesFolder(ctx, ms, detail.SeriesSummary, true), nil
}

// scanSeriesFolder is ScanSeriesLibrary's per-series body, shared with
// ScanSeries - see ScanMovie/scanMovieFolder's comments for why errors
// here are swallowed as "nothing importable" rather than propagated.

// ScanMusicLibrary only considers albums whose FindImportRelease
// candidate has ZERO tracks with a file yet - deliberately not a partial
// scan like series gets. Music tracks are matched POSITIONALLY (no
// per-file parsing the way S01E02 gives episodes), so once some tracks
// in a release already have files and others don't, there's no reliable
// way to know which physical file on disk belongs at which now-missing
// position without re-deriving the same alignment a first-time scan
// already committed to - safer to only ever do this once per album,
// all-or-nothing.
func (s *ImportService) ScanMusicLibrary(ctx context.Context) (imported int, err error) {
	return s.scanMusicLibrary(ctx, ScanScope{})
}

// qualityUnknown reports whether a recorded quality says nothing at all.
func qualityUnknown(q releaseparse.FileQuality) bool {
	return q.Key() == "Unknown" && q.ReleaseGroup == "" && q.Resolution == ""
}

// fileQuality parses a file's own name, falling back to the release name of
// the grab that fetched it when the name carries nothing - a file already
// renamed to the naming template, for example, says nothing about itself.
func fileQuality(ctx context.Context, fileName string, released func(context.Context) (string, bool, error)) releaseparse.FileQuality {
	quality := releaseparse.Parse(fileName)
	if !qualityUnknown(quality) {
		return quality
	}
	if title, ok, err := released(ctx); err == nil && ok {
		if fromRelease := releaseparse.Parse(title); !qualityUnknown(fromRelease) {
			return fromRelease
		}
	}
	return quality
}

func (s *ImportService) movieFileQuality(ctx context.Context, movieID int64, fileName string) releaseparse.FileQuality {
	return fileQuality(ctx, fileName, func(ctx context.Context) (string, bool, error) {
		return store.ImportedReleaseTitleForMovie(ctx, s.DB, movieID)
	})
}

func (s *ImportService) episodeFileQuality(ctx context.Context, episodeID int64, fileName string) releaseparse.FileQuality {
	return fileQuality(ctx, fileName, func(ctx context.Context) (string, bool, error) {
		return store.ImportedReleaseTitleForEpisode(ctx, s.DB, episodeID)
	})
}

func (s *ImportService) trackFileQuality(ctx context.Context, trackID int64, fileName string) releaseparse.FileQuality {
	return fileQuality(ctx, fileName, func(ctx context.Context) (string, bool, error) {
		return store.ImportedReleaseTitleForTrack(ctx, s.DB, trackID)
	})
}

// backfillQualities fills in recorded qualities that say nothing, from the
// grabs that fetched the files. Runs on every Refresh, so files attached by
// an earlier scan of already-renamed names catch up.
func (s *ImportService) backfillQualities(ctx context.Context, refs []store.FileRef,
	read func(context.Context, store.Queryer, int64) (releaseparse.FileQuality, error),
	resolve func(context.Context, int64, string) releaseparse.FileQuality,
	write func(context.Context, store.Queryer, int64, releaseparse.FileQuality) error) {
	for _, ref := range refs {
		stored, err := read(ctx, s.DB, ref.ID)
		if err != nil || !qualityUnknown(stored) {
			continue
		}
		if q := resolve(ctx, ref.OwnerID, filepath.Base(ref.RelativePath)); !qualityUnknown(q) {
			_ = write(ctx, s.DB, ref.ID, q)
		}
	}
}

// ScanScope narrows a library scan: MediaType ("movie", "series", "music"
// or "" for all) and RootFolderID (0 for every folder of that type).
type ScanScope struct {
	MediaType    string
	RootFolderID int64
}

func (sc ScanScope) inRoot(rootFolderID int64) bool {
	return sc.RootFolderID == 0 || sc.RootFolderID == rootFolderID
}

func (sc ScanScope) covers(mediaType string) bool {
	return sc.MediaType == "" || sc.MediaType == mediaType
}

// ScanResult counts what a library scan imported.
type ScanResult struct {
	Movies, Episodes, Tracks int
}

// ScanLibrary runs the movie, series and music scans that scope covers.
func (s *ImportService) ScanLibrary(ctx context.Context, scope ScanScope) (ScanResult, error) {
	var res ScanResult
	var err error
	if scope.covers("movie") {
		if res.Movies, err = s.scanMovieLibrary(ctx, scope); err != nil {
			return res, err
		}
	}
	if scope.covers("series") {
		if res.Episodes, err = s.scanSeriesLibrary(ctx, scope); err != nil {
			return res, err
		}
	}
	if scope.covers("music") {
		if res.Tracks, err = s.scanMusicLibrary(ctx, scope); err != nil {
			return res, err
		}
	}
	return res, nil
}

func (s *ImportService) scanMusicLibrary(ctx context.Context, scope ScanScope) (imported int, err error) {
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return 0, err
	}
	albums, err := store.ListAlbums(ctx, s.DB)
	if err != nil {
		return 0, err
	}
	for _, album := range albums {
		if !album.Path.Valid || !scope.inRoot(album.RootFolderID) {
			continue
		}
		s.reconcileAlbum(ctx, ms, album.ID, album.Path.String)
		imported += s.scanAlbumFolder(ctx, ms, album.ID, album.Path.String, true)
	}
	return imported, nil
}

// scanAlbumFolder attaches the audio files in an album's own folder to its
// tracks, in order, when none of them has a file yet. rename moves each to
// its template name; a library import leaves files as they are.
func (s *ImportService) scanAlbumFolder(ctx context.Context, ms store.MediaSettings, albumID int64, dir string, rename bool) (imported int) {
	_, tracks, err := store.FindImportRelease(ctx, s.DB, albumID)
	if err != nil || len(tracks) == 0 {
		return 0
	}
	for _, t := range tracks {
		if t.HasFile {
			return 0
		}
	}
	files, err := importer.ScanDirectory(dir)
	if err != nil {
		return 0
	}
	audioFiles := importer.AudioFilesSorted(files)
	// No positional fallback on a scan: these are files someone has kept
	// for years, and attaching one to whichever track happened to line up
	// writes a wrong answer into the library. A file nothing identifies is
	// left where it is, and Manage Track Files lists it to be matched by
	// hand.
	matches, _ := s.MatchTracks(ctx, dir, audioFiles, tracks, false)
	perms := permissions(ms)
	for _, match := range matches {
		file, track := match.File, match.Track
		name := file.Path
		src := filepath.Join(dir, file.Path)
		dest := src
		if rename {
			if name, err = store.ResolveTrackFileName(ctx, s.DB, track.ID, file.Path); err != nil {
				continue
			}
			dest = filepath.Join(dir, name)
			// Already in the album's own folder - rename, don't duplicate.
			if err := importer.RenameFileIfDifferent(src, dest, perms); err != nil {
				continue
			}
		}
		var trackFileID int64
		attachErr := store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
			var err error
			trackFileID, err = store.AttachTrackFile(ctx, tx, track.ID, name, file.Size)
			return err
		})
		if attachErr != nil {
			continue
		}
		_ = store.UpdateTrackFileQuality(ctx, s.DB, trackFileID, s.trackFileQuality(ctx, track.ID, file.Path))
		s.dateTrackFile(ctx, ms, track.ID, dest)
		imported++
	}
	return imported
}

// replaceMovieFiles finishes an upgrade: every file the movie had before
// keptID goes to the recycle bin (or away), and its row with it. A file the
// import already overwrote in place only loses its row.
func (s *ImportService) replaceMovieFiles(ctx context.Context, ms store.MediaSettings, movieID int64, folder string, keptID int64, keptPath string) {
	refs, err := store.ListMovieFilesForMovie(ctx, s.DB, movieID)
	if err != nil {
		return
	}
	for _, ref := range refs {
		if ref.ID == keptID {
			continue
		}
		if old := filepath.Join(folder, ref.RelativePath); old != keptPath {
			if err := importer.RecycleOrRemove(old, ms.RecycleBinPath); err != nil {
				log.Printf("upgrade: remove replaced movie file %s: %v", old, err)
				continue
			}
		}
		_ = store.DeleteMovieFile(ctx, s.DB, ref.ID)
	}
}

// replaceEpisodeFile is replaceMovieFiles for one episode's previous file.
func (s *ImportService) replaceEpisodeFile(ctx context.Context, ms store.MediaSettings, seriesPath string, previous store.FileRef, keptPath string) {
	if old := filepath.Join(seriesPath, previous.RelativePath); old != keptPath {
		if err := importer.RecycleOrRemove(old, ms.RecycleBinPath); err != nil {
			log.Printf("upgrade: remove replaced episode file %s: %v", old, err)
			return
		}
	}
	_ = store.DeleteEpisodeFile(ctx, s.DB, previous.ID)
}

// recycleExisting moves a file already at an import's destination out of the
// way first - an upgrade landing under the same name - so the recycle bin
// keeps it instead of the import overwriting it.
func (s *ImportService) recycleExisting(ms store.MediaSettings, dest string) {
	if _, err := os.Stat(dest); err != nil {
		return
	}
	if err := importer.RecycleOrRemove(dest, ms.RecycleBinPath); err != nil {
		log.Printf("upgrade: move existing %s aside: %v", dest, err)
	}
}

// scanSeriesFolder attaches the episode files already in a series' folder to
// the episodes still missing one, a multi-episode file to each of its
// episodes. rename moves each into its season folder under its template
// name; a library import leaves files as they are.
func (s *ImportService) scanSeriesFolder(ctx context.Context, ms store.MediaSettings, series store.SeriesSummary, rename bool) (imported int) {
	if !series.Path.Valid {
		return 0
	}
	files, err := importer.ScanDirectory(series.Path.String)
	if err != nil {
		return 0
	}
	perms := permissions(ms)
	for _, file := range files {
		if !importer.IsVideoFile(file.Path) {
			continue
		}
		season, episodes, ok := importer.ParseEpisodes(file.Path)
		if !ok {
			continue
		}
		var all, missing []int64
		for _, episode := range episodes {
			if id, found, err := store.FindEpisode(ctx, s.DB, series.ID, season, episode); err == nil && found {
				all = append(all, id)
				if _, hasFile, _ := store.CurrentEpisodeFile(ctx, s.DB, id); !hasFile {
					missing = append(missing, id)
				}
			}
		}
		if len(missing) == 0 {
			continue
		}
		folder, err := store.ResolveEpisodeFolderPath(ctx, s.DB, series.ID, season)
		if err != nil {
			continue
		}
		name, err := store.ResolveEpisodesFileName(ctx, s.DB, all, file.Path)
		if err != nil {
			continue
		}
		src := filepath.Join(series.Path.String, file.Path)
		dest := src
		// Already inside the series folder - rename into its season
		// subfolder rather than leaving a second copy behind. A library
		// import leaves files as they are.
		if rename {
			dest = filepath.Join(folder, name)
			if err := importer.RenameFileIfDifferent(src, dest, perms); err != nil {
				continue
			}
		}
		relativePath, err := filepath.Rel(series.Path.String, dest)
		if err != nil {
			relativePath = name
		}
		for _, episodeID := range missing {
			var episodeFileID int64
			attachErr := store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
				var err error
				episodeFileID, err = store.AttachEpisodeFile(ctx, tx, episodeID, relativePath, file.Size)
				return err
			})
			if attachErr != nil {
				continue
			}
			_ = store.UpdateEpisodeFileQuality(ctx, s.DB, episodeFileID, s.episodeFileQuality(ctx, episodeID, file.Path))
			s.dateEpisodeFile(ctx, ms, episodeID, dest)
			imported++
		}
	}
	return imported
}

// Import brings a finished download's files into the library for the item
// the grab was for, and records the outcome.
func (s *ImportService) Import(ctx context.Context, grab store.Grab, files []importer.File, savePath string) (status, message string) {
	var replaced bool
	switch {
	case grab.MovieID.Valid:
		status, message, replaced = s.importMovieReplacing(ctx, grab.MovieID.Int64, files, savePath)
	case grab.SeriesID.Valid:
		status, message, replaced = s.importSeriesReplacing(ctx, grab.SeriesID.Int64, files, savePath)
	case grab.TrackID.Valid:
		status, message = s.importTrack(ctx, grab.TrackID.Int64, files, savePath)
	case grab.AlbumID.Valid:
		status, message = s.importAlbum(ctx, grab.AlbumID.Int64, files, savePath)
	default:
		return "import_failed", "grab has no movie/series/album id set"
	}
	if status == "imported" {
		event := store.EventImported
		if replaced {
			event = store.EventUpgraded
		}
		e := grabEvent(ctx, s.DB, grab, event)
		e.Detail = grab.ReleaseTitle + " - " + message
		e.Quality = releaseparse.Parse(grab.ReleaseTitle).Key()
		s.Events.Record(ctx, e)
	}
	return status, message
}

// importMovieReplacing is importMovie, also saying whether a file was replaced.
func (s *ImportService) importMovieReplacing(ctx context.Context, movieID int64, files []importer.File, savePath string) (string, string, bool) {
	refs, _ := store.ListMovieFilesForMovie(ctx, s.DB, movieID)
	status, message := s.importMovie(ctx, movieID, files, savePath)
	return status, message, len(refs) > 0
}

// importSeriesReplacing is importSeries, also saying whether any episode
// the download covered already had a file.
func (s *ImportService) importSeriesReplacing(ctx context.Context, seriesID int64, files []importer.File, savePath string) (string, string, bool) {
	replaced := false
	for _, file := range files {
		if season, episodes, ok := importer.ParseEpisodes(file.Path); ok && importer.IsVideoFile(file.Path) {
			for _, episode := range episodes {
				if id, found, err := store.FindEpisode(ctx, s.DB, seriesID, season, episode); err == nil && found {
					if _, has, _ := store.CurrentEpisodeFile(ctx, s.DB, id); has {
						replaced = true
					}
				}
			}
		}
	}
	status, message := s.importSeries(ctx, seriesID, files, savePath)
	return status, message, replaced
}

// FilePermissions is the Media management chmod/chown as importer sees it.
func FilePermissions(ms store.MediaSettings) importer.Permissions { return permissions(ms) }

// writeMetadata runs a metadata write, logging rather than failing an
// import over it.
func (s *ImportService) writeMetadata(write func() error) {
	if s.Metadata == nil {
		return
	}
	if err := write(); err != nil {
		log.Printf("metadata: %v", err)
	}
}

// importEpisodeFile imports one video file as the given episodes of a
// series, bringing along its extras among files. It returns the
// episode_files rows it attached, or why it couldn't.
//
// A multi-episode file (e.g. "S01E23-E24") attaches to every episode number
// it covers, all pointing at the same relativePath - naming/destination is
// resolved once from the first matched episode, since it's one physical
// file either way.
func (s *ImportService) importEpisodeFile(ctx context.Context, ms store.MediaSettings, opts importer.ImportOptions, seriesID int64, seriesPath string,
	season int, episodes []int, file importer.File, files []importer.File, savePath string) (fileIDs []int64, reason string) {
	var episodeIDs []int64
	for _, episode := range episodes {
		episodeID, found, err := store.FindEpisode(ctx, s.DB, seriesID, season, episode)
		if err != nil || !found {
			reason = fmt.Sprintf("series has no S%02dE%02d", season, episode)
			continue
		}
		episodeIDs = append(episodeIDs, episodeID)
	}
	if len(episodeIDs) == 0 {
		return nil, reason
	}

	folder, err := store.ResolveEpisodeFolderPath(ctx, s.DB, seriesID, season)
	if err != nil {
		return nil, fmt.Sprintf("resolve season folder: %v", err)
	}
	name, err := store.ResolveEpisodesFileName(ctx, s.DB, episodeIDs, file.Path)
	if err != nil {
		return nil, fmt.Sprintf("resolve episode file name: %v", err)
	}
	dest := filepath.Join(folder, name)
	s.recycleExisting(ms, dest)
	if _, err := importer.ImportFile(filepath.Join(savePath, file.Path), dest, opts); err != nil {
		return nil, fmt.Sprintf("import %q: %v", filepath.Base(file.Path), err)
	}
	relativePath, err := filepath.Rel(seriesPath, dest)
	if err != nil {
		relativePath = name // dest is always under seriesPath by construction; name is the safe fallback
	}

	for _, episodeID := range episodeIDs {
		previous, hadFile, _ := store.CurrentEpisodeFile(ctx, s.DB, episodeID)
		var episodeFileID int64
		attachErr := store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
			var err error
			episodeFileID, err = store.AttachEpisodeFile(ctx, tx, episodeID, relativePath, file.Size)
			return err
		})
		if attachErr != nil {
			reason = fmt.Sprintf("record episode file: %v", attachErr)
			continue
		}
		_ = store.UpdateEpisodeFileQuality(ctx, s.DB, episodeFileID, s.episodeFileQuality(ctx, episodeID, file.Path))
		if hadFile {
			s.replaceEpisodeFile(ctx, ms, seriesPath, previous, dest)
		}
		s.dateEpisodeFile(ctx, ms, episodeID, dest)
		fileIDs = append(fileIDs, episodeFileID)
	}
	if len(fileIDs) > 0 {
		importExtras(ms, files, file, savePath, folder, name, extrasRenameMatching)
	}
	return fileIDs, reason
}
