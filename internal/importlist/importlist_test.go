package importlist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

func fakeTMDB(t *testing.T) *tmdb.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/list/8":
			json.NewEncoder(w).Encode(map[string]any{"page": 1, "total_pages": 1, "items": []map[string]any{
				{"id": 603, "media_type": "movie", "title": "The Matrix", "release_date": "1999-03-31"},
				{"id": 1396, "media_type": "tv", "name": "Breaking Bad"},
				{"id": 27205, "media_type": "movie", "title": "Inception", "release_date": "2010-07-16"},
			}})
		case r.URL.Path == "/movie/popular":
			json.NewEncoder(w).Encode(map[string]any{"page": 1, "total_pages": 1, "results": []map[string]any{{"id": 603, "title": "The Matrix"}, {"id": 27205, "title": "Inception"}}})
		case strings.HasPrefix(r.URL.Path, "/find/tt0133093"):
			json.NewEncoder(w).Encode(map[string]any{"movie_results": []map[string]any{{"id": 603, "title": "The Matrix", "release_date": "1999-03-31"}}})
		case r.URL.Path == "/movie/603":
			json.NewEncoder(w).Encode(map[string]any{"id": 603, "title": "The Matrix", "release_date": "1999-03-31", "external_ids": map[string]any{"imdb_id": "tt0133093"}})
		case r.URL.Path == "/movie/27205":
			json.NewEncoder(w).Encode(map[string]any{"id": 27205, "title": "Inception", "release_date": "2010-07-16", "external_ids": map[string]any{"imdb_id": "tt1375666"}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return tmdb.New(tmdb.Options{Token: "test", BaseURL: srv.URL})
}

func TestSyncTMDBList(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store.CreateRootFolder(ctx, db, dir, "movie")
	store.CreateQualityProfile(ctx, db, "Any")
	store.AddExclusion(ctx, db, store.Exclusion{MediaType: "movie", TMDBID: 27205, Title: "Inception"})
	client := fakeTMDB(t)
	events := &sync.Events{DB: db}
	s := &Service{DB: db, TMDB: client, Movies: &sync.MovieService{DB: db, TMDB: client}, Events: events}
	list := store.ImportList{Name: "My list", Implementation: store.ListTMDBList, Enabled: true, MediaType: "movie", Settings: map[string]string{"list_id": "8"}, Monitored: true, AutoAdd: true}
	id, err := store.CreateImportList(ctx, db, list)
	if err != nil {
		t.Fatal(err)
	}
	list.ID = id

	items, err := s.Preview(ctx, list)
	if err != nil || len(items) != 2 || items[0].Title != "The Matrix" || !items[1].Excluded {
		t.Fatalf("preview: %+v %v", items, err)
	}
	r := s.SyncList(ctx, list)
	if r.Err != nil || r.Added != 1 || r.Excluded != 1 {
		t.Fatalf("sync: %+v", r)
	}
	movies, _ := store.ListMovies(ctx, db)
	if len(movies) != 1 || movies[0].Title != "The Matrix" {
		t.Fatalf("want The Matrix added, got %+v", movies)
	}
	if r := s.SyncList(ctx, list); r.Added != 0 || r.Tracked != 1 {
		t.Fatalf("want the second sync to add nothing, got %+v", r)
	}
	saved, _ := store.GetImportList(ctx, db, id)
	if !saved.LastSync.Valid || !strings.Contains(saved.LastResult, "0 added, 1 already") {
		t.Fatalf("want the sync recorded, got %+v", saved)
	}
	events2, _, _ := store.ListHistory(ctx, db, store.HistoryFilter{})
	if len(events2) != 1 || events2[0].Source != "import list" || events2[0].Title != "The Matrix (1999)" {
		t.Fatalf("want an added event from the list, got %+v", events2)
	}

	// Popular needs no id; a Plex feed resolves IMDb ids through TMDB.
	popular, err := s.Fetch(ctx, store.ImportList{Implementation: store.ListTMDBPopular, MediaType: "movie", Settings: map[string]string{"limit": "1"}})
	if err != nil || len(popular) != 1 {
		t.Fatalf("popular: %+v %v", popular, err)
	}
	rss := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<rss><channel><item><title>The Matrix</title><category>movie</category><guid>imdb://tt0133093</guid></item><item><title>A Show</title><category>show</category><guid>tmdb://1396</guid></item></channel></rss>`))
	}))
	defer rss.Close()
	plex, err := s.Fetch(ctx, store.ImportList{Implementation: store.ListPlexRSS, MediaType: "movie", Settings: map[string]string{"url": rss.URL}})
	if err != nil || len(plex) != 1 || plex[0].TMDBID != 603 {
		t.Fatalf("plex: %+v %v", plex, err)
	}
}
