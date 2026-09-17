package sync

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sort"
	"strings"
	gosync "sync"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/decision"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// SearchService grabs releases on its own, as Radarr, Sonarr and Lidarr do:
// automatic search for one item, Search all missing, and RSS sync. Every
// release is judged by the decision engine and only approved ones are
// grabbed.
type SearchService struct {
	DB       *sql.DB
	Indexers *IndexerService
	Download *DownloadService
	Now      func() time.Time // test override

	mu      gosync.Mutex
	rssBusy bool
	missing map[string]*MissingSearch
}

func (s *SearchService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Grabbed is one release a search sent to the download client.
type Grabbed struct {
	Release string
	Indexer string
	For     string // e.g. "S01E03" or "season 2"; empty for a movie or album
}

// SearchReport is what a search or RSS sync did.
type SearchReport struct {
	Searched      int // indexers asked
	Releases      int
	Grabbed       []Grabbed
	TopRejections []string // the most common reasons nothing else was grabbed
	Errors        []IndexerError
	GrabErrors    []string
}

// Summary is the report in one line.
func (r SearchReport) Summary() string {
	msg := fmt.Sprintf("%d releases from %d indexers, %d grabbed", r.Releases, r.Searched, len(r.Grabbed))
	if n := len(r.Errors); n > 0 {
		msg += fmt.Sprintf(", %d indexer errors", n)
	}
	if n := len(r.GrabErrors); n > 0 {
		msg += fmt.Sprintf(", %d grabs failed", n)
	}
	return msg
}

func (r *SearchReport) add(result SearchResult, decisions []decision.Decision) {
	r.Searched = max(r.Searched, result.Searched)
	r.Releases += len(result.Releases)
	r.Errors = append(r.Errors, result.Errors...)
	counts := map[string]int{}
	for _, d := range decisions {
		for _, reason := range d.Rejections {
			counts[reason]++
		}
	}
	reasons := make([]string, 0, len(counts))
	for reason := range counts {
		reasons = append(reasons, reason)
	}
	sort.Slice(reasons, func(i, j int) bool {
		if counts[reasons[i]] != counts[reasons[j]] {
			return counts[reasons[i]] > counts[reasons[j]]
		}
		return reasons[i] < reasons[j]
	})
	if len(reasons) > 3 {
		reasons = reasons[:3]
	}
	r.TopRejections = reasons
}

func (r *SearchReport) grabbed(d decision.Decision, what string) {
	r.Grabbed = append(r.Grabbed, Grabbed{Release: d.Release.Title, Indexer: d.Release.Indexer, For: what})
}

// grabBest grabs the best approved decision with grab.
func grabBest(decisions []decision.Decision, report *SearchReport, grab func(decision.Decision) error) {
	best, ok := decision.Best(decisions)
	if !ok {
		return
	}
	if err := grab(best); err != nil {
		report.GrabErrors = append(report.GrabErrors, fmt.Sprintf("Couldn't grab %s: %v", best.Release.Title, err))
		return
	}
	report.grabbed(best, "")
}

// grabEpisodes grabs approved series decisions best first, skipping any that
// cover an episode already grabbed in this run, so a season pack and a single
// episode from it are never both downloaded.
func (s *SearchService) grabEpisodes(ctx context.Context, seriesID int64, decisions []decision.Decision, by string, taken map[int64]bool, report *SearchReport) {
	for _, d := range decisions {
		if !d.Approved() || len(d.Target.Episodes) == 0 {
			continue
		}
		overlaps := false
		for _, ep := range d.Target.Episodes {
			overlaps = overlaps || taken[ep.ID]
		}
		if overlaps {
			continue
		}
		var err error
		what := fmt.Sprintf("S%02dE%02d", d.Target.Episodes[0].SeasonNumber, d.Target.Episodes[0].EpisodeNumber)
		if d.Target.FullSeason {
			season := d.Target.Season
			what = fmt.Sprintf("season %d", season)
			_, err = s.Download.GrabSeries(ctx, seriesID, &season, d.Release, by)
		} else {
			ep := d.Target.Episodes[0]
			_, err = s.Download.GrabEpisode(ctx, seriesID, ep.SeasonNumber, ep.EpisodeNumber, d.Release, by)
		}
		if err != nil {
			report.GrabErrors = append(report.GrabErrors, fmt.Sprintf("Couldn't grab %s: %v", d.Release.Title, err))
			continue
		}
		for _, ep := range d.Target.Episodes {
			taken[ep.ID] = true
		}
		report.grabbed(d, what)
	}
}

// SearchMovie is Radarr's Search Movie: search, judge, grab the best.
func (s *SearchService) SearchMovie(ctx context.Context, movieID int64) (SearchReport, error) {
	var report SearchReport
	movie, err := store.GetWantedMovie(ctx, s.DB, movieID)
	if err != nil {
		return report, err
	}
	engine, err := decision.Load(ctx, s.DB, true)
	if err != nil {
		return report, err
	}
	s.searchMovie(ctx, engine, movie, &report)
	return report, nil
}

func (s *SearchService) searchMovie(ctx context.Context, engine *decision.Engine, movie store.WantedMovie, report *SearchReport) {
	result := s.Indexers.SearchMovie(ctx, PurposeAutomatic, MovieCriteria{Title: movie.Title, Year: movie.Year, TMDbID: movie.TMDbID, IMDbID: movie.IMDbID})
	decisions := engine.Movie(movie, result.Releases)
	report.add(result, decisions)
	grabBest(decisions, report, func(d decision.Decision) error {
		_, err := s.Download.GrabMovie(ctx, movie.ID, d.Release, PurposeAutomatic)
		return err
	})
}

func aired(ep store.WantedEpisode, now time.Time) bool {
	return ep.AirDate.Valid && !ep.AirDate.Time.After(now)
}

func missing(ep store.WantedEpisode) bool {
	return ep.Monitored && !ep.HasFile && !ep.Queued
}

// SearchSeries is Sonarr's series or season search. For each season with
// missing monitored episodes (just season, when given) it searches the whole
// season when every episode has aired, so season packs can be found, and
// otherwise each missing aired episode on its own.
func (s *SearchService) SearchSeries(ctx context.Context, seriesID int64, season *int) (SearchReport, error) {
	var report SearchReport
	series, err := store.GetWantedSeries(ctx, s.DB, seriesID)
	if err != nil {
		return report, err
	}
	engine, err := decision.Load(ctx, s.DB, true)
	if err != nil {
		return report, err
	}
	s.searchSeries(ctx, engine, series, season, &report)
	return report, nil
}

func (s *SearchService) searchSeries(ctx context.Context, engine *decision.Engine, series store.WantedSeries, only *int, report *SearchReport) {
	now := s.now()
	seasons := map[int]bool{}
	for _, ep := range series.Episodes {
		if (only == nil || ep.SeasonNumber == *only) && missing(ep) && (only != nil || ep.SeasonNumber > 0) {
			seasons[ep.SeasonNumber] = true
		}
	}
	numbers := make([]int, 0, len(seasons))
	for n := range seasons {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)

	taken := map[int64]bool{}
	for _, n := range numbers {
		episodes := series.Season(n)
		allAired := true
		for _, ep := range episodes {
			allAired = allAired && aired(ep, now)
		}
		if allAired {
			seasonNumber := n
			criteria := SeriesCriteria{Title: series.Title, TVDBID: series.TVDBID, Season: &seasonNumber}
			result := s.Indexers.SearchSeries(ctx, PurposeAutomatic, criteria)
			decisions := engine.Series(series, decision.SeriesScope{Season: &seasonNumber}, result.Releases)
			report.add(result, decisions)
			s.grabEpisodes(ctx, series.ID, decisions, PurposeAutomatic, taken, report)
			continue
		}
		for _, ep := range episodes {
			if missing(ep) && aired(ep, now) && !taken[ep.ID] {
				s.searchEpisode(ctx, engine, series, ep, taken, report)
			}
		}
	}
}

// SearchEpisode is Sonarr's episode search.
func (s *SearchService) SearchEpisode(ctx context.Context, seriesID, episodeID int64) (SearchReport, error) {
	var report SearchReport
	series, err := store.GetWantedSeries(ctx, s.DB, seriesID)
	if err != nil {
		return report, err
	}
	for _, ep := range series.Episodes {
		if ep.ID == episodeID {
			engine, err := decision.Load(ctx, s.DB, true)
			if err != nil {
				return report, err
			}
			s.searchEpisode(ctx, engine, series, ep, map[int64]bool{}, &report)
			return report, nil
		}
	}
	return report, fmt.Errorf("episode %d isn't part of series %d", episodeID, seriesID)
}

func (s *SearchService) searchEpisode(ctx context.Context, engine *decision.Engine, series store.WantedSeries, ep store.WantedEpisode, taken map[int64]bool, report *SearchReport) {
	season, episode := ep.SeasonNumber, ep.EpisodeNumber
	criteria := SeriesCriteria{Title: series.Title, TVDBID: series.TVDBID, Season: &season, Episode: &episode}
	result := s.Indexers.SearchSeries(ctx, PurposeAutomatic, criteria)
	decisions := engine.Series(series, decision.SeriesScope{Season: &season, Episode: &episode}, result.Releases)
	report.add(result, decisions)
	s.grabEpisodes(ctx, series.ID, decisions, PurposeAutomatic, taken, report)
}

// SearchAlbum is Lidarr's album search.
func (s *SearchService) SearchAlbum(ctx context.Context, albumID int64) (SearchReport, error) {
	var report SearchReport
	album, err := store.GetWantedAlbum(ctx, s.DB, albumID)
	if err != nil {
		return report, err
	}
	engine, err := decision.Load(ctx, s.DB, true)
	if err != nil {
		return report, err
	}
	s.searchAlbum(ctx, engine, album, &report)
	return report, nil
}

func (s *SearchService) searchAlbum(ctx context.Context, engine *decision.Engine, album store.WantedAlbum, report *SearchReport) {
	result := s.Indexers.SearchAlbum(ctx, PurposeAutomatic, AlbumCriteria{Artist: album.Artist, Album: album.Title})
	decisions := engine.Album(album, result.Releases)
	report.add(result, decisions)
	grabBest(decisions, report, func(d decision.Decision) error {
		_, err := s.Download.GrabAlbum(ctx, album.ID, d.Release, PurposeAutomatic)
		return err
	})
}

// MissingSearch is the progress of a Search all missing run.
type MissingSearch struct {
	Mode      string // SearchMissing or SearchCutoff
	MediaType string
	Running   bool
	Total     int
	Done      int
	Grabbed   int
	Finished  time.Time
	LastError string
}

// MissingSearchStatus reports the latest Search all missing run for mediaType.
func (s *SearchService) MissingSearchStatus(mediaType, mode string) MissingSearch {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.missing[mediaType+":"+mode]; ok {
		return *m
	}
	return MissingSearch{MediaType: mediaType, Mode: mode}
}

// StartMissingSearch searches every monitored item of mediaType that has
// nothing yet, one at a time in the background, like Radarr's Wanted ->
// Missing -> Search All. It reports false when a run is already going.
func (s *SearchService) StartMissingSearch(mediaType, mode string) bool {
	s.mu.Lock()
	if s.missing == nil {
		s.missing = map[string]*MissingSearch{}
	}
	if m, ok := s.missing[mediaType+":"+mode]; ok && m.Running {
		s.mu.Unlock()
		return false
	}
	progress := &MissingSearch{MediaType: mediaType, Mode: mode, Running: true}
	s.missing[mediaType+":"+mode] = progress
	s.mu.Unlock()

	update := func(change func(*MissingSearch)) {
		s.mu.Lock()
		change(progress)
		s.mu.Unlock()
	}
	go func() {
		ctx := context.Background()
		err := s.searchAllMissing(ctx, mediaType, mode, update)
		update(func(m *MissingSearch) {
			m.Running, m.Finished = false, s.now()
			if err != nil {
				m.LastError = err.Error()
			}
		})
		if err != nil {
			log.Printf("search all missing %s: %v", mediaType, err)
		}
	}()
	return true
}

func (s *SearchService) searchAllMissing(ctx context.Context, mediaType, mode string, update func(func(*MissingSearch))) error {
	engine, err := decision.Load(ctx, s.DB, true)
	if err != nil {
		return err
	}
	step := func(report SearchReport) {
		update(func(m *MissingSearch) { m.Done++; m.Grabbed += len(report.Grabbed) })
	}
	switch mediaType {
	case newznab.MediaMovie:
		movies, err := store.ListWantedMovies(ctx, s.DB)
		if err != nil {
			return err
		}
		movies = wantedMovies(movies, mode, engine.Profiles)
		update(func(m *MissingSearch) { m.Total = len(movies) })
		for _, movie := range movies {
			var report SearchReport
			if !movie.Queued {
				s.searchMovie(ctx, engine, movie, &report)
			}
			step(report)
		}
	case newznab.MediaSeries:
		series, err := store.ListWantedSeries(ctx, s.DB)
		if err != nil {
			return err
		}
		series = wantedSeries(series, mode, engine.Profiles)
		update(func(m *MissingSearch) { m.Total = len(series) })
		for _, show := range series {
			var report SearchReport
			s.searchSeries(ctx, engine, show, nil, &report)
			step(report)
		}
	case newznab.MediaMusic:
		albums, err := s.albumsToSearch(ctx, mode, engine.Profiles)
		if err != nil {
			return err
		}
		update(func(m *MissingSearch) { m.Total = len(albums) })
		for _, album := range albums {
			var report SearchReport
			if !album.Queued {
				s.searchAlbum(ctx, engine, album, &report)
			}
			step(report)
		}
	default:
		return fmt.Errorf("unknown media type %q", mediaType)
	}
	return nil
}

// Recent fetches the newest releases from every RSS-enabled indexer, in all
// of its categories.
func (s *IndexerService) Recent(ctx context.Context) SearchResult {
	return s.run(ctx, PurposeRSS, "", func(ctx context.Context, c *newznab.Client, _ newznab.Caps, _ bool, cats []int) ([]newznab.Release, error) {
		return c.Recent(ctx, cats)
	})
}

// RSSDue reports whether RSS sync should run: its interval is set and has
// passed since the last run.
func (s *SearchService) RSSDue(ctx context.Context) (bool, error) {
	settings, err := store.GetIndexerSettings(ctx, s.DB)
	if err != nil {
		return false, err
	}
	if settings.RSSSyncInterval <= 0 {
		return false, nil
	}
	if !settings.LastRSSSync.Valid {
		return true, nil
	}
	return s.now().Sub(settings.LastRSSSync.Time) >= time.Duration(settings.RSSSyncInterval)*time.Minute, nil
}

// ErrRSSSyncRunning is returned when RSS sync is asked to start while it runs.
var ErrRSSSyncRunning = fmt.Errorf("RSS sync is already running")

// mediaTypesOf lists the media types a release's standard categories belong
// to; none means it could be anything.
func mediaTypesOf(r newznab.Release) map[string]bool {
	types := map[string]bool{}
	for _, c := range r.Categories {
		if t := newznab.MediaTypeOf(c); t != "" {
			types[t] = true
		}
	}
	return types
}

// RSSSync is Radarr's RSS sync: read every RSS-enabled indexer's newest
// releases, match them to monitored items that are missing, judge them
// (monitoring and availability included) and grab the best for each item.
func (s *SearchService) RSSSync(ctx context.Context) (SearchReport, error) {
	s.mu.Lock()
	if s.rssBusy {
		s.mu.Unlock()
		return SearchReport{}, ErrRSSSyncRunning
	}
	s.rssBusy = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.rssBusy = false; s.mu.Unlock() }()

	started := s.now()
	report, err := s.rssSync(ctx)
	summary := report.Summary()
	if err != nil {
		summary = "Failed: " + err.Error()
	}
	if recordErr := store.RecordRSSSync(ctx, s.DB, started, summary); recordErr != nil && err == nil {
		err = recordErr
	}
	return report, err
}

func (s *SearchService) rssSync(ctx context.Context) (SearchReport, error) {
	var report SearchReport
	result := s.Indexers.Recent(ctx)
	report.Searched, report.Releases, report.Errors = result.Searched, len(result.Releases), result.Errors
	if len(result.Releases) == 0 {
		return report, nil
	}
	engine, err := decision.Load(ctx, s.DB, false)
	if err != nil {
		return report, err
	}
	movies, err := store.ListWantedMovies(ctx, s.DB)
	if err != nil {
		return report, err
	}
	series, err := store.ListWantedSeries(ctx, s.DB)
	if err != nil {
		return report, err
	}
	albums, err := store.ListWantedAlbums(ctx, s.DB)
	if err != nil {
		return report, err
	}

	movieReleases := map[int][]newznab.Release{}
	seriesReleases := map[int][]newznab.Release{}
	albumReleases := map[int][]newznab.Release{}
	for _, r := range result.Releases {
		types := mediaTypesOf(r)
		any := len(types) == 0
		if any || types[newznab.MediaSeries] {
			if info, ok := releaseparse.ParseEpisode(r.Title); ok {
				for i, show := range series {
					if decision.MatchSeriesTitle(show, info) {
						seriesReleases[i] = append(seriesReleases[i], r)
						break
					}
				}
				continue
			}
		}
		matched := false
		if any || types[newznab.MediaMovie] {
			for i, movie := range movies {
				if ok, _ := decision.MatchMovie(movie, r); ok {
					movieReleases[i] = append(movieReleases[i], r)
					matched = true
					break
				}
			}
		}
		if !matched && (any || types[newznab.MediaMusic]) {
			for i, album := range albums {
				if decision.MatchAlbum(album, r.Title) {
					albumReleases[i] = append(albumReleases[i], r)
					break
				}
			}
		}
	}

	var decided []decision.Decision
	for i, releases := range movieReleases {
		movie := movies[i]
		decisions := engine.Movie(movie, releases)
		decided = append(decided, decisions...)
		grabBest(decisions, &report, func(d decision.Decision) error {
			_, err := s.Download.GrabMovie(ctx, movie.ID, d.Release, PurposeRSS)
			return err
		})
	}
	for i, releases := range seriesReleases {
		decisions := engine.Series(series[i], decision.SeriesScope{}, releases)
		decided = append(decided, decisions...)
		s.grabEpisodes(ctx, series[i].ID, decisions, PurposeRSS, map[int64]bool{}, &report)
	}
	for i, releases := range albumReleases {
		album := albums[i]
		decisions := engine.Album(album, releases)
		decided = append(decided, decisions...)
		grabBest(decisions, &report, func(d decision.Decision) error {
			_, err := s.Download.GrabAlbum(ctx, album.ID, d.Release, PurposeRSS)
			return err
		})
	}
	grabbed, errs := report.Grabbed, report.GrabErrors
	report.add(SearchResult{}, decided)
	report.Grabbed, report.GrabErrors = grabbed, errs
	if len(grabbed) > 0 {
		titles := make([]string, len(grabbed))
		for i, g := range grabbed {
			titles[i] = g.Release
		}
		log.Printf("rss sync grabbed: %s", strings.Join(titles, "; "))
	}
	return report, nil
}

// Search all modes: Missing wants items without a file, Cutoff the ones
// whose file is below the profile's cutoff (Radarr's Wanted pages).
const (
	SearchMissing = "missing"
	SearchCutoff  = "cutoff"
)

func wantedMovies(movies []store.WantedMovie, mode string, profiles decision.Profiles) []store.WantedMovie {
	out := movies[:0]
	for _, m := range movies {
		switch mode {
		case SearchCutoff:
			if m.HasFile && profiles.CutoffUnmet(m.QualityProfileID, m.FileQuality) {
				out = append(out, m)
			}
		default:
			if !m.HasFile {
				out = append(out, m)
			}
		}
	}
	return out
}

func wantedSeries(series []store.WantedSeries, mode string, profiles decision.Profiles) []store.WantedSeries {
	out := series[:0]
	for _, s := range series {
		keep := false
		for _, ep := range s.Episodes {
			if !ep.Monitored {
				continue
			}
			if mode == SearchCutoff {
				keep = ep.HasFile && profiles.CutoffUnmet(s.QualityProfileID, ep.FileQuality)
			} else {
				keep = !ep.HasFile
			}
			if keep {
				break
			}
		}
		if keep {
			out = append(out, s)
		}
	}
	return out
}

// albumsToSearch is the music half of wantedMovies/wantedSeries: the
// albums a missing or cutoff-unmet search should look at. Missing means no
// files at all; cutoff means the album has files, but at least one of them
// is below its profile's cutoff, so an upgrade is worth searching for.
func (s *SearchService) albumsToSearch(ctx context.Context, mode string, profiles decision.Profiles) ([]store.WantedAlbum, error) {
	if mode != SearchCutoff {
		return store.ListWantedAlbums(ctx, s.DB)
	}
	albums, err := store.ListUpgradableAlbums(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	var out []store.WantedAlbum
	for _, album := range albums {
		qualities, err := store.AlbumFileQualities(ctx, s.DB, album.ID)
		if err != nil {
			return nil, err
		}
		for _, q := range qualities {
			if profiles.CutoffUnmet(album.QualityProfileID, q) {
				out = append(out, album)
				break
			}
		}
	}
	return out, nil
}
