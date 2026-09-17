package merge

import (
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/musicbrainz"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/omdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tvmaze"
)

func TestMergeMovieFromProviders(t *testing.T) {
	tmdbMovie := &tmdb.Movie{
		ID: 27205, Title: "Inception", ReleaseDate: "2010-07-15",
		ProductionCompanies: []tmdb.ProductionCompany{{Name: "Legendary Pictures"}, {Name: "Syncopy"}},
		BelongsToCollection: &tmdb.Collection{Name: "Inception Collection"},
	}
	merged, _, externalIDs := MergeMovieFromProviders(tmdbMovie, nil, Options{})
	if merged.Studio.Value != "Legendary Pictures" {
		t.Fatalf("want studio 'Legendary Pictures', got %q", merged.Studio.Value)
	}
	if merged.CollectionTitle.Value != "Inception Collection" {
		t.Fatalf("want collection title set, got %q", merged.CollectionTitle.Value)
	}
	if len(externalIDs) != 1 || externalIDs[0].Provider != "tmdb" {
		t.Fatalf("want only tmdb external id with nil omdb, got %v", externalIDs)
	}

	// Nil TMDB movie shouldn't panic - just an empty merge.
	empty, _, _ := MergeMovieFromProviders(nil, &omdb.Response{Title: "X", Response: "True"}, Options{})
	if empty.Title.Provider != "omdb" {
		t.Fatalf("want omdb-only merge to still work, got %+v", empty.Title)
	}
}

func TestMergeSeriesFromProviders(t *testing.T) {
	tmdbSeries := &tmdb.Series{
		ID: 1396, Name: "Breaking Bad",
		Networks:       []tmdb.Network{{Name: "AMC"}},
		EpisodeRunTime: []int{47},
	}
	imdb := "tt0903747"
	tvmazeShow := &tvmaze.Show{ID: 169, Name: "Breaking Bad", Externals: tvmaze.Externals{IMDb: &imdb}}

	merged, _, externalIDs := MergeSeriesFromProviders(tmdbSeries, tvmazeShow, nil, Options{})
	if merged.Network.Value != "AMC" || merged.Network.Provider != "tmdb" {
		t.Fatalf("want network AMC from tmdb, got %+v", merged.Network)
	}
	if merged.Runtime.Value != 47 {
		t.Fatalf("want runtime 47, got %d", merged.Runtime.Value)
	}
	idsByProvider := map[string]string{}
	for _, id := range externalIDs {
		idsByProvider[id.Provider] = id.ExternalID
	}
	if idsByProvider["tmdb"] == "" || idsByProvider["tvmaze"] == "" || idsByProvider["imdb"] != imdb {
		t.Fatalf("want tmdb+tvmaze+imdb external ids, got %v", idsByProvider)
	}
}

func TestMergeSeasonsFromProviders(t *testing.T) {
	tmdbSeasons := []tmdb.Season{
		{SeasonNumber: 1, Episodes: []tmdb.Episode{{EpisodeNumber: 1, Name: "Pilot"}}},
	}
	tvmazeEpisodes := []tvmaze.Episode{
		{Season: 1, Number: 1, Name: "Pilot TVMaze", Airdate: "2008-01-20"},
	}
	seasons, _ := MergeSeasonsFromProviders(tmdbSeasons, tvmazeEpisodes, nil, Options{})
	if len(seasons) != 1 || len(seasons[0].Episodes) != 1 {
		t.Fatalf("want 1 season with 1 unioned episode, got %+v", seasons)
	}
}

func TestMergeArtistAndAlbumFromProvider(t *testing.T) {
	artist := &musicbrainz.Artist{ID: "mbid-artist", Name: "Daft Punk", Type: "Group"}
	mergedArtist, _, ids := MergeArtistFromProvider(artist)
	if mergedArtist.Name.Value != "Daft Punk" || mergedArtist.Name.Provider != "musicbrainz" {
		t.Fatalf("want artist name from musicbrainz, got %+v", mergedArtist.Name)
	}
	if len(ids) != 1 || ids[0].ExternalID != "mbid-artist" {
		t.Fatalf("want musicbrainz external id, got %v", ids)
	}

	rg := &musicbrainz.ReleaseGroup{ID: "mbid-rg", Title: "Discovery", PrimaryType: "Album"}
	mergedAlbum, _, _ := MergeAlbumFromProvider(rg)
	if mergedAlbum.Title.Value != "Discovery" {
		t.Fatalf("want album title 'Discovery', got %q", mergedAlbum.Title.Value)
	}

	// nil inputs must not panic
	if _, _, _ = MergeArtistFromProvider(nil); true {
	}
	if _, _, _ = MergeAlbumFromProvider(nil); true {
	}
}

func TestMergeReleaseFromProvider_TrackArtistCredits(t *testing.T) {
	release := &musicbrainz.Release{
		ID: "mbid-release", Title: "Now That's What I Call Music! 50", Status: "Official",
		Date:    "2001-11-19",
		Country: "GB",
		LabelInfo: []musicbrainz.LabelInfo{
			{Label: struct {
				Name string `json:"name"`
			}{Name: "EMI"}},
		},
		Media: []musicbrainz.Medium{{
			Position:   1,
			TrackCount: 2,
			Tracks: []musicbrainz.Track{
				{
					Number: "1", Title: "Angels", Length: 240000,
					ArtistCredit: []musicbrainz.ArtistCredit{{Name: "Robbie Williams", Artist: musicbrainz.Artist{ID: "mbid-robbie", Name: "Robbie Williams"}}},
				},
				{
					Number: "2", Title: "Groovejet", Length: 210000,
					ArtistCredit: []musicbrainz.ArtistCredit{{Name: "Spiller", Artist: musicbrainz.Artist{ID: "mbid-spiller", Name: "Spiller"}}},
				},
			},
		}},
	}

	merged, tracks, _ := MergeReleaseFromProvider(release)
	if merged.Title.Value != "Now That's What I Call Music! 50" {
		t.Fatalf("want release title set, got %q", merged.Title.Value)
	}
	if merged.Label.Value[0] != "EMI" {
		t.Fatalf("want label EMI, got %v", merged.Label.Value)
	}
	if len(tracks) != 2 {
		t.Fatalf("want 2 tracks, got %d", len(tracks))
	}
	if tracks[0].ArtistCredits[0].Name != "Robbie Williams" || tracks[0].ArtistCredits[0].MusicBrainzArtistID != "mbid-robbie" {
		t.Fatalf("want track 1 credited to Robbie Williams, got %+v", tracks[0].ArtistCredits)
	}
	if tracks[1].ArtistCredits[0].Name != "Spiller" {
		t.Fatalf("want track 2 credited to Spiller, got %+v", tracks[1].ArtistCredits)
	}
}
