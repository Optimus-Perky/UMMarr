// Package sync ties the metadata-provider layer (internal/metadata,
// internal/metadata/merge) and persistence layer (internal/store)
// together: fetch from providers, merge, persist, all inside one
// transaction per Add/Refresh call.
package sync

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/merge"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/omdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// MovieService adds/refreshes movies. OMDb is nilable - callers without
// an OMDb key configured just pass nil and get TMDB-only merges.
type MovieService struct {
	DB   *sql.DB
	TMDB *tmdb.Client
	OMDb *omdb.Client
}

// AddByTMDBID fetches a movie from TMDB (and OMDb, once TMDB reveals its
// IMDb id, if OMDb is configured), merges them, and persists both the
// movie_metadata and per-instance movies row in one transaction.
func (s *MovieService) AddByTMDBID(ctx context.Context, tmdbID int, rootFolderID, qualityProfileID int64) (int64, error) {
	tmdbMovie, err := s.TMDB.GetMovie(ctx, tmdbID)
	if err != nil {
		return 0, fmt.Errorf("fetch tmdb movie %d: %w", tmdbID, err)
	}

	omdbResp := s.fetchOMDbByIMDbID(ctx, tmdbMovie.ExternalIDs)
	merged, _, _ := merge.MergeMovieFromProviders(tmdbMovie, omdbResp, mergeOptions(ctx, s.DB))

	var movieID int64
	err = store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		metadataID, err := store.UpsertMovieMetadata(ctx, tx, merged)
		if err != nil {
			return err
		}
		movieID, err = store.UpsertMovie(ctx, tx, metadataID, qualityProfileID, rootFolderID, true)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("add movie (tmdb %d): %w", tmdbID, err)
	}
	return movieID, nil
}

// Refresh re-fetches and re-merges a previously-added movie's metadata
// using its existing TMDB external id, calling the exact same upsert
// path AddByTMDBID does - idempotent by construction, not a separate
// code path that could drift from Add's behavior.
func (s *MovieService) Refresh(ctx context.Context, movieID int64) error {
	var metadataID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT movie_metadata_id FROM movies WHERE id = ?`, movieID).Scan(&metadataID); err != nil {
		return fmt.Errorf("find movie %d: %w", movieID, err)
	}
	tmdbIDStr, found, err := store.GetExternalID(ctx, s.DB, "movie", metadataID, "tmdb")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("movie %d has no tmdb external id to refresh from", movieID)
	}
	tmdbID, err := strconv.Atoi(tmdbIDStr)
	if err != nil {
		return fmt.Errorf("movie %d has a non-numeric tmdb external id %q: %w", movieID, tmdbIDStr, err)
	}

	tmdbMovie, err := s.TMDB.GetMovie(ctx, tmdbID)
	if err != nil {
		return fmt.Errorf("fetch tmdb movie %d: %w", tmdbID, err)
	}
	omdbResp := s.fetchOMDbByIMDbID(ctx, tmdbMovie.ExternalIDs)
	merged, _, _ := merge.MergeMovieFromProviders(tmdbMovie, omdbResp, mergeOptions(ctx, s.DB))

	return store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		_, err := store.UpsertMovieMetadata(ctx, tx, merged)
		return err
	})
}

func (s *MovieService) fetchOMDbByIMDbID(ctx context.Context, ids *tmdb.ExternalIDs) *omdb.Response {
	if s.OMDb == nil || ids == nil || ids.IMDbID == "" {
		return nil
	}
	resp, err := s.OMDb.GetByIMDbID(ctx, ids.IMDbID)
	if err != nil {
		// OMDb is supplementary - a failed/missing lookup shouldn't block
		// adding the movie with TMDB data alone.
		return nil
	}
	return resp
}

// FixMatch points movieID at TMDB movie tmdbID instead, keeping its folder,
// file, monitoring and profile.
func (s *MovieService) FixMatch(ctx context.Context, movieID int64, tmdbID int) error {
	tmdbMovie, err := s.TMDB.GetMovie(ctx, tmdbID)
	if err != nil {
		return fmt.Errorf("fetch tmdb movie %d: %w", tmdbID, err)
	}
	omdbResp := s.fetchOMDbByIMDbID(ctx, tmdbMovie.ExternalIDs)
	merged, _, _ := merge.MergeMovieFromProviders(tmdbMovie, omdbResp, mergeOptions(ctx, s.DB))
	return store.WithTx(ctx, s.DB, func(tx *sql.Tx) error {
		metadataID, err := store.UpsertMovieMetadata(ctx, tx, merged)
		if err != nil {
			return err
		}
		return store.RelinkMovieMetadata(ctx, tx, movieID, metadataID)
	})
}
