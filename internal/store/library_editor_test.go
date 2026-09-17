package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func seedMovieTitled(t *testing.T, db interface {
	store.Queryer
}, title string, tmdb string, profile, root int64) int64 {
	t.Helper()
	ctx := context.Background()
	meta, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{Title: metadata.Field[string]{Value: title, Provider: "tmdb"}, Year: metadata.Field[int]{Value: 2010, Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": tmdb}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.UpsertMovie(ctx, db, meta, profile, root, true)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestEditLibrary(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	root := seedRootFolder(t, db, "movie")
	profile := seedQualityProfile(t, db)
	hd, _ := store.CreateQualityProfile(ctx, db, "HD")
	m1 := seedMovieTitled(t, db, "Inception", "27205", profile, root)
	m2 := seedMovieTitled(t, db, "Heat", "949", profile, root)

	off, avail := false, "inCinemas"
	err := store.EditMovies(ctx, db, []int64{m1, m2}, store.LibraryEdit{Monitored: &off, QualityProfileID: &hd, MinimumAvailability: &avail, TagMode: "add", Tags: []string{"4K", " kids "}})
	if err != nil {
		t.Fatal(err)
	}
	check := func(id int64, monitored bool, profileID int64, availability, tags string) {
		t.Helper()
		var gotMon bool
		var gotProfile int64
		var gotAvail, gotTags string
		db.QueryRow(`SELECT monitored, quality_profile_id, minimum_availability, tags FROM movies WHERE id = ?`, id).Scan(&gotMon, &gotProfile, &gotAvail, &gotTags)
		labels, _ := store.TagLabels(ctx, db, gotTags)
		if gotMon != monitored || gotProfile != profileID || gotAvail != availability || strings.Join(labels, ",") != tags {
			t.Errorf("movie %d: got monitored=%v profile=%d availability=%q tags=%s", id, gotMon, gotProfile, gotAvail, gotTags)
		}
	}
	check(m1, false, hd, "inCinemas", "4k,kids")
	check(m2, false, hd, "inCinemas", "4k,kids")

	if err := store.EditMovies(ctx, db, []int64{m1}, store.LibraryEdit{TagMode: "remove", Tags: []string{"KIDS", "nope"}}); err != nil {
		t.Fatal(err)
	}
	check(m1, false, hd, "inCinemas", "4k")
	var tagRows int
	db.QueryRow(`SELECT COUNT(*) FROM tags`).Scan(&tagRows)
	if tagRows != 2 {
		t.Errorf("removing a tag that doesn't exist mustn't create it, got %d tags", tagRows)
	}
	on := true
	if err := store.EditMovies(ctx, db, []int64{m2}, store.LibraryEdit{Monitored: &on, TagMode: "replace", Tags: []string{"uhd"}}); err != nil {
		t.Fatal(err)
	}
	check(m2, true, hd, "inCinemas", "uhd")
	if err := store.EditMovies(ctx, db, []int64{m2}, store.LibraryEdit{}); err != nil {
		t.Fatalf("an edit that changes nothing is fine: %v", err)
	}
	check(m2, true, hd, "inCinemas", "uhd")
	bad := "preDB"
	if err := store.EditMovies(ctx, db, []int64{m1}, store.LibraryEdit{MinimumAvailability: &bad}); err == nil {
		t.Error("want an unknown availability refused")
	}
	if err := store.EditMovies(ctx, db, []int64{m1}, store.LibraryEdit{TagMode: "swap"}); err == nil {
		t.Error("want an unknown tag mode refused")
	}

	seriesRoot := seedRootFolder(t, db, "series")
	meta, _ := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{Title: metadata.Field[string]{Value: "Show", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "1"}})
	sid, _ := store.UpsertSeries(ctx, db, meta, profile, seriesRoot, true)
	anime, noFolder := "anime", false
	if err := store.EditSeries(ctx, db, []int64{sid}, store.LibraryEdit{SeriesType: &anime, SeasonFolder: &noFolder, Monitored: &off, MinimumAvailability: &avail}); err != nil {
		t.Fatalf("a series ignores minimum availability: %v", err)
	}
	s, _ := store.GetSeriesSettings(ctx, db, sid)
	if s.SeriesType != "anime" || s.SeasonFolder || s.Monitored {
		t.Errorf("series edit: %+v", s)
	}
	weird := "cartoon"
	if err := store.EditSeries(ctx, db, []int64{sid}, store.LibraryEdit{SeriesType: &weird}); err == nil {
		t.Error("want an unknown series type refused")
	}
	if err := store.SetRootFolder(ctx, db, "series", sid, seriesRoot, "/media/series/Show (2020)"); err != nil {
		t.Fatal(err)
	}
	if sd, _, _ := store.GetSeriesDetail(ctx, db, sid); sd.Path.String != "/media/series/Show (2020)" {
		t.Errorf("want the new path, got %q", sd.Path.String)
	}
	if err := store.SetRootFolder(ctx, db, "albums", 1, 1, "/x"); err == nil {
		t.Error("want an unknown table refused")
	}
}

func TestSeasonPassAndLibrarySummaries(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	root := seedRootFolder(t, db, "series")
	profile := seedQualityProfile(t, db)
	add := func(title, tmdb string) int64 {
		meta, _ := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{Title: metadata.Field[string]{Value: title, Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": tmdb}})
		id, _ := store.UpsertSeries(ctx, db, meta, profile, root, true)
		return id
	}
	show := add("The Show", "1")
	alpha := add("Alpha", "2")
	db.Exec(`UPDATE series_metadata SET overview = 'A show about shows.', network = 'AMC', year = 2008 WHERE title = 'The Show'`)
	for _, n := range []int{0, 1, 2} {
		seasonID, _ := store.UpsertSeason(ctx, db, show, metadata.SeasonMetadata{SeasonNumber: n, Monitored: true})
		for ep := 1; ep <= 2; ep++ {
			eid, _ := store.UpsertEpisode(ctx, db, show, seasonID, n, metadata.EpisodeMetadata{EpisodeNumber: ep, Title: metadata.Field[string]{Value: "Ep", Provider: "tmdb"}})
			if n == 1 && ep == 1 {
				store.AttachEpisodeFile(ctx, db, eid, "Season 01/ep.mkv", 1000)
			}
		}
	}
	store.UpdateSeasonMonitored(ctx, db, show, 2, false)

	pass, err := store.ListSeasonPass(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(pass) != 2 || pass[0].ID != alpha || pass[1].ID != show || len(pass[0].Seasons) != 0 {
		t.Fatalf("want Alpha then The Show (The ignored), got %+v", pass)
	}
	seasons := pass[1].Seasons
	if len(seasons) != 3 || seasons[1] != (store.SeasonPassSeason{Number: 1, Monitored: true, Total: 2, Downloaded: 1}) || seasons[2].Monitored {
		t.Fatalf("seasons: %+v", seasons)
	}

	list, _ := store.ListSeries(ctx, db)
	var got store.SeriesSummary
	for _, s := range list {
		if s.ID == show {
			got = s
		}
	}
	if got.Overview != "A show about shows." || got.Network != "AMC" || got.Year.Int64 != 2008 || got.SeasonCount != 2 || got.SizeOnDisk != 1000 {
		t.Fatalf("summary: %+v", got)
	}

	movieRoot := seedRootFolder(t, db, "movie")
	m := seedMovieTitled(t, db, "Inception", "27205", profile, movieRoot)
	db.Exec(`UPDATE movie_metadata SET overview = 'Dreams within dreams.'`)
	movies, _ := store.ListMovies(ctx, db)
	if len(movies) != 1 || movies[0].ID != m || movies[0].Overview != "Dreams within dreams." {
		t.Fatalf("movie summary: %+v", movies)
	}
}
