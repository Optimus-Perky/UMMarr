package sync

import (
	"context"
	"fmt"
	"log"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// RefreshReport counts what a library-wide metadata refresh did.
type RefreshReport struct {
	Movies, Series, Failed int
}

// Summary is the one-line report for the log and the Tasks page.
func (r RefreshReport) Summary() string {
	return fmt.Sprintf("refreshed %d movies and %d series, %d failed", r.Movies, r.Series, r.Failed)
}

// RefreshLibrary re-fetches every movie's and series' metadata from the
// providers, as Radarr's Refresh Movie and Sonarr's Refresh Series tasks do:
// new episodes, changed titles, posters and season posters arrive this
// way. One failure doesn't stop the rest; the first few are logged.
func RefreshLibrary(ctx context.Context, movies *MovieService, series *SeriesService) (RefreshReport, error) {
	var report RefreshReport
	logged := 0
	fail := func(what string, id int64, err error) {
		report.Failed++
		if logged < 5 {
			logged++
			log.Printf("refresh metadata: %s %d: %v", what, id, err)
		}
	}
	if movies != nil {
		list, err := store.ListMovies(ctx, movies.DB)
		if err != nil {
			return report, err
		}
		for _, m := range list {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			if err := movies.Refresh(ctx, m.ID); err != nil {
				fail("movie", m.ID, err)
				continue
			}
			report.Movies++
		}
	}
	if series != nil {
		list, err := store.ListSeries(ctx, series.DB)
		if err != nil {
			return report, err
		}
		for _, s := range list {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			if err := series.Refresh(ctx, s.ID); err != nil {
				fail("series", s.ID, err)
				continue
			}
			report.Series++
		}
	}
	return report, nil
}
