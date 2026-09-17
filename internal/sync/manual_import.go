package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/decision"
	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/titleutil"
)

// Manual Import, as in Sonarr and Radarr: list the media files in a folder
// with UMMarr's best guess at what each one is, let someone correct the
// guesses, then import the ticked files.

// Manual import kinds.
const (
	ManualMovie  = "movie"
	ManualSeries = "series"
	ManualTrack  = "track"
)

// Import modes: copy leaves the source (a download that's still seeding),
// move deletes it once imported.
const (
	ImportCopy = "copy"
	ImportMove = "move"
)

// ManualImportItem is one file and what it's to be imported as.
type ManualImportItem struct {
	Path     string // relative to the folder
	Size     int64
	Kind     string // ManualMovie, ManualSeries, ManualTrack, or "" when unknown
	MovieID  int64
	SeriesID int64
	Season   int
	Episodes []int
	AlbumID  int64
	TrackID  int64
	Quality  string // releaseparse.AllQualities key
	// Rejections are why the file mightn't be what it seems, or can't be
	// imported as guessed. They're shown, not enforced.
	Rejections []string
}

// ManualImportResult is how importing one item went.
type ManualImportResult struct {
	Path    string
	OK      bool
	Message string
}

var samplePattern = regexp.MustCompile(`(?i)(^|[\W_])sample([\W_]|$)`)

// minimumVideoSize is below Sonarr's sample threshold for an episode.
const minimumVideoSize = 40 << 20

// ErrNotAFolder means the manual import path isn't a readable folder.
var ErrNotAFolder = errors.New("not a folder UMMarr can read")

// ManualImportScan lists folder's video and audio files with a guess at
// each. With grab set, every file is taken to be for what the grab was for.
func (s *ImportService) ManualImportScan(ctx context.Context, folder string, grab *store.Grab) ([]ManualImportItem, error) {
	info, err := os.Stat(folder)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%s: %w", folder, ErrNotAFolder)
	}
	files, err := importer.ScanDirectory(folder)
	if err != nil {
		return nil, err
	}
	lib, err := s.loadManualLibrary(ctx)
	if err != nil {
		return nil, err
	}
	folderName := filepath.Base(folder)

	var items []ManualImportItem
	for _, f := range files {
		video, audio := importer.IsVideoFile(f.Path), importer.IsAudioFile(f.Path)
		if !video && !audio {
			continue
		}
		item := ManualImportItem{Path: f.Path, Size: f.Size, Quality: releaseparse.Parse(folderName + "/" + f.Path).Key()}
		if audio {
			item.Quality = "Unknown"
		}
		if video && (samplePattern.MatchString(filepath.Base(f.Path)) || f.Size < minimumVideoSize) {
			item.Rejections = append(item.Rejections, "Sample")
		}
		switch {
		case grab != nil && grab.MovieID.Valid && video:
			item.Kind, item.MovieID = ManualMovie, grab.MovieID.Int64
		case grab != nil && grab.SeriesID.Valid && video:
			item.Kind, item.SeriesID = ManualSeries, grab.SeriesID.Int64
			s.guessEpisodes(ctx, &item, folderName)
		case grab != nil && grab.AlbumID.Valid && audio:
			item.Kind, item.AlbumID = ManualTrack, grab.AlbumID.Int64
			if grab.TrackID.Valid {
				item.TrackID = grab.TrackID.Int64
			}
		case video:
			lib.guessVideo(&item, folderName)
			if item.Kind == ManualSeries {
				s.guessEpisodes(ctx, &item, folderName)
			}
		case audio:
			lib.guessAlbum(&item, folderName)
		}
		if item.Kind == ManualTrack && item.TrackID == 0 {
			s.guessTrack(ctx, &item)
		}
		if item.Kind == "" {
			item.Rejections = append(item.Rejections, "Couldn't tell what this is - choose it below")
		}
		s.existingFileRejection(ctx, &item)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items, nil
}

// manualLibrary is what a file's name is matched against.
type manualLibrary struct {
	movies []store.MovieSummary
	series []store.SeriesSummary
	albums []store.AlbumSummary
}

func (s *ImportService) loadManualLibrary(ctx context.Context) (manualLibrary, error) {
	var lib manualLibrary
	var err error
	if lib.movies, err = store.ListMovies(ctx, s.DB); err != nil {
		return lib, err
	}
	if lib.series, err = store.ListSeries(ctx, s.DB); err != nil {
		return lib, err
	}
	if lib.albums, err = store.ListAlbums(ctx, s.DB); err != nil {
		return lib, err
	}
	return lib, nil
}

// guessVideo tries the file's name, then its folder's, as an episode and
// then as a movie.
func (lib manualLibrary) guessVideo(item *ManualImportItem, folderName string) {
	for _, name := range []string{filepath.Base(item.Path), folderName} {
		if info, ok := releaseparse.ParseEpisode(name); ok && !info.MultiSeason {
			for _, series := range lib.series {
				ws := store.WantedSeries{ID: series.ID, Title: series.Title, Year: int(series.Year.Int64)}
				if decision.MatchSeriesTitle(ws, info) {
					item.Kind, item.SeriesID = ManualSeries, series.ID
					return
				}
			}
		}
	}
	for _, name := range []string{folderName, filepath.Base(item.Path)} {
		info, ok := releaseparse.ParseMovie(name)
		if !ok {
			continue
		}
		clean := titleutil.CleanTitle(info.Title)
		for _, m := range lib.movies {
			if titleutil.CleanTitle(m.Title) != clean {
				continue
			}
			if info.Year != 0 && m.Year.Valid && (int(m.Year.Int64) < info.Year-1 || int(m.Year.Int64) > info.Year+1) {
				continue
			}
			item.Kind, item.MovieID = ManualMovie, m.ID
			return
		}
	}
}

// guessAlbum matches the folder (or the file's parent folder) against
// "Artist - Album".
func (lib manualLibrary) guessAlbum(item *ManualImportItem, folderName string) {
	names := []string{folderName}
	if dir := filepath.Dir(item.Path); dir != "." {
		names = append([]string{filepath.Base(dir), filepath.Base(dir) + " " + folderName}, names...)
	}
	for _, name := range names {
		for _, a := range lib.albums {
			if decision.MatchAlbum(store.WantedAlbum{Artist: a.ArtistName, Title: a.Title}, name) {
				item.Kind, item.AlbumID = ManualTrack, a.ID
				return
			}
		}
	}
}

// guessEpisodes reads the season and episodes from the file's path, or from
// the folder's name for a single file.
func (s *ImportService) guessEpisodes(ctx context.Context, item *ManualImportItem, folderName string) {
	season, episodes, ok := importer.ParseEpisodes(item.Path)
	if !ok {
		season, episodes, ok = importer.ParseEpisodes(folderName + "/" + filepath.Base(item.Path))
	}
	if !ok {
		item.Rejections = append(item.Rejections, "No season and episode in the file name - enter them below")
		return
	}
	item.Season, item.Episodes = season, episodes
	for _, ep := range episodes {
		if _, found, err := store.FindEpisode(ctx, s.DB, item.SeriesID, season, ep); err == nil && !found {
			item.Rejections = append(item.Rejections, fmt.Sprintf("The series has no S%02dE%02d", season, ep))
		}
	}
}

var leadingTrackNumber = regexp.MustCompile(`^\D{0,3}?(\d{1,3})[\s._-]`)

// guessTrack matches an audio file to its album's track by a leading track
// number, then by title.
func (s *ImportService) guessTrack(ctx context.Context, item *ManualImportItem) {
	_, tracks, err := store.FindImportRelease(ctx, s.DB, item.AlbumID)
	if err != nil || len(tracks) == 0 {
		item.Rejections = append(item.Rejections, "The album has no tracks to import into")
		return
	}
	base := strings.TrimSuffix(filepath.Base(item.Path), filepath.Ext(item.Path))
	if m := leadingTrackNumber.FindStringSubmatch(base); m != nil {
		if n, _ := strconv.Atoi(m[1]); n >= 1 && n <= len(tracks) {
			item.TrackID = tracks[n-1].ID
			return
		}
	}
	clean := titleutil.CleanTitle(base)
	for _, t := range tracks {
		if title := titleutil.CleanTitle(t.Title); title != "" && strings.Contains(clean, title) {
			item.TrackID = t.ID
			return
		}
	}
	item.Rejections = append(item.Rejections, "Couldn't tell which track this is - choose it below")
}

// existingFileRejection notes when importing would replace a file.
func (s *ImportService) existingFileRejection(ctx context.Context, item *ManualImportItem) {
	switch item.Kind {
	case ManualMovie:
		if refs, err := store.ListMovieFilesForMovie(ctx, s.DB, item.MovieID); err == nil && len(refs) > 0 {
			item.Rejections = append(item.Rejections, "The movie already has a file, which this would replace")
		}
	case ManualSeries:
		for _, ep := range item.Episodes {
			if id, found, err := store.FindEpisode(ctx, s.DB, item.SeriesID, item.Season, ep); err == nil && found {
				if _, has, _ := store.CurrentEpisodeFile(ctx, s.DB, id); has {
					item.Rejections = append(item.Rejections, fmt.Sprintf("S%02dE%02d already has a file, which this would replace", item.Season, ep))
				}
			}
		}
	}
}

// ManualImport imports items from folder. mode is ImportCopy or ImportMove.
// With grab set, the grab is marked imported once anything imports.
func (s *ImportService) ManualImport(ctx context.Context, folder, mode string, items []ManualImportItem, grab *store.Grab) []ManualImportResult {
	all, err := importer.ScanDirectory(folder)
	if err != nil {
		return []ManualImportResult{{Message: fmt.Sprintf("read %s: %v", folder, err)}}
	}
	sizes := map[string]int64{}
	for _, f := range all {
		sizes[f.Path] = f.Size
	}
	ms, err := store.GetMediaSettings(ctx, s.DB)
	if err != nil {
		return []ManualImportResult{{Message: err.Error()}}
	}

	results := make([]ManualImportResult, 0, len(items))
	imported := 0
	for _, item := range items {
		res := ManualImportResult{Path: item.Path}
		size, ok := sizes[item.Path]
		if !ok || strings.Contains(item.Path, "..") {
			res.Message = "file isn't in the folder any more"
			results = append(results, res)
			continue
		}
		file := importer.File{Path: item.Path, Size: size}
		quality, qualityErr := qualityOverride(item.Quality)
		switch item.Kind {
		case ManualMovie:
			var fileID int64
			var status string
			fileID, status, res.Message = s.importMovieFile(ctx, item.MovieID, file, all, folder)
			if res.OK = status == "imported"; res.OK && qualityErr == nil {
				_ = store.UpdateMovieFileQuality(ctx, s.DB, fileID, quality)
			}
			if res.OK {
				s.recordManualImport(ctx, movieEvent(ctx, s.DB, item.MovieID, store.EventImported), item, res.Message)
			}
		case ManualSeries:
			seriesPath, err := store.SeriesFolderPath(ctx, s.DB, item.SeriesID)
			if err != nil {
				res.Message = fmt.Sprintf("resolve series path: %v", err)
				break
			}
			if len(item.Episodes) == 0 {
				res.Message = "no episodes chosen"
				break
			}
			fileIDs, reason := s.importEpisodeFile(ctx, ms, importOptions(ms), item.SeriesID, seriesPath, item.Season, item.Episodes, file, all, folder)
			res.OK = len(fileIDs) > 0
			if !res.OK {
				res.Message = reason
				break
			}
			if qualityErr == nil {
				for _, id := range fileIDs {
					_ = store.UpdateEpisodeFileQuality(ctx, s.DB, id, quality)
				}
			}
			res.Message = fmt.Sprintf("imported as %s", episodeCodes(item.Season, item.Episodes))
			e := seriesEvent(ctx, s.DB, item.SeriesID, store.EventImported)
			e.Title += " " + episodeCodes(item.Season, item.Episodes)
			s.recordManualImport(ctx, e, item, res.Message)
		case ManualTrack:
			if item.TrackID == 0 {
				res.Message = "no track chosen"
				break
			}
			var status string
			_, status, res.Message = s.importTrackFile(ctx, item.TrackID, file, all, folder)
			res.OK = status == "imported"
			if res.OK {
				albumID, _ := store.GetAlbumIDForTrack(ctx, s.DB, item.TrackID)
				s.recordManualImport(ctx, albumEvent(ctx, s.DB, albumID, store.EventImported), item, res.Message)
			}
		default:
			res.Message = "choose a movie, series or album first"
		}
		if res.OK {
			imported++
			if mode == ImportMove {
				if err := os.Remove(filepath.Join(folder, item.Path)); err != nil {
					res.Message += fmt.Sprintf(" (couldn't remove the original: %v)", err)
				}
			}
		}
		results = append(results, res)
	}
	if mode == ImportMove && imported > 0 {
		_, _ = importer.RemoveEmptyDirs(folder)
	}
	if grab != nil && imported > 0 {
		msg := fmt.Sprintf("manually imported %d file(s)", imported)
		_ = store.UpdateGrabStatus(ctx, s.DB, grab.ID, "imported", sql.NullString{String: msg, Valid: true})
	}
	return results
}

func (s *ImportService) recordManualImport(ctx context.Context, e store.Event, item ManualImportItem, message string) {
	e.Detail = item.Path + " - " + message
	e.Source = "manual import"
	e.Quality = item.Quality
	s.Events.Record(ctx, e)
}

func qualityOverride(key string) (releaseparse.FileQuality, error) {
	var q releaseparse.FileQuality
	err := store.QualityFromKey(key, &q)
	return q, err
}

func episodeCodes(season int, episodes []int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "S%02d", season)
	for _, e := range episodes {
		fmt.Fprintf(&b, "E%02d", e)
	}
	return b.String()
}
