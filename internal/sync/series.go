package sync

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/merge"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tvmaze"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// SeriesService adds/refreshes TV series, including their season/episode
// lists.
type SeriesService struct {
	DB        *sql.DB
	TMDB      *tmdb.Client
	UserAgent string
	TVMaze    *tvmaze.Client
}

// AddByTMDBID fetches a series from TMDB, every one of its seasons'
// episode lists (one TMDB call per season - the base series response only
// lists season stubs), and a best-effort TVMaze match, merges them, and
// persists the series/seasons/episodes rows in one transaction.
func (s *SeriesService) AddByTMDBID(ctx context.Context, tmdbID int, rootFolderID, qualityProfileID int64) (int64, error) {
	tmdbSeries, err := s.TMDB.GetSeries(ctx, tmdbID)
	if err != nil {
		return 0, fmt.Errorf("fetch tmdb series %d: %w", tmdbID, err)
	}

	tmdbSeasons, err := s.fetchAllSeasons(ctx, tmdbID, tmdbSeries)
	if err != nil {
		return 0, err
	}
	tvmazeShow, tvmazeEpisodes := s.fetchTVMazeBestEffort(ctx, tmdbSeries)
	tvdbSeries, tvdbEpisodes := s.fetchTVDBBestEffort(ctx, tmdbSeries, tvmazeShow)

	mergedSeries, _, _ := merge.MergeSeriesFromProviders(tmdbSeries, tvmazeShow, tvdbSeries, mergeOptions(ctx, s.DB))
	mergedSeasons, _ := merge.MergeSeasonsFromProviders(tmdbSeasons, tvmazeEpisodes, tvdbEpisodes, mergeOptions(ctx, s.DB))

	var seriesID int64
	err = store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		metadataID, err := store.UpsertSeriesMetadata(ctx, tx, mergedSeries)
		if err != nil {
			return err
		}
		seriesID, err = store.UpsertSeries(ctx, tx, metadataID, qualityProfileID, rootFolderID, true)
		if err != nil {
			return err
		}
		for _, season := range mergedSeasons {
			seasonID, err := store.UpsertSeason(ctx, tx, seriesID, season)
			if err != nil {
				return err
			}
			for _, episode := range season.Episodes {
				if _, err := store.UpsertEpisode(ctx, tx, seriesID, seasonID, season.SeasonNumber, episode); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("add series (tmdb %d): %w", tmdbID, err)
	}
	return seriesID, nil
}

// Refresh re-fetches and re-merges a previously-added series (and its
// seasons/episodes) using its existing TMDB external id, calling the
// exact same upsert path AddByTMDBID does.
func (s *SeriesService) Refresh(ctx context.Context, seriesID int64) error {
	var metadataID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT series_metadata_id FROM series WHERE id = ?`, seriesID).Scan(&metadataID); err != nil {
		return fmt.Errorf("find series %d: %w", seriesID, err)
	}
	tmdbIDStr, found, err := store.GetExternalID(ctx, s.DB, "series", metadataID, "tmdb")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("series %d has no tmdb external id to refresh from", seriesID)
	}
	tmdbID, err := strconv.Atoi(tmdbIDStr)
	if err != nil {
		return fmt.Errorf("series %d has a non-numeric tmdb external id %q: %w", seriesID, tmdbIDStr, err)
	}

	tmdbSeries, err := s.TMDB.GetSeries(ctx, tmdbID)
	if err != nil {
		return fmt.Errorf("fetch tmdb series %d: %w", tmdbID, err)
	}
	tmdbSeasons, err := s.fetchAllSeasons(ctx, tmdbID, tmdbSeries)
	if err != nil {
		return err
	}
	tvmazeShow, tvmazeEpisodes := s.fetchTVMazeBestEffort(ctx, tmdbSeries)
	tvdbSeries, tvdbEpisodes := s.fetchTVDBBestEffort(ctx, tmdbSeries, tvmazeShow)

	mergedSeries, _, _ := merge.MergeSeriesFromProviders(tmdbSeries, tvmazeShow, tvdbSeries, mergeOptions(ctx, s.DB))
	mergedSeasons, _ := merge.MergeSeasonsFromProviders(tmdbSeasons, tvmazeEpisodes, tvdbEpisodes, mergeOptions(ctx, s.DB))

	return store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		if _, err := store.UpsertSeriesMetadata(ctx, tx, mergedSeries); err != nil {
			return err
		}
		for _, season := range mergedSeasons {
			seasonID, err := store.UpsertSeason(ctx, tx, seriesID, season)
			if err != nil {
				return err
			}
			for _, episode := range season.Episodes {
				if _, err := store.UpsertEpisode(ctx, tx, seriesID, seasonID, season.SeasonNumber, episode); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *SeriesService) fetchAllSeasons(ctx context.Context, tmdbID int, series *tmdb.Series) ([]tmdb.Season, error) {
	seasons := make([]tmdb.Season, 0, len(series.Seasons))
	for _, summary := range series.Seasons {
		season, err := s.TMDB.GetSeason(ctx, tmdbID, summary.SeasonNumber)
		if err != nil {
			return nil, fmt.Errorf("fetch tmdb season %d for series %d: %w", summary.SeasonNumber, tmdbID, err)
		}
		seasons = append(seasons, *season)
	}
	return seasons, nil
}

// fetchTVMazeBestEffort finds a TVMaze match via the IMDb id TMDB already
// gave us, falling back to a name search. TVMaze is a supplementary
// source here - if no match is found, the series still syncs from TMDB
// alone rather than failing the whole add.
func (s *SeriesService) fetchTVMazeBestEffort(ctx context.Context, tmdbSeries *tmdb.Series) (*tvmaze.Show, []tvmaze.Episode) {
	if s.TVMaze == nil {
		return nil, nil
	}

	var show *tvmaze.Show
	if tmdbSeries.ExternalIDs != nil && tmdbSeries.ExternalIDs.IMDbID != "" {
		if found, err := s.TVMaze.LookupByIMDb(ctx, tmdbSeries.ExternalIDs.IMDbID); err == nil {
			show = found
		}
	}
	if show == nil {
		results, err := s.TVMaze.SearchShows(ctx, tmdbSeries.Name)
		if err == nil && len(results) > 0 {
			show = &results[0].Show
		}
	}
	if show == nil {
		return nil, nil
	}

	episodes, err := s.TVMaze.GetEpisodes(ctx, show.ID)
	if err != nil {
		return show, nil
	}
	return show, episodes
}

// FixMatch points seriesID at TMDB series tmdbID instead: its folder, files
// and settings stay, episodes without files are replaced by the new title's.
func (s *SeriesService) FixMatch(ctx context.Context, seriesID int64, tmdbID int) error {
	tmdbSeries, err := s.TMDB.GetSeries(ctx, tmdbID)
	if err != nil {
		return fmt.Errorf("fetch tmdb series %d: %w", tmdbID, err)
	}
	tmdbSeasons, err := s.fetchAllSeasons(ctx, tmdbID, tmdbSeries)
	if err != nil {
		return err
	}
	tvmazeShow, tvmazeEpisodes := s.fetchTVMazeBestEffort(ctx, tmdbSeries)
	tvdbSeries, tvdbEpisodes := s.fetchTVDBBestEffort(ctx, tmdbSeries, tvmazeShow)
	mergedSeries, _, _ := merge.MergeSeriesFromProviders(tmdbSeries, tvmazeShow, tvdbSeries, mergeOptions(ctx, s.DB))
	mergedSeasons, _ := merge.MergeSeasonsFromProviders(tmdbSeasons, tvmazeEpisodes, tvdbEpisodes, mergeOptions(ctx, s.DB))

	return store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		metadataID, err := store.UpsertSeriesMetadata(ctx, tx, mergedSeries)
		if err != nil {
			return err
		}
		if err := store.RelinkSeriesMetadata(ctx, tx, seriesID, metadataID); err != nil {
			return err
		}
		for _, season := range mergedSeasons {
			seasonID, err := store.UpsertSeason(ctx, tx, seriesID, season)
			if err != nil {
				return err
			}
			for _, episode := range season.Episodes {
				if _, err := store.UpsertEpisode(ctx, tx, seriesID, seasonID, season.SeasonNumber, episode); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
