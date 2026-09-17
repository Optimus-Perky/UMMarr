package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/musicbrainz"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

func fixMatchTMDB(t *testing.T) *tmdb.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/movie":
			json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"id": 603, "title": "The Matrix", "release_date": "1999-03-31"}}})
		case "/movie/603":
			json.NewEncoder(w).Encode(map[string]any{"id": 603, "title": "The Matrix", "release_date": "1999-03-31", "external_ids": map[string]any{"imdb_id": "tt0133093"}})
		case "/search/tv":
			json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"id": 1396, "name": "Breaking Bad", "first_air_date": "2008-01-20"}}})
		case "/tv/1396":
			json.NewEncoder(w).Encode(map[string]any{"id": 1396, "name": "Breaking Bad", "first_air_date": "2008-01-20", "status": "Ended",
				"seasons": []map[string]any{{"season_number": 1, "episode_count": 2, "name": "Season 1"}}, "external_ids": map[string]any{"imdb_id": "tt0903747"}})
		case "/tv/1396/season/1":
			json.NewEncoder(w).Encode(map[string]any{"season_number": 1, "name": "Season 1", "episodes": []map[string]any{
				{"id": 62085, "episode_number": 1, "season_number": 1, "name": "Pilot", "air_date": "2008-01-20"},
				{"id": 62086, "episode_number": 2, "season_number": 1, "name": "Cat's in the Bag...", "air_date": "2008-01-27"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return tmdb.New(tmdb.Options{Token: "test", BaseURL: srv.URL})
}

// TestFixMatch_Movie: the dialog searches TMDB, and applying a result
// re-points the movie at it while its file and folder stay.
func TestFixMatch_Movie(t *testing.T) {
	db := openTestDB(t)
	client := fixMatchTMDB(t)
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, TMDB: client, Movies: &sync.MovieService{DB: db, TMDB: client}}))
	t.Cleanup(srv.Close)
	movieID := seedTestMovie(t, db)
	if _, err := store.InsertMovieFile(t.Context(), db, movieID, "Inception (2010).mkv", 5); err != nil {
		t.Fatal(err)
	}
	before, _, _ := store.GetMovieDetail(t.Context(), db, movieID)

	_, body := get(t, srv, "/movies/"+itoa(movieID)+"/fix-match")
	if !strings.Contains(body, "Fix match - Inception") || !strings.Contains(body, `hx-get="/movies/`+itoa(movieID)+`/fix-match"`) {
		t.Fatalf("want the Fix match dialog, got:\n%s", body)
	}
	_, body = get(t, srv, "/movies/"+itoa(movieID)+"/fix-match?q=matrix")
	if !strings.Contains(body, "The Matrix") || !strings.Contains(body, `hx-vals='{"id":"603"}'`) {
		t.Fatalf("want TMDB results with Use this, got:\n%s", body)
	}
	resp, _ := postForm(t, srv, "/movies/"+itoa(movieID)+"/fix-match", url.Values{"id": {"603"}})
	if resp.Header.Get("HX-Redirect") != "/movies/"+itoa(movieID) {
		t.Fatalf("want a redirect to the movie, got %q", resp.Header.Get("HX-Redirect"))
	}
	after, _, _ := store.GetMovieDetail(t.Context(), db, movieID)
	if after.Title != "The Matrix" || after.Path != before.Path || after.File == nil || after.File.RelativePath != "Inception (2010).mkv" {
		t.Fatalf("want The Matrix with the same folder and file, got %+v", after)
	}
	movies, _ := store.ListMovies(t.Context(), db)
	if len(movies) != 1 {
		t.Fatalf("want still one movie, got %d", len(movies))
	}

	// Re-applying the same match is a no-op.
	resp, body = postForm(t, srv, "/movies/"+itoa(movieID)+"/fix-match", url.Values{"id": {"603"}})
	if resp.Header.Get("HX-Redirect") == "" {
		t.Fatalf("want re-applying the same match to succeed, got:\n%s", body)
	}
}

// TestFixMatch_Series: episodes without files are replaced by the new
// title's, episodes with files stay.
func TestFixMatch_Series(t *testing.T) {
	db := openTestDB(t)
	client := fixMatchTMDB(t)
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, TMDB: client, Series: &sync.SeriesService{DB: db, TMDB: client}}))
	t.Cleanup(srv.Close)
	seriesID := seedTestSeries(t, db)
	var filedEpisode int64
	if err := db.QueryRow(`SELECT id FROM episodes WHERE series_id = ? ORDER BY season_number, episode_number LIMIT 1`, seriesID).Scan(&filedEpisode); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO episode_files (episode_id, relative_path, size) VALUES (?, 'Season 01/ep.mkv', 1)`, filedEpisode); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE episodes SET episode_file_id = (SELECT id FROM episode_files WHERE episode_id = ?) WHERE id = ?`, filedEpisode, filedEpisode); err != nil {
		t.Fatal(err)
	}

	resp, body := postForm(t, srv, "/tv/"+itoa(seriesID)+"/fix-match", url.Values{"id": {"1396"}})
	if resp.Header.Get("HX-Redirect") != "/tv/"+itoa(seriesID) {
		t.Fatalf("want a redirect to the series, got %q:\n%s", resp.Header.Get("HX-Redirect"), body)
	}
	detail, _, _ := store.GetSeriesDetail(t.Context(), db, seriesID)
	if detail.Title != "Breaking Bad" {
		t.Fatalf("want the series retitled, got %+v", detail)
	}
	var titles []string
	rows, _ := db.Query(`SELECT title FROM episodes WHERE series_id = ? ORDER BY season_number, episode_number`, seriesID)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		titles = append(titles, s)
	}
	rows.Close()
	if strings.Join(titles, "|") != "Pilot|Cat's in the Bag..." {
		t.Fatalf("want the new title's episodes (the filed one updated in place), got %v", titles)
	}
	var stillFiled int
	db.QueryRow(`SELECT COUNT(*) FROM episodes WHERE id = ? AND episode_file_id IS NOT NULL`, filedEpisode).Scan(&stillFiled)
	if stillFiled != 1 {
		t.Fatalf("want the episode with a file kept")
	}
}

// TestFixMatch_ArtistDialog: the artist dialog searches MusicBrainz and
// applying re-points the artist and its albums.
func TestFixMatch_Artist(t *testing.T) {
	db := openTestDB(t)
	mb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/artist":
			json.NewEncoder(w).Encode(map[string]any{"artists": []map[string]any{{"id": "b7ffd2af-418f-4be2-bdd1-22f8b48613da", "name": "Nine Inch Nails", "type": "Group"}}})
		case "/artist/b7ffd2af-418f-4be2-bdd1-22f8b48613da":
			json.NewEncoder(w).Encode(map[string]any{"id": "b7ffd2af-418f-4be2-bdd1-22f8b48613da", "name": "Nine Inch Nails", "sort-name": "Nine Inch Nails", "type": "Group"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(mb.Close)
	client, err := musicbrainz.New(musicbrainz.Options{UserAgent: "test/1.0", BaseURL: mb.URL})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, MusicBrainz: client, Music: &sync.MusicService{DB: db, MusicBrainz: client}}))
	t.Cleanup(srv.Close)
	seedTestAlbum(t, db)
	var artistID int64
	if err := db.QueryRow(`SELECT id FROM artists LIMIT 1`).Scan(&artistID); err != nil {
		t.Fatal(err)
	}

	_, body := get(t, srv, "/music/artists/"+itoa(artistID)+"/fix-match?q=nine")
	if !strings.Contains(body, "Nine Inch Nails") {
		t.Fatalf("want MusicBrainz results, got:\n%s", body)
	}
	resp, body := postForm(t, srv, "/music/artists/"+itoa(artistID)+"/fix-match", url.Values{"id": {"b7ffd2af-418f-4be2-bdd1-22f8b48613da"}})
	if resp.Header.Get("HX-Redirect") != "/music" {
		t.Fatalf("want a redirect, got:\n%s", body)
	}
	var name string
	db.QueryRow(`SELECT am.name FROM artists a JOIN artist_metadata am ON am.id = a.artist_metadata_id WHERE a.id = ?`, artistID).Scan(&name)
	if name != "Nine Inch Nails" {
		t.Fatalf("want the artist renamed, got %q", name)
	}
}

// TestFixMatch_Album: the album takes the new release group's details and
// track listing; its old unfiled tracks go.
func TestFixMatch_Album(t *testing.T) {
	db := openTestDB(t)
	mb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release-group":
			json.NewEncoder(w).Encode(map[string]any{"release-groups": []map[string]any{{"id": "discovery-rg", "title": "Discovery", "primary-type": "Album", "first-release-date": "2001-03-12"}}})
		case "/release-group/discovery-rg":
			json.NewEncoder(w).Encode(map[string]any{"id": "discovery-rg", "title": "Discovery", "primary-type": "Album", "first-release-date": "2001-03-12",
				"releases": []map[string]any{{"id": "discovery-rel", "title": "Discovery", "status": "Official", "date": "2001-03-12"}}})
		case "/release/discovery-rel":
			json.NewEncoder(w).Encode(map[string]any{"id": "discovery-rel", "title": "Discovery", "status": "Official", "date": "2001-03-12",
				"media": []map[string]any{{"position": 1, "track-count": 2, "tracks": []map[string]any{
					{"id": "t1", "number": "1", "title": "One More Time", "length": 320000},
					{"id": "t2", "number": "2", "title": "Aerodynamic", "length": 212000},
				}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(mb.Close)
	client, err := musicbrainz.New(musicbrainz.Options{UserAgent: "test/1.0", BaseURL: mb.URL})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, MusicBrainz: client, Music: &sync.MusicService{DB: db, MusicBrainz: client}}))
	t.Cleanup(srv.Close)
	albumID := seedTestAlbum(t, db)

	_, body := get(t, srv, "/music/albums/"+itoa(albumID)+"/fix-match?q=discovery")
	if !strings.Contains(body, "Discovery") || !strings.Contains(body, `hx-vals='{"id":"discovery-rg"}'`) {
		t.Fatalf("want release group results, got:\n%s", body)
	}
	resp, body := postForm(t, srv, "/music/albums/"+itoa(albumID)+"/fix-match", url.Values{"id": {"discovery-rg"}})
	if resp.Header.Get("HX-Redirect") != "/music/albums/"+itoa(albumID) {
		t.Fatalf("want a redirect to the album, got:\n%s", body)
	}
	var title string
	db.QueryRow(`SELECT title FROM albums WHERE id = ?`, albumID).Scan(&title)
	var tracks []string
	rows, _ := db.Query(`SELECT t.title FROM tracks t JOIN album_releases r ON r.id = t.album_release_id WHERE r.album_id = ? ORDER BY t.track_number`, albumID)
	for rows.Next() {
		var s string
		rows.Scan(&s)
		tracks = append(tracks, s)
	}
	rows.Close()
	if title != "Discovery" || strings.Join(tracks, "|") != "One More Time|Aerodynamic" {
		t.Fatalf("want Discovery with its own tracks, got %q %v", title, tracks)
	}
	albums, _ := store.ListAlbums(t.Context(), db)
	if len(albums) != 1 {
		t.Fatalf("want still one album, got %d", len(albums))
	}
}
