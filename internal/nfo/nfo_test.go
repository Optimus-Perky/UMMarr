package nfo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestWriteMovie(t *testing.T) {
	images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("jpeg:" + r.URL.Path)) }))
	defer images.Close()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	root, _ := store.CreateRootFolder(ctx, db, dir, "movie")
	profile, _ := store.CreateQualityProfile(ctx, db, "Any")
	metaID, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
		Title: metadata.Field[string]{Value: "Inception", Provider: "tmdb"}, Year: metadata.Field[int]{Value: 2010, Provider: "tmdb"},
		Overview: metadata.Field[string]{Value: "A thief who steals secrets.", Provider: "tmdb"}, Genres: metadata.Field[[]string]{Value: []string{"Action", "Sci-Fi"}, Provider: "tmdb"},
		Ratings: map[string]float64{"tmdb": 8.4}, Images: metadata.Field[[]string]{Value: []string{images.URL + "/poster.jpg", images.URL + "/backdrop.jpg"}, Provider: "tmdb"},
		ExternalIDs: map[string]string{"tmdb": "27205", "imdb": "tt1375666"},
	})
	if err != nil {
		t.Fatal(err)
	}
	movieID, err := store.UpsertMovie(ctx, db, metaID, profile, root, true)
	if err != nil {
		t.Fatal(err)
	}
	d, _, _ := store.GetMovieDetail(ctx, db, movieID)
	os.MkdirAll(d.Path.String, 0o755)
	store.InsertMovieFile(ctx, db, movieID, "Inception (2010).mkv", 1)

	w := &Writer{DB: db}
	if err := w.WriteMovie(ctx, movieID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d.Path.String, "Inception (2010).nfo")); !os.IsNotExist(err) {
		t.Fatal("want nothing written while metadata is off")
	}
	store.UpdateMetadataSettings(ctx, db, store.MetadataSettings{Enabled: true, MovieNFO: true, MovieImages: true})
	if err := w.WriteMovie(ctx, movieID); err != nil {
		t.Fatal(err)
	}
	nfoData, err := os.ReadFile(filepath.Join(d.Path.String, "Inception (2010).nfo"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(nfoData)
	for _, want := range []string{"<movie>", "<title>Inception</title>", "<year>2010</year>", "<genre>Sci-Fi</genre>", `<uniqueid type="tmdb" default="true">27205</uniqueid>`, `<uniqueid type="imdb">tt1375666</uniqueid>`, `<rating name="tmdb" max="10" default="true">`, "<plot>A thief who steals secrets.</plot>"} {
		if !strings.Contains(body, want) {
			t.Errorf("want %s in:\n%s", want, body)
		}
	}
	poster, _ := os.ReadFile(filepath.Join(d.Path.String, "poster.jpg"))
	fanart, _ := os.ReadFile(filepath.Join(d.Path.String, "fanart.jpg"))
	if string(poster) != "jpeg:/poster.jpg" || string(fanart) != "jpeg:/backdrop.jpg" {
		t.Fatalf("want the images downloaded, got %q %q", poster, fanart)
	}
}

func TestMarshalEpisode(t *testing.T) {
	data, err := Marshal(Episode{Title: "Pilot", ShowTitle: "Breaking Bad", Season: 1, Episode: 1, Aired: "2008-01-20"})
	if err != nil || !strings.HasPrefix(string(data), "<?xml") || !strings.Contains(string(data), "<episodedetails>") || !strings.Contains(string(data), "<season>1</season>") {
		t.Fatalf("got %s (%v)", data, err)
	}
}

func TestWriteSeriesProviders(t *testing.T) {
	images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("jpeg:" + r.URL.Path)) }))
	defer images.Close()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	root, _ := store.CreateRootFolder(ctx, db, dir, "series")
	profile, _ := store.CreateQualityProfile(ctx, db, "Any")
	metaID, err := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{
		Title: metadata.Field[string]{Value: "Breaking Bad", Provider: "tmdb"}, Images: metadata.Field[[]string]{Value: []string{images.URL + "/show.jpg"}, Provider: "tmdb"},
		ExternalIDs: map[string]string{"tmdb": "1396"},
	})
	if err != nil {
		t.Fatal(err)
	}
	seriesID, err := store.UpsertSeries(ctx, db, metaID, profile, root, true)
	if err != nil {
		t.Fatal(err)
	}
	seasonID, _ := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: 1, Poster: images.URL + "/s1.jpg"})
	episodeID, _ := store.UpsertEpisode(ctx, db, seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: 1, Title: metadata.Field[string]{Value: "Pilot", Provider: "tmdb"}})
	d, _, _ := store.GetSeriesDetail(ctx, db, seriesID)
	seasonDir := filepath.Join(d.Path.String, "Season 01")
	os.MkdirAll(seasonDir, 0o755)
	store.AttachEpisodeFile(ctx, db, episodeID, "Season 01/Breaking Bad - S01E01 - Pilot.mkv", 1)
	read := func(name string) string { b, _ := os.ReadFile(filepath.Join(d.Path.String, name)); return string(b) }
	w := &Writer{DB: db}

	// Plex: images only, season poster inside the season folder.
	store.UpdateMetadataSettings(ctx, db, store.MetadataSettings{Plex: true, SeriesNFO: true, EpisodeNFO: true, SeriesImages: true})
	if err := w.WriteSeries(ctx, seriesID); err != nil {
		t.Fatal(err)
	}
	if read("poster.jpg") != "jpeg:/show.jpg" || read("Season 01/poster.jpg") != "jpeg:/s1.jpg" {
		t.Fatalf("want Plex posters, got %q %q", read("poster.jpg"), read("Season 01/poster.jpg"))
	}
	for _, name := range []string{"tvshow.nfo", "season01-poster.jpg", "Season 01/season.nfo", "Season 01/Breaking Bad - S01E01 - Pilot.nfo"} {
		if read(name) != "" {
			t.Fatalf("Plex must not get %s", name)
		}
	}

	// Jellyfin: Kodi-style nfo plus seasonNN-poster.jpg and season.nfo.
	os.RemoveAll(filepath.Join(d.Path.String, "poster.jpg"))
	store.UpdateMetadataSettings(ctx, db, store.MetadataSettings{Jellyfin: true, SeriesNFO: true, EpisodeNFO: true, SeriesImages: true})
	if err := w.WriteSeries(ctx, seriesID); err != nil {
		t.Fatal(err)
	}
	if read("season01-poster.jpg") != "jpeg:/s1.jpg" || read("poster.jpg") != "jpeg:/show.jpg" {
		t.Fatalf("want Jellyfin season poster, got %q", read("season01-poster.jpg"))
	}
	if !strings.Contains(read("tvshow.nfo"), "<title>Breaking Bad</title>") || !strings.Contains(read("Season 01/season.nfo"), "<seasonnumber>1</seasonnumber>") || !strings.Contains(read("Season 01/Breaking Bad - S01E01 - Pilot.nfo"), "<title>Pilot</title>") {
		t.Fatalf("want Jellyfin nfo files, got tvshow=%q season=%q", read("tvshow.nfo"), read("Season 01/season.nfo"))
	}
}

func TestWriteAlbumProviders(t *testing.T) {
	images := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("jpeg:" + r.URL.Path)) }))
	defer images.Close()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	root, _ := store.CreateRootFolder(ctx, db, dir, "music")
	profile, _ := store.CreateQualityProfile(ctx, db, "Any")
	artistMetaID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name: metadata.Field[string]{Value: "Muse", Provider: "musicbrainz"}, Overview: metadata.Field[string]{Value: "Rock band from Devon.", Provider: "musicbrainz"},
		Images: metadata.Field[[]string]{Value: []string{images.URL + "/muse.jpg"}, Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "9c9f1380-2516-4fc9-a3e6-f9f61941d090"},
	})
	if err != nil {
		t.Fatal(err)
	}
	artistID, err := store.UpsertArtist(ctx, db, artistMetaID, profile, root, true)
	if err != nil {
		t.Fatal(err)
	}
	artistDir := filepath.Join(dir, "Muse")
	albumDir := filepath.Join(artistDir, "Absolution")
	os.MkdirAll(albumDir, 0o755)
	store.SetArtistPath(ctx, db, artistID, artistDir)
	albumID, _, err := store.UpsertAlbum(ctx, db, artistMetaID, metadata.AlbumMetadata{
		Title: metadata.Field[string]{Value: "Absolution", Provider: "musicbrainz"}, Images: metadata.Field[[]string]{Value: []string{images.URL + "/absolution.jpg"}, Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "rg-absolution"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.SetAlbumPathTo(ctx, db, albumID, albumDir)
	read := func(name string) string { b, _ := os.ReadFile(filepath.Join(artistDir, name)); return string(b) }
	w := &Writer{DB: db}

	store.UpdateMetadataSettings(ctx, db, store.MetadataSettings{Plex: true, AlbumNFO: true, AlbumImages: true})
	if err := w.WriteAlbum(ctx, albumID); err != nil {
		t.Fatal(err)
	}
	if read("Absolution/folder.jpg") != "jpeg:/absolution.jpg" || read("folder.jpg") != "jpeg:/muse.jpg" {
		t.Fatalf("want Plex folder.jpg files, got %q %q", read("Absolution/folder.jpg"), read("folder.jpg"))
	}
	if read("Absolution/cover.jpg") != "" || read("Absolution/album.nfo") != "" || read("artist.nfo") != "" {
		t.Fatal("Plex must not get cover.jpg or nfo files")
	}

	store.UpdateMetadataSettings(ctx, db, store.MetadataSettings{Jellyfin: true, AlbumNFO: true, AlbumImages: true})
	if err := w.WriteAlbum(ctx, albumID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read("artist.nfo"), "<name>Muse</name>") || !strings.Contains(read("artist.nfo"), "<biography>Rock band from Devon.</biography>") || !strings.Contains(read("artist.nfo"), `<uniqueid type="musicbrainz" default="true">9c9f1380`) {
		t.Fatalf("want artist.nfo, got %q", read("artist.nfo"))
	}
	if !strings.Contains(read("Absolution/album.nfo"), "<title>Absolution</title>") {
		t.Fatalf("want album.nfo, got %q", read("Absolution/album.nfo"))
	}
}
