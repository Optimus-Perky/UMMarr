package sync

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/merge"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tvdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tvmaze"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// mergeOptions reads Settings -> Metadata -> Sources into merge priorities.
// It falls back to the built-in order when the setting can't be read.
func mergeOptions(ctx context.Context, db *sql.DB) merge.Options {
	if db == nil {
		return merge.Options{}
	}
	orders, err := store.GetMetadataSourceOrders(ctx, db)
	if err != nil {
		return merge.Options{}
	}
	return merge.OptionsFromOrder(merge.ProviderOrder{Movie: orders["movie"], Series: orders["series"]})
}

// RefreshAlbum re-fetches an album's release group from MusicBrainz. Files
// stay put.
func (s *MusicService) RefreshAlbum(ctx context.Context, albumID int64) error {
	mbid, found, err := store.GetExternalID(ctx, s.DB, "album", albumID, "musicbrainz")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("album %d has no MusicBrainz id to refresh from", albumID)
	}
	return s.FixMatchAlbum(ctx, albumID, mbid)
}

// tvdbClient builds a TheTVDB client from the saved provider row, or returns
// nil when TheTVDB isn't set up.
func tvdbClient(ctx context.Context, db *sql.DB, userAgent string) *tvdb.Client {
	p, ok := store.EnabledMetadataProvider(ctx, db, "tvdb")
	if !ok || p.APIKey == "" {
		return nil
	}
	client, err := tvdb.New(tvdb.Options{APIKey: p.APIKey, PIN: p.Pin(), UserAgent: userAgent})
	if err != nil {
		return nil
	}
	return client
}

// fetchTVDBBestEffort looks a series up at TheTVDB - by the id the library
// already has, otherwise by name - and reads its episodes. Anything that
// fails leaves TheTVDB out of the merge rather than failing the refresh.
func (s *SeriesService) fetchTVDBBestEffort(ctx context.Context, tmdbSeries *tmdb.Series, tvmazeShow *tvmaze.Show) (*tvdb.Series, []tvdb.Episode) {
	client := tvdbClient(ctx, s.DB, s.UserAgent)
	if client == nil || tmdbSeries == nil {
		return nil, nil
	}
	// TVmaze carries the series' TheTVDB id; it's how the library got the
	// ones it already has.
	id := 0
	if tvmazeShow != nil && tvmazeShow.Externals.TheTVDB != nil {
		id = *tvmazeShow.Externals.TheTVDB
	}
	if id == 0 {
		results, err := client.SearchSeries(ctx, tmdbSeries.Name)
		if err != nil || len(results) == 0 {
			return nil, nil
		}
		n, err := strconv.Atoi(strings.TrimPrefix(results[0].ID, "series-"))
		if err != nil {
			return nil, nil
		}
		id = n
	}
	series, err := client.GetSeries(ctx, id)
	if err != nil {
		log.Printf("thetvdb series %d: %v", id, err)
		return nil, nil
	}
	episodes, err := client.GetEpisodes(ctx, id)
	if err != nil {
		log.Printf("thetvdb episodes %d: %v", id, err)
	}
	return series, episodes
}
