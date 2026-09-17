// Exported entrypoints taking real provider client response types,
// letting callers outside this package (the sync service) actually invoke
// a merge - the source-adapter machinery in adapt_*.go stays unexported,
// this file is the only bridge to it.
package merge

import (
	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/musicbrainz"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/omdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tvdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tvmaze"
)

// MergeMovieFromProviders merges a TMDB movie (required) with an OMDb
// response (optional - pass nil when no OMDb key is configured or the
// lookup failed) into one MovieMetadata.
func MergeMovieFromProviders(tmdbMovie *tmdb.Movie, omdbResp *omdb.Response, opts Options) (metadata.MovieMetadata, []metadata.Provenance, []metadata.ExternalID) {
	var sources []movieSource
	if tmdbMovie != nil {
		sources = append(sources, adaptTMDBMovie(tmdbMovie))
	}
	if omdbResp != nil {
		sources = append(sources, adaptOMDbMovie(omdbResp))
	}
	return MergeMovie(sources, opts)
}

// MergeSeriesFromProviders merges a TMDB series (required) with a TVMaze
// show (optional - pass nil when no TVMaze match was found) into one
// SeriesMetadata.
func MergeSeriesFromProviders(tmdbSeries *tmdb.Series, tvmazeShow *tvmaze.Show, tvdbSeries *tvdb.Series, opts Options) (metadata.SeriesMetadata, []metadata.Provenance, []metadata.ExternalID) {
	var sources []seriesSource
	if tmdbSeries != nil {
		sources = append(sources, adaptTMDBSeries(tmdbSeries))
	}
	if tvmazeShow != nil {
		tvmazeSource, _ := adaptTVMazeSeries(tvmazeShow, nil)
		sources = append(sources, tvmazeSource)
	}
	if tvdbSeries != nil {
		tvdbSource, _ := adaptTVDBSeries(tvdbSeries, nil)
		sources = append(sources, tvdbSource)
	}
	return MergeSeries(sources, opts)
}

// MergeSeasonsFromProviders reconciles TMDB's per-season episode lists
// (fetched one call per season via Client.GetSeason - pass each result
// here) with TVMaze's flat episode list (optional - pass nil when no
// TVMaze match was found) into one season/episode list per
// internal/metadata/merge/seasons.go's union strategy.
func MergeSeasonsFromProviders(tmdbSeasons []tmdb.Season, tvmazeEpisodes []tvmaze.Episode, tvdbEpisodes []tvdb.Episode, opts Options) ([]metadata.SeasonMetadata, metadata.MergeReport) {
	var sourceLists [][]seasonSource
	if len(tmdbSeasons) > 0 {
		var tmdbSources []seasonSource
		for _, season := range tmdbSeasons {
			tmdbSources = append(tmdbSources, adaptTMDBSeason(season.SeasonNumber, &season))
		}
		sourceLists = append(sourceLists, tmdbSources)
	}
	if len(tvmazeEpisodes) > 0 {
		sourceLists = append(sourceLists, groupTVMazeEpisodesBySeason(tvmazeEpisodes))
	}
	if len(tvdbEpisodes) > 0 {
		sourceLists = append(sourceLists, groupTVDBEpisodesBySeason(tvdbEpisodes))
	}
	return MergeSeasons(sourceLists, opts)
}

// MergeArtistFromProvider merges a MusicBrainz artist into one
// ArtistMetadata. MusicBrainz is the only artist provider wired up so far
// (see the metadata-provider-layer plan) - this still goes through the
// same Merge/priority-table machinery as the other media types so adding
// a second source (e.g. TheAudioDB) later is additive, not a rewrite.
func MergeArtistFromProvider(mbArtist *musicbrainz.Artist) (metadata.ArtistMetadata, []metadata.Provenance, []metadata.ExternalID) {
	if mbArtist == nil {
		return MergeArtist(nil)
	}
	return MergeArtist([]artistSource{adaptMusicBrainzArtist(mbArtist)})
}

// MergeAlbumFromProvider merges a MusicBrainz release-group into one
// AlbumMetadata, including its compilation-series membership if present
// (see adaptMusicBrainzAlbum).
func MergeAlbumFromProvider(mbReleaseGroup *musicbrainz.ReleaseGroup) (metadata.AlbumMetadata, []metadata.Provenance, []metadata.ExternalID) {
	if mbReleaseGroup == nil {
		return MergeAlbum(nil)
	}
	return MergeAlbum([]albumSource{adaptMusicBrainzAlbum(mbReleaseGroup)})
}

// MergeReleaseFromProvider merges a MusicBrainz release into one
// ReleaseMetadata plus its full track list (each track carrying its own
// artist credits).
func MergeReleaseFromProvider(mbRelease *musicbrainz.Release) (metadata.ReleaseMetadata, []metadata.TrackSource, []metadata.ExternalID) {
	if mbRelease == nil {
		merged, _, externalIDs := MergeRelease(nil)
		return merged, nil, externalIDs
	}
	src, tracks := adaptMusicBrainzRelease(mbRelease)
	merged, _, externalIDs := MergeRelease([]releaseSource{src})
	return merged, tracks, externalIDs
}
