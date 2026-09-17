package sync

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/musicbrainz"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/titleutil"
)

// LibraryImport is the progress of importing one movie library folder's
// unmapped subfolders, like Radarr's Import existing movies: each folder is
// matched on TMDB by its "Title (Year)" name, added pointing at that folder,
// and its video file attached as it is on disk - nothing is renamed.
type LibraryImport struct {
	RootFolderID int64
	Kind         string // movie, series or music
	Path         string
	Running      bool
	Total        int
	Done         int
	Added        int      // movies, series or artists added
	Albums       int      // albums added (music)
	NoAlbums     int      // artist folders holding tracks but no album folders (music)
	NoVideo      int      // folders without a video (or audio) file
	Unmatched    []string // folders TMDB gave no confident match for
	Skipped      int      // loose files at the top level, which Radarr ignores too
	LastError    string
	Finished     time.Time
}

// LibraryImports lists the latest import of every folder, newest first.
func (s *ImportService) LibraryImports() []LibraryImport {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LibraryImport, 0, len(s.imports))
	for _, li := range s.imports {
		out = append(out, *li)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].RootFolderID < out[j-1].RootFolderID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// StartLibraryImport imports rootFolderID's unmapped folders in the
// background. It reports false when that folder's import is still running.
func (s *ImportService) StartLibraryImport(rootFolderID int64, path, kind string) bool {
	s.mu.Lock()
	if s.imports == nil {
		s.imports = map[int64]*LibraryImport{}
	}
	if li, ok := s.imports[rootFolderID]; ok && li.Running {
		s.mu.Unlock()
		return false
	}
	progress := &LibraryImport{RootFolderID: rootFolderID, Path: path, Kind: kind, Running: true}
	s.imports[rootFolderID] = progress
	s.mu.Unlock()

	update := func(change func(*LibraryImport)) {
		s.mu.Lock()
		change(progress)
		s.mu.Unlock()
	}
	go func() {
		s.runMu.Lock()
		defer s.runMu.Unlock()
		err := s.importLibrary(context.Background(), rootFolderID, update)
		update(func(li *LibraryImport) {
			li.Running, li.Finished = false, time.Now()
			if err != nil {
				li.LastError = err.Error()
			}
		})
		if err != nil {
			log.Printf("library import of root folder %d: %v", rootFolderID, err)
		}
	}()
	return true
}

var movieFolderPattern = regexp.MustCompile(`^(.+?)\s*\((\d{4})?\)$`)

// parseMovieFolder reads "Title (Year)" - Radarr's folder name - with the
// year optional or empty, falling back to release-name parsing.
func parseMovieFolder(name string) (title string, year int) {
	if m := movieFolderPattern.FindStringSubmatch(name); m != nil {
		year, _ = strconv.Atoi(m[2])
		return strings.TrimSpace(m[1]), year
	}
	if info, ok := releaseparse.ParseMovie(name); ok && info.Title != "" {
		return info.Title, info.Year
	}
	return strings.TrimSpace(name), 0
}

func resultYear(r tmdb.MovieSearchResult) int {
	if len(r.ReleaseDate) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(r.ReleaseDate[:4])
	return y
}

func yearNear(a, b int) bool {
	return a == 0 || b == 0 || a-b <= 1 && b-a <= 1
}

// pickMovie chooses the TMDB result the folder means: the same cleaned title
// with a year within one, or failing that the first result when the folder's
// year matches it. No confident match means no match - Radarr asks the user.
func pickMovie(results []tmdb.MovieSearchResult, title string, year int) (tmdb.MovieSearchResult, bool) {
	clean := matchKey(title)
	for _, r := range results {
		if matchKey(r.Title) == clean && yearNear(year, resultYear(r)) {
			return r, true
		}
	}
	if year > 0 && len(results) > 0 && resultYear(results[0]) == year {
		return results[0], true
	}
	return tmdb.MovieSearchResult{}, false
}

func (s *ImportService) importLibrary(ctx context.Context, rootFolderID int64, update func(func(*LibraryImport))) error {
	folder, err := store.GetRootFolder(ctx, s.DB, rootFolderID)
	if err != nil {
		return err
	}
	update(func(li *LibraryImport) { li.Path, li.Kind = folder.Path, folder.MediaType })
	switch folder.MediaType {
	case "series":
		return s.importSeriesLibrary(ctx, folder, update)
	case "music":
		return s.importMusicLibrary(ctx, folder, update)
	}
	return s.importMovieLibrary(ctx, rootFolderID, update)
}

func (s *ImportService) importMovieLibrary(ctx context.Context, rootFolderID int64, update func(func(*LibraryImport))) error {
	if s.Movies == nil || s.Movies.TMDB == nil {
		return fmt.Errorf("TMDB isn't configured, so folders can't be matched")
	}
	rootPath, err := store.GetRootFolderPath(ctx, s.DB, rootFolderID)
	if err != nil {
		return err
	}
	update(func(li *LibraryImport) { li.Path = rootPath })
	profileID, err := store.DefaultQualityProfileID(ctx, s.DB)
	if err != nil {
		return err
	}
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return err
	}
	mappedPaths, err := store.ItemFolderPaths(ctx, s.DB, "movie")
	if err != nil {
		return err
	}
	mapped := make(map[string]bool, len(mappedPaths))
	for _, p := range mappedPaths {
		mapped[filepath.Clean(p)] = true
	}
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		return err
	}
	var folders []string
	loose := 0
	for _, e := range entries {
		switch {
		case e.IsDir() && !mapped[filepath.Join(filepath.Clean(rootPath), e.Name())]:
			folders = append(folders, e.Name())
		case !e.IsDir() && importer.IsVideoFile(e.Name()):
			loose++
		}
	}
	update(func(li *LibraryImport) { li.Total, li.Skipped = len(folders), loose })

	for _, name := range folders {
		s.importMovieFolder(ctx, ms, rootFolderID, rootPath, name, profileID, update)
		update(func(li *LibraryImport) { li.Done++ })
	}
	return nil
}

func (s *ImportService) importMovieFolder(ctx context.Context, ms store.MediaSettings, rootFolderID int64, rootPath, name string, profileID int64, update func(func(*LibraryImport))) {
	dir := filepath.Join(rootPath, name)
	files, err := importer.ScanDirectory(dir)
	if err != nil {
		update(func(li *LibraryImport) { li.LastError = err.Error() })
		return
	}
	file, ok := importer.LargestVideoFile(files)
	if !ok {
		update(func(li *LibraryImport) { li.NoVideo++ })
		return
	}
	title, year := parseMovieFolder(name)
	results, err := s.Movies.TMDB.SearchMovies(ctx, title, year)
	if err == nil && len(results) == 0 && year > 0 {
		results, err = s.Movies.TMDB.SearchMovies(ctx, title, 0)
	}
	if err != nil {
		update(func(li *LibraryImport) { li.LastError = fmt.Sprintf("%s: %v", name, err) })
		return
	}
	match, ok := pickMovie(results, title, year)
	if !ok {
		s.noteUnmatched(ctx, update, name, "", store.UnmatchedNoMatch, "")
		return
	}
	movieID, err := s.Movies.AddByTMDBID(ctx, match.ID, rootFolderID, profileID)
	if err != nil {
		update(func(li *LibraryImport) { li.LastError = fmt.Sprintf("%s: %v", name, err) })
		return
	}
	// The movie may have been tracked already, pointed at a folder that
	// doesn't exist: it belongs here. One that already has a file elsewhere
	// is a duplicate copy, which is the user's call.
	if detail, found, err := store.GetMovieDetail(ctx, s.DB, movieID); err == nil && found && detail.Path.Valid && filepath.Clean(detail.Path.String) != filepath.Clean(dir) {
		if detail.File != nil {
			s.noteUnmatched(ctx, update, name, "", store.UnmatchedDuplicate, "already in the library as "+detail.Path.String)
			return
		}
	}
	store.ResolveUnmatchedFolder(ctx, s.DB, dir)
	if err := store.SetMoviePath(ctx, s.DB, movieID, dir); err != nil {
		update(func(li *LibraryImport) { li.LastError = fmt.Sprintf("%s: %v", name, err) })
		return
	}
	fileID, err := store.InsertMovieFile(ctx, s.DB, movieID, file.Path, file.Size)
	if err != nil {
		update(func(li *LibraryImport) { li.LastError = fmt.Sprintf("%s: %v", name, err) })
		return
	}
	_ = store.UpdateMovieFileQuality(ctx, s.DB, fileID, s.movieFileQuality(ctx, movieID, file.Path))
	s.dateMovieFile(ctx, ms, movieID, filepath.Join(dir, file.Path))
	e := movieEvent(ctx, s.DB, movieID, store.EventAdded)
	e.Detail, e.Source = dir, "library import"
	s.Events.Record(ctx, e)
	update(func(li *LibraryImport) { li.Added++ })
}

// unmappedFolders lists root's subfolders no item of mediaType lives in,
// and counts the loose video or audio files beside them.
func (s *ImportService) unmappedFolders(ctx context.Context, root, mediaType string, isMedia func(string) bool) (folders []string, loose int, err error) {
	mappedPaths, err := store.ItemFolderPaths(ctx, s.DB, mediaType)
	if err != nil {
		return nil, 0, err
	}
	// Another library folder inside this one is scanned on its own.
	for _, mt := range []string{"movie", "series", "music"} {
		roots, err := store.ListRootFolders(ctx, s.DB, mt)
		if err != nil {
			return nil, 0, err
		}
		for _, r := range roots {
			mappedPaths = append(mappedPaths, r.Path)
		}
	}
	if err != nil {
		return nil, 0, err
	}
	mapped := make(map[string]bool, len(mappedPaths))
	for _, p := range mappedPaths {
		mapped[filepath.Clean(p)] = true
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, 0, err
	}
	for _, e := range entries {
		switch {
		case e.IsDir() && !mapped[filepath.Join(filepath.Clean(root), e.Name())]:
			folders = append(folders, e.Name())
		case !e.IsDir() && isMedia(e.Name()):
			loose++
		}
	}
	return folders, loose, nil
}

func fail(update func(func(*LibraryImport)), name string, err error) {
	update(func(li *LibraryImport) { li.LastError = fmt.Sprintf("%s: %v", name, err) })
}

// pickSeries is pickMovie for TV: the same cleaned title, and the first-air
// year within one when the folder names one.
func pickSeries(results []tmdb.SeriesSearchResult, title string, year int) (tmdb.SeriesSearchResult, bool) {
	clean := matchKey(title)
	for _, r := range results {
		ry := 0
		if len(r.FirstAirDate) >= 4 {
			ry, _ = strconv.Atoi(r.FirstAirDate[:4])
		}
		if matchKey(r.Name) == clean && yearNear(year, ry) {
			return r, true
		}
	}
	return tmdb.SeriesSearchResult{}, false
}

// hasEpisodeFiles reports whether a folder holds a video file named like
// an episode - what makes it a series folder rather than a stray one.
func hasEpisodeFiles(files []importer.File) bool {
	for _, f := range files {
		if !importer.IsVideoFile(f.Path) {
			continue
		}
		if _, _, ok := importer.ParseEpisode(f.Path); ok {
			return true
		}
	}
	return false
}

func (s *ImportService) importSeriesLibrary(ctx context.Context, folder store.RootFolder, update func(func(*LibraryImport))) error {
	if s.Series == nil || s.Series.TMDB == nil {
		return fmt.Errorf("TMDB isn't configured, so folders can't be matched")
	}
	profileID, err := store.DefaultQualityProfileID(ctx, s.DB)
	if err != nil {
		return err
	}
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return err
	}
	folders, loose, err := s.unmappedFolders(ctx, folder.Path, "series", importer.IsVideoFile)
	if err != nil {
		return err
	}
	update(func(li *LibraryImport) { li.Total, li.Skipped = len(folders), loose })
	for _, name := range folders {
		dir := filepath.Join(folder.Path, name)
		files, err := importer.ScanDirectory(dir)
		if err != nil {
			fail(update, name, err)
		} else if !hasEpisodeFiles(files) {
			update(func(li *LibraryImport) { li.NoVideo++ })
		} else {
			title, year := parseMovieFolder(name)
			results, err := s.Series.TMDB.SearchSeries(ctx, title)
			if err != nil {
				fail(update, name, err)
			} else if match, ok := pickSeries(results, title, year); !ok {
				s.noteUnmatched(ctx, update, name, "", store.UnmatchedNoMatch, "")
			} else if seriesID, err := s.Series.AddByTMDBID(ctx, match.ID, folder.ID, profileID); err != nil {
				fail(update, name, err)
			} else if detail, found, err := store.GetSeriesDetail(ctx, s.DB, seriesID); err == nil && found && detail.Path.Valid && filepath.Clean(detail.Path.String) != filepath.Clean(dir) && detail.EpisodeFileCount > 0 {
				s.noteUnmatched(ctx, update, name, "", store.UnmatchedDuplicate, "already in the library as "+detail.Path.String)
			} else if err := store.SetSeriesPath(ctx, s.DB, seriesID, dir); err != nil {
				fail(update, name, err)
			} else {
				summary := detail.SeriesSummary
				summary.Path.String, summary.Path.Valid = dir, true
				s.scanSeriesFolder(ctx, ms, summary, false)
				e := seriesEvent(ctx, s.DB, seriesID, store.EventAdded)
				e.Detail, e.Source = dir, "library import"
				s.Events.Record(ctx, e)
				update(func(li *LibraryImport) { li.Added++ })
			}
		}
		update(func(li *LibraryImport) { li.Done++ })
	}
	return nil
}

var artistSuffix = regexp.MustCompile(`^(.+), (The|A|An)$`)

// artistFolderName reads an artist from a folder name, turning "Beloved,
// The" back into "The Beloved".
func artistFolderName(name string) string {
	if m := artistSuffix.FindStringSubmatch(name); m != nil {
		return m[2] + " " + m[1]
	}
	return strings.TrimSpace(name)
}

var albumYearPattern = regexp.MustCompile(`^(?:(\d{4}) - )?(.+?)(?: \((\d{4})\))?$`)

// albumQualifiers are the packaging notes people add to album folder names.
var albumQualifiers = regexp.MustCompile(`(?i)\s*(?:\((?:cd|vinyl|flac|mp3|deluxe[^)]*|remaster[^)]*|[^)]*edition|[^)]*bit[^)]*)\)|\[[^\]]*\]|-\s*vinyl(?: edition)?|vinyl edition|vinyl)\s*$`)

// albumFolderName reads an album title from "Album", "Album (2001)",
// "Album (Vinyl)", "Album [24 bit FLAC]" or
// "2001 - Album".
func albumFolderName(name string) string {
	for {
		stripped := albumQualifiers.ReplaceAllString(name, "")
		if stripped == name || stripped == "" {
			break
		}
		name = stripped
	}
	if m := albumYearPattern.FindStringSubmatch(name); m != nil {
		return strings.TrimSpace(m[2])
	}
	return strings.TrimSpace(name)
}

func hasAudioFiles(files []importer.File) bool {
	for _, f := range files {
		if importer.IsAudioFile(f.Path) {
			return true
		}
	}
	return false
}

func pickReleaseGroup(groups []musicbrainz.ReleaseGroup, title string) (musicbrainz.ReleaseGroup, bool) {
	clean := matchKey(title)
	var fallback *musicbrainz.ReleaseGroup
	for i := range groups {
		if matchKey(groups[i].Title) != clean {
			continue
		}
		if groups[i].PrimaryType == "Album" && len(groups[i].SecondaryTypes) == 0 {
			return groups[i], true
		}
		if fallback == nil {
			fallback = &groups[i]
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return musicbrainz.ReleaseGroup{}, false
}

func (s *ImportService) importMusicLibrary(ctx context.Context, folder store.RootFolder, update func(func(*LibraryImport))) error {
	if s.Music == nil || s.Music.MusicBrainz == nil {
		return fmt.Errorf("MusicBrainz isn't configured, so folders can't be matched")
	}
	profileID, err := store.DefaultQualityProfileID(ctx, s.DB)
	if err != nil {
		return err
	}
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return err
	}
	// Every artist folder: an artist already tracked can still have album
	// folders nothing maps to yet.
	entries, err := os.ReadDir(folder.Path)
	if err != nil {
		return err
	}
	var folders []string
	loose := 0
	for _, e := range entries {
		switch {
		case e.IsDir() && e.Name() != "Various Artists":
			folders = append(folders, e.Name())
		case !e.IsDir() && importer.IsAudioFile(e.Name()):
			loose++
		}
	}
	update(func(li *LibraryImport) { li.Total, li.Skipped = len(folders), loose })
	for _, name := range folders {
		s.importArtistFolder(ctx, ms, folder, name, profileID, update)
		update(func(li *LibraryImport) { li.Done++ })
	}
	return nil
}

// trackedArtistAt finds the artist living in dir and its MusicBrainz id.
func (s *ImportService) trackedArtistAt(ctx context.Context, dir string) (artistID int64, mbid string, found bool) {
	err := s.DB.QueryRowContext(ctx, `
		SELECT a.id, COALESCE((SELECT e.external_id FROM external_ids e WHERE e.entity_type = 'artist' AND e.provider = 'musicbrainz' AND e.entity_id = a.artist_metadata_id), '')
		FROM artists a WHERE a.path = ?`, dir).Scan(&artistID, &mbid)
	return artistID, mbid, err == nil && mbid != ""
}

func (s *ImportService) importArtistFolder(ctx context.Context, ms store.MediaSettings, folder store.RootFolder, name string, profileID int64, update func(func(*LibraryImport))) {
	dir := filepath.Join(folder.Path, name)
	artistID, mbid, tracked := s.trackedArtistAt(ctx, dir)
	if !tracked {
		files, err := importer.ScanDirectory(dir)
		if err != nil {
			fail(update, name, err)
			return
		}
		if !hasAudioFiles(files) {
			update(func(li *LibraryImport) { li.NoVideo++ })
			return
		}
		artistName := artistFolderName(name)
		results, err := s.Music.MusicBrainz.SearchArtist(ctx, artistName)
		if err != nil {
			fail(update, name, err)
			return
		}
		clean := titleutil.CleanTitle(artistName)
		var match *musicbrainz.Artist
		for i := range results {
			if matchKey(results[i].Name) == clean {
				match = &results[i]
				break
			}
		}
		if match == nil {
			s.noteUnmatched(ctx, update, name, "", store.UnmatchedNoMatch, "")
			return
		}
		if artistID, err = s.Music.AddArtistByMBID(ctx, match.ID, folder.ID, profileID); err != nil {
			fail(update, name, err)
			return
		}
		if err := store.SetArtistPath(ctx, s.DB, artistID, dir); err != nil {
			fail(update, name, err)
			return
		}
		mbid = match.ID
		e := store.Event{Event: store.EventAdded, MediaType: "music", Title: artistName}
		e.Detail, e.Source = dir, "library import"
		s.Events.Record(ctx, e)
		update(func(li *LibraryImport) { li.Added++ })
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		fail(update, name, err)
		return
	}
	mappedAlbums, err := store.ItemFolderPaths(ctx, s.DB, "album")
	if err != nil {
		fail(update, name, err)
		return
	}
	mapped := make(map[string]bool, len(mappedAlbums))
	for _, p := range mappedAlbums {
		mapped[filepath.Clean(p)] = true
	}
	var groups []musicbrainz.ReleaseGroup
	albumFolders := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		albumDir := filepath.Join(dir, e.Name())
		if mapped[filepath.Clean(albumDir)] {
			albumFolders++
			continue
		}
		albumFiles, err := importer.ScanDirectory(albumDir)
		if err != nil || !hasAudioFiles(albumFiles) {
			continue
		}
		albumFolders++
		if groups == nil {
			if groups, err = s.Music.MusicBrainz.GetArtistReleaseGroups(ctx, mbid); err != nil {
				fail(update, name, err)
				return
			}
		}
		rg, ok := pickReleaseGroup(groups, albumFolderName(e.Name()))
		if !ok {
			s.noteUnmatched(ctx, update, name+" / "+e.Name(), albumDir, store.UnmatchedNoMatch, "")
			continue
		}
		albumID, err := s.Music.AddAlbumByMBID(ctx, rg.ID, artistID)
		if err != nil {
			fail(update, name+" / "+e.Name(), err)
			continue
		}
		if err := store.SetAlbumPathTo(ctx, s.DB, albumID, albumDir); err != nil {
			fail(update, name+" / "+e.Name(), err)
			continue
		}
		s.scanAlbumFolder(ctx, ms, albumID, albumDir, false)
		e := albumEvent(ctx, s.DB, albumID, store.EventAdded)
		e.Detail, e.Source = albumDir, "library import"
		s.Events.Record(ctx, e)
		update(func(li *LibraryImport) { li.Albums++ })
	}
	if albumFolders == 0 && !tracked {
		update(func(li *LibraryImport) { li.NoAlbums++ })
	}
}

var countrySuffix = regexp.MustCompile(`\s*\((?:US|UK|AU|CA|NZ|IE)\)$`)

// matchKey is how a folder name and a provider title are compared: cleaned,
// with "&" read as "and" and a trailing country tag like (US) ignored.
func matchKey(title string) string {
	title = countrySuffix.ReplaceAllString(title, "")
	return titleutil.CleanTitle(strings.ReplaceAll(title, "&", " and "))
}

// noteUnmatched records a folder a scan couldn't take: on the running
// import's own list, and in unmatched_folders so the scan report outlives
// the run and the folder can be matched by hand later. path defaults to the
// folder under the library folder being scanned.
func (s *ImportService) noteUnmatched(ctx context.Context, update func(func(*LibraryImport)), name, path, reason, detail string) {
	row := store.UnmatchedFolder{Name: name, Path: path, Reason: reason, Detail: detail}
	update(func(li *LibraryImport) {
		shown := name
		if detail != "" {
			shown += " (" + detail + ")"
		}
		li.Unmatched = append(li.Unmatched, shown)
		row.RootFolderID, row.Kind = li.RootFolderID, li.Kind
		if row.Path == "" {
			row.Path = filepath.Join(li.Path, name)
		}
	})
	if err := store.RecordUnmatchedFolder(ctx, s.DB, row); err != nil {
		log.Printf("library import: %v", err)
	}
}
