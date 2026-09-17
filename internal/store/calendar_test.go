package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestListCalendar(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	profile := seedQualityProfile(t, db)
	day := func(s string) time.Time {
		d, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	// A series with an episode inside the month and one outside it.
	seriesMeta, _ := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{Title: metadata.Field[string]{Value: "The Show", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "1"}})
	seriesID, _ := store.UpsertSeries(ctx, db, seriesMeta, profile, seedRootFolder(t, db, "series"), true)
	seasonID, _ := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: 1, Monitored: true})
	inMonth, outOfMonth := day("2026-09-17"), day("2026-10-02")
	store.UpsertEpisode(ctx, db, seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: 5, Title: metadata.Field[string]{Value: "Pilot", Provider: "tmdb"}, AirDate: metadata.Field[*time.Time]{Value: &inMonth, Provider: "tmdb"}})
	store.UpsertEpisode(ctx, db, seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: 6, Title: metadata.Field[string]{Value: "Later", Provider: "tmdb"}, AirDate: metadata.Field[*time.Time]{Value: &outOfMonth, Provider: "tmdb"}})

	// A movie with a digital and a physical release in the month.
	cinemas, digital := day("2026-03-01"), day("2026-09-04")
	physical := day("2026-09-25")
	movieMeta, _ := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
		Title: metadata.Field[string]{Value: "Some Film", Provider: "tmdb"}, Year: metadata.Field[int]{Value: 2026, Provider: "tmdb"},
		InCinemas: metadata.Field[*time.Time]{Value: &cinemas, Provider: "tmdb"}, DigitalRelease: metadata.Field[*time.Time]{Value: &digital, Provider: "tmdb"},
		PhysicalRelease: metadata.Field[*time.Time]{Value: &physical, Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "7"},
	})
	store.UpsertMovie(ctx, db, movieMeta, profile, seedRootFolder(t, db, "movie"), true)

	// An album released in the month.
	artistMeta, _ := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{Name: metadata.Field[string]{Value: "The Band", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "b1"}})
	store.UpsertArtist(ctx, db, artistMeta, profile, seedRootFolder(t, db, "music"), true)
	released := day("2026-09-11")
	store.UpsertAlbum(ctx, db, artistMeta, metadata.AlbumMetadata{
		Title: metadata.Field[string]{Value: "New Record", Provider: "musicbrainz"}, ReleaseDate: metadata.Field[*time.Time]{Value: &released, Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "rg1"},
	})

	entries, err := store.ListCalendar(ctx, db, day("2026-09-01"), day("2026-09-30"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("want the digital and physical releases, the album and the one episode, got %d: %+v", len(entries), entries)
	}
	want := []struct{ kind, title, label, date string }{
		{"movie", "Some Film", "Digital", "2026-09-04"},
		{"music", "The Band", "Album", "2026-09-11"},
		{"series", "The Show", "1x05", "2026-09-17"},
		{"movie", "Some Film", "Physical", "2026-09-25"},
	}
	for i, w := range want {
		got := entries[i]
		if got.Kind != w.kind || got.Title != w.title || got.Label != w.label || got.Date.Format("2006-01-02") != w.date {
			t.Errorf("entry %d: got %s %s %s on %s, want %s %s %s on %s", i, got.Kind, got.Title, got.Label, got.Date.Format("2006-01-02"), w.kind, w.title, w.label, w.date)
		}
	}
	if entries[2].Subtitle != "Pilot" || entries[2].URL != "/tv/the-show" || !entries[2].Monitored || entries[2].HasFile {
		t.Errorf("episode entry: %+v", entries[2])
	}
	if entries[1].Subtitle != "New Record" || entries[1].URL != "/music/albums/the-band/new-record" {
		t.Errorf("album entry: %+v", entries[1])
	}
	if entries[0].URL != "/movies/some-film-2026" {
		t.Errorf("movie entry: %+v", entries[0])
	}
	// A window with nothing in it comes back empty, not an error.
	if none, err := store.ListCalendar(ctx, db, day("2027-01-01"), day("2027-01-31")); err != nil || len(none) != 0 {
		t.Errorf("want nothing in an empty month, got %d (%v)", len(none), err)
	}
}
