package merge

import (
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/musicbrainz"
)

// TestAdaptMusicBrainzAlbum_SeriesExtraction confirms the release_group-series
// relationship parsing that populates compilation_series - the mechanism
// behind grouping Various Artists compilations like "Now That's What I
// Call Music" by franchise (migration 00008).
func TestAdaptMusicBrainzAlbum_SeriesExtraction(t *testing.T) {
	rg := &musicbrainz.ReleaseGroup{
		ID:          "release-group-mbid",
		Title:       "Now That's What I Call Music! 50",
		PrimaryType: "Album",
		Relations: []musicbrainz.Relation{
			{TargetType: "artist"}, // unrelated relation, should be ignored
			{
				TargetType: "series",
				Type:       "part of",
				Series: &musicbrainz.SeriesTarget{
					ID:   "series-mbid",
					Name: "Now That's What I Call Music",
				},
				AttributeValues: map[string]string{"number": "50"},
			},
		},
	}

	src := adaptMusicBrainzAlbum(rg)
	if src.Series == nil {
		t.Fatalf("want series info extracted, got nil")
	}
	if src.Series.Name != "Now That's What I Call Music" {
		t.Fatalf("want series name 'Now That's What I Call Music', got %s", src.Series.Name)
	}
	if src.Series.MusicBrainzSeriesID != "series-mbid" {
		t.Fatalf("want series mbid 'series-mbid', got %s", src.Series.MusicBrainzSeriesID)
	}
	if src.Series.SequenceNumber != 50 {
		t.Fatalf("want sequence number 50, got %d", src.Series.SequenceNumber)
	}
}

// TestAdaptMusicBrainzAlbum_NoSeries confirms a normal (non-compilation)
// release-group with no series relations gets nil Series, not a
// zero-valued struct that would look like a bogus match.
func TestAdaptMusicBrainzAlbum_NoSeries(t *testing.T) {
	rg := &musicbrainz.ReleaseGroup{ID: "mbid", Title: "Discovery", PrimaryType: "Album"}
	src := adaptMusicBrainzAlbum(rg)
	if src.Series != nil {
		t.Fatalf("want no series for a normal album, got %+v", src.Series)
	}
}

// TestMergeAlbum_CarriesSeries confirms MergeAlbum passes the
// MusicBrainz-derived series info through into the merged AlbumMetadata
// unchanged, since it's not a competing field to merge across providers.
func TestMergeAlbum_CarriesSeries(t *testing.T) {
	rg := &musicbrainz.ReleaseGroup{
		ID:             "mbid",
		Title:          "Now That's What I Call Music! 50",
		PrimaryType:    "Album",
		SecondaryTypes: []string{"Compilation"},
		Relations: []musicbrainz.Relation{{
			TargetType:      "series",
			Series:          &musicbrainz.SeriesTarget{ID: "series-mbid", Name: "Now That's What I Call Music"},
			AttributeValues: map[string]string{"number": "50"},
		}},
	}
	merged, _, _ := MergeAlbum([]albumSource{adaptMusicBrainzAlbum(rg)})
	if merged.Series == nil || merged.Series.Name != "Now That's What I Call Music" {
		t.Fatalf("want merged album to carry series info, got %+v", merged.Series)
	}
	if merged.AlbumType.Value != "Album" || merged.AlbumType.Provider != "musicbrainz" {
		t.Fatalf("want album_type from musicbrainz, got %+v", merged.AlbumType)
	}
}
