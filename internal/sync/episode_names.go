package sync

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/merge"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Looking up season and episode names on demand: a season's names are asked
// of every enabled provider in turn, and only the names, summaries, air
// dates and runtimes of episodes the library already has are updated. The
// episode list itself isn't touched, so a provider that numbers a season
// differently can't add or remove episodes here.

// EpisodeNameReport is what a name search found.
type EpisodeNameReport struct {
	Series    string
	Seasons   []int
	Checked   int      // episodes looked at
	Named     int      // episodes that got a real name
	Providers []string // providers that supplied one
	StillTBA  int      // episodes nobody has named yet
}

// Summary is the report in one line.
func (r EpisodeNameReport) Summary() string {
	if r.Checked == 0 {
		return "No episodes to name."
	}
	msg := fmt.Sprintf("Named %d of %d episode(s)", r.Named, r.Checked)
	if len(r.Providers) > 0 {
		msg += " from " + strings.Join(r.Providers, ", ")
	}
	if r.StillTBA > 0 {
		msg += fmt.Sprintf("; %d still have no name at any provider", r.StillTBA)
	}
	return msg + "."
}

// SearchEpisodeNames looks up names for one season, or every season when
// season is nil.
func (s *SeriesService) SearchEpisodeNames(ctx context.Context, seriesID int64, season *int) (EpisodeNameReport, error) {
	var report EpisodeNameReport
	var metadataID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT series_metadata_id FROM series WHERE id = ?`, seriesID).Scan(&metadataID); err != nil {
		return report, fmt.Errorf("find series %d: %w", seriesID, err)
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT title FROM series_metadata WHERE id = ?`, metadataID).Scan(&report.Series); err != nil {
		return report, err
	}
	tmdbIDStr, found, err := store.GetExternalID(ctx, s.DB, "series", metadataID, "tmdb")
	if err != nil || !found {
		return report, fmt.Errorf("%s has no TMDB id to look up", report.Series)
	}
	tmdbID, err := strconv.Atoi(tmdbIDStr)
	if err != nil {
		return report, fmt.Errorf("%s has a non-numeric TMDB id", report.Series)
	}

	tmdbSeries, err := s.TMDB.GetSeries(ctx, tmdbID)
	if err != nil {
		return report, fmt.Errorf("fetch tmdb series %d: %w", tmdbID, err)
	}
	var tmdbSeasons []tmdb.Season
	for _, summary := range tmdbSeries.Seasons {
		if season != nil && summary.SeasonNumber != *season {
			continue
		}
		fetched, err := s.TMDB.GetSeason(ctx, tmdbID, summary.SeasonNumber)
		if err != nil {
			continue
		}
		tmdbSeasons = append(tmdbSeasons, *fetched)
	}
	tvmazeShow, tvmazeEpisodes := s.fetchTVMazeBestEffort(ctx, tmdbSeries)
	_, tvdbEpisodes := s.fetchTVDBBestEffort(ctx, tmdbSeries, tvmazeShow)
	merged, _ := merge.MergeSeasonsFromProviders(tmdbSeasons, tvmazeEpisodes, tvdbEpisodes, mergeOptions(ctx, s.DB))

	providers := map[string]bool{}
	for _, mergedSeason := range merged {
		if season != nil && mergedSeason.SeasonNumber != *season {
			continue
		}
		report.Seasons = append(report.Seasons, mergedSeason.SeasonNumber)
		for _, episode := range mergedSeason.Episodes {
			episodeID, found, err := store.FindEpisode(ctx, s.DB, seriesID, mergedSeason.SeasonNumber, episode.EpisodeNumber)
			if err != nil || !found {
				continue // the library doesn't have this one; naming never adds episodes
			}
			report.Checked++
			if merge.PlaceholderEpisodeTitle(episode.Title.Value) || episode.Title.Value == "" {
				report.StillTBA++
			}
			updated, err := store.UpdateEpisodeDetails(ctx, s.DB, episodeID, episode)
			if err != nil {
				log.Printf("episode names %s S%02dE%02d: %v", report.Series, mergedSeason.SeasonNumber, episode.EpisodeNumber, err)
				continue
			}
			if updated {
				report.Named++
				if episode.Title.Provider != "" {
					providers[episode.Title.Provider] = true
				}
			}
		}
	}
	for _, p := range []string{"tmdb", "tvmaze", "tvdb"} {
		if providers[p] {
			report.Providers = append(report.Providers, providerDisplayName(p))
		}
	}
	return report, nil
}

func providerDisplayName(key string) string {
	switch key {
	case "tmdb":
		return "TMDB"
	case "tvmaze":
		return "TVmaze"
	case "tvdb":
		return "TheTVDB"
	}
	return key
}

// SearchMissingEpisodeNames looks up names for every series that still has
// episodes called "Episode 5" or nothing at all - the cheap version of a
// full refresh, and what the "Find episode names" task runs.
func (s *SeriesService) SearchMissingEpisodeNames(ctx context.Context) (EpisodeNameReport, error) {
	var total EpisodeNameReport
	seriesIDs, err := store.SeriesWithUnnamedEpisodes(ctx, s.DB)
	if err != nil {
		return total, err
	}
	for _, seriesID := range seriesIDs {
		report, err := s.SearchEpisodeNames(ctx, seriesID, nil)
		if err != nil {
			log.Printf("episode names for series %d: %v", seriesID, err)
			continue
		}
		total.Checked += report.Checked
		total.Named += report.Named
		total.StillTBA += report.StillTBA
		for _, p := range report.Providers {
			if !contains(total.Providers, p) {
				total.Providers = append(total.Providers, p)
			}
		}
	}
	total.Series = fmt.Sprintf("%d series", len(seriesIDs))
	return total, nil
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

var _ = sql.ErrNoRows
var _ metadata.EpisodeMetadata
