package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// fakeTMDB knows one movie, 10 Things I Hate About You (1999).
func fakeTMDB(t *testing.T) *tmdb.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search/movie":
			results := []map[string]any{}
			if strings.Contains(strings.ToLower(r.URL.Query().Get("query")), "10 things i hate about you") {
				results = append(results, map[string]any{"id": 4951, "title": "10 Things I Hate About You", "release_date": "1999-03-31"})
			}
			json.NewEncoder(w).Encode(map[string]any{"results": results})
		case r.URL.Path == "/movie/4951":
			json.NewEncoder(w).Encode(map[string]any{"id": 4951, "title": "10 Things I Hate About You", "release_date": "1999-03-31", "external_ids": map[string]any{"imdb_id": "tt0147800"}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return tmdb.New(tmdb.Options{Token: "test", BaseURL: srv.URL})
}

// TestImportLibrary_AddsUnmappedMovieFolders: a folder named the way Radarr
// names them is matched on TMDB, added pointing at that folder, and its file
// attached under its existing name; the rest is reported, not guessed.
func TestImportLibrary_AddsUnmappedMovieFolders(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	root := t.TempDir()
	rootID, err := store.CreateRootFolder(ctx, db, root, "movie")
	if err != nil {
		t.Fatalf("root folder: %v", err)
	}
	if _, err := store.CreateQualityProfile(ctx, db, "Any"); err != nil {
		t.Fatalf("profile: %v", err)
	}
	mk := func(dir, file string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if file != "" {
			if err := os.WriteFile(filepath.Join(root, dir, file), []byte("video"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("10 Things I Hate About You (1999)", "10 Things I Hate About You (1999) Bluray-1080p x264.mp4")
	mk("10 Things I Hate About Dating ()", "movie.mkv")
	mk("Empty Folder (2001)", "")
	if err := os.WriteFile(filepath.Join(root, "Loose File.mkv"), []byte("video"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := &ImportService{DB: db, Movies: &MovieService{DB: db, TMDB: fakeTMDB(t)}}
	progress := &LibraryImport{}
	if err := svc.importLibrary(ctx, rootID, func(change func(*LibraryImport)) { change(progress) }); err != nil {
		t.Fatalf("import library: %v", err)
	}
	if progress.Total != 3 || progress.Added != 1 || progress.NoVideo != 1 || progress.Skipped != 1 || len(progress.Unmatched) != 1 || progress.Unmatched[0] != "10 Things I Hate About Dating ()" {
		t.Fatalf("want 3 folders: 1 added, 1 without video, 1 unmatched, 1 loose file skipped; got %+v", progress)
	}
	movies, err := store.ListMovies(ctx, db)
	if err != nil || len(movies) != 1 {
		t.Fatalf("want one movie, got %+v (%v)", movies, err)
	}
	if movies[0].Path.String != filepath.Join(root, "10 Things I Hate About You (1999)") || !movies[0].HasFile {
		t.Fatalf("want the movie at its existing folder with its file, got %+v", movies[0])
	}
	files, _ := store.ListMovieFilesForMovie(ctx, db, movies[0].ID)
	if len(files) != 1 || files[0].RelativePath != "10 Things I Hate About You (1999) Bluray-1080p x264.mp4" {
		t.Fatalf("want the file attached under its existing name, got %+v", files)
	}

	// Running again finds nothing new: the folder is mapped now.
	again := &LibraryImport{}
	if err := svc.importLibrary(ctx, rootID, func(change func(*LibraryImport)) { change(again) }); err != nil {
		t.Fatalf("second import: %v", err)
	}
	if again.Total != 2 || again.Added != 0 {
		t.Fatalf("want the second run to skip the mapped folder, got %+v", again)
	}
}

func TestParseMovieFolder(t *testing.T) {
	for name, want := range map[string]struct {
		title string
		year  int
	}{
		"10 Things I Hate About You (1999)": {"10 Things I Hate About You", 1999},
		"10 Things I Hate About Dating ()":  {"10 Things I Hate About Dating", 0},
		"Blade.Runner.2049.2017.1080p":      {"Blade Runner 2049", 2017},
		"Just A Name":                       {"Just A Name", 0},
	} {
		title, year := parseMovieFolder(name)
		if title != want.title || year != want.year {
			t.Errorf("%q: want %q %d, got %q %d", name, want.title, want.year, title, year)
		}
	}
}
