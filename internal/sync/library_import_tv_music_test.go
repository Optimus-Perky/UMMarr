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

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/musicbrainz"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestImportLibrary_Series: a series folder holding episode files is
// matched on TMDB and added at its folder with the episode attached under
// its existing name; a folder of stray videos is left alone.
func TestImportLibrary_Series(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/tv":
			if strings.Contains(strings.ToLower(r.URL.Query().Get("query")), "breaking bad") {
				json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"id": 1396, "name": "Breaking Bad", "first_air_date": "2008-01-20"}}})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{{"id": 1, "name": "Temp", "first_air_date": "2010-01-01"}}})
		case "/tv/1396":
			json.NewEncoder(w).Encode(map[string]any{"id": 1396, "name": "Breaking Bad", "first_air_date": "2008-01-20", "status": "Ended",
				"seasons": []map[string]any{{"season_number": 1, "episode_count": 1, "name": "Season 1"}}, "external_ids": map[string]any{"imdb_id": "tt0903747"}})
		case "/tv/1396/season/1":
			json.NewEncoder(w).Encode(map[string]any{"season_number": 1, "episodes": []map[string]any{{"id": 62085, "episode_number": 1, "season_number": 1, "name": "Pilot", "air_date": "2008-01-20"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	db := openTestDB(t)
	ctx := context.Background()
	root := t.TempDir()
	rootID, _ := store.CreateRootFolder(ctx, db, root, "series")
	store.CreateQualityProfile(ctx, db, "Any")
	writeFile(t, filepath.Join(root, "Breaking Bad (2008)", "Season 01", "Breaking.Bad.S01E01.720p.mkv"))
	writeFile(t, filepath.Join(root, "Temp", "holiday video.mkv"))
	writeFile(t, filepath.Join(root, "Current", "Show", "Show.S01E01.mkv"))
	if _, err := store.CreateRootFolder(ctx, db, filepath.Join(root, "Current"), "series"); err != nil {
		t.Fatal(err)
	}

	client := tmdb.New(tmdb.Options{Token: "test", BaseURL: srv.URL})
	svc := &ImportService{DB: db, Series: &SeriesService{DB: db, TMDB: client}}
	progress := &LibraryImport{}
	if err := svc.importLibrary(ctx, rootID, func(change func(*LibraryImport)) { change(progress) }); err != nil {
		t.Fatalf("import: %v", err)
	}
	if progress.Kind != "series" || progress.Total != 2 || progress.Added != 1 || progress.NoVideo != 1 || len(progress.Unmatched) != 0 {
		t.Fatalf("want 1 series added and the stray folder skipped, got %+v", progress)
	}
	series, _ := store.ListSeries(ctx, db)
	if len(series) != 1 || series[0].Path.String != filepath.Join(root, "Breaking Bad (2008)") || series[0].EpisodeFileCount != 1 {
		t.Fatalf("want Breaking Bad at its folder with one episode file, got %+v", series)
	}
	refs, _ := store.ListEpisodeFileRefs(ctx, db, series[0].ID)
	if len(refs) != 1 || refs[0].RelativePath != filepath.Join("Season 01", "Breaking.Bad.S01E01.720p.mkv") {
		t.Fatalf("want the episode file attached under its existing name, got %+v", refs)
	}
}

// TestImportLibrary_Music: an artist folder is matched on MusicBrainz
// ("Beloved, The" read as The Beloved), its album folders matched among
// the artist's release groups, and the tracks attached as they are.
func TestImportLibrary_Music(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/artist":
			json.NewEncoder(w).Encode(map[string]any{"artists": []map[string]any{{"id": "beloved-mbid", "name": "The Beloved", "sort-name": "Beloved, The", "type": "Group"}}})
		case "/artist/beloved-mbid":
			json.NewEncoder(w).Encode(map[string]any{"id": "beloved-mbid", "name": "The Beloved", "sort-name": "Beloved, The", "type": "Group"})
		case "/release-group":
			json.NewEncoder(w).Encode(map[string]any{"release-groups": []map[string]any{
				{"id": "happiness-rg", "title": "Happiness", "primary-type": "Album", "first-release-date": "1990-02-05"},
				{"id": "happiness-live-rg", "title": "Happiness", "primary-type": "Album", "secondary-types": []string{"Live"}},
			}})
		case "/release-group/happiness-rg":
			json.NewEncoder(w).Encode(map[string]any{"id": "happiness-rg", "title": "Happiness", "primary-type": "Album", "first-release-date": "1990-02-05",
				"releases": []map[string]any{{"id": "happiness-rel", "title": "Happiness", "status": "Official"}}})
		case "/release/happiness-rel":
			json.NewEncoder(w).Encode(map[string]any{"id": "happiness-rel", "title": "Happiness", "media": []map[string]any{{"position": 1, "tracks": []map[string]any{
				{"id": "t1", "number": "1", "title": "Hello", "length": 250000}, {"id": "t2", "number": "2", "title": "Your Love Takes Me Higher", "length": 240000},
			}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	db := openTestDB(t)
	ctx := context.Background()
	root := t.TempDir()
	rootID, _ := store.CreateRootFolder(ctx, db, root, "music")
	store.CreateQualityProfile(ctx, db, "Any")
	writeFile(t, filepath.Join(root, "Beloved, The", "Happiness (1990)", "01 - Hello.flac"))
	writeFile(t, filepath.Join(root, "Beloved, The", "Happiness (1990)", "02 - Your Love Takes Me Higher.flac"))
	writeFile(t, filepath.Join(root, "Beloved, The", "Rarities", "odd.flac"))
	writeFile(t, filepath.Join(root, "Photos Only", "cover.jpg"))

	client, err := musicbrainz.New(musicbrainz.Options{UserAgent: "test/1.0", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	svc := &ImportService{DB: db, Music: &MusicService{DB: db, MusicBrainz: client}}
	progress := &LibraryImport{}
	if err := svc.importLibrary(ctx, rootID, func(change func(*LibraryImport)) { change(progress) }); err != nil {
		t.Fatalf("import: %v", err)
	}
	if progress.Kind != "music" || progress.Total != 2 || progress.Added != 1 || progress.Albums != 1 || progress.NoVideo != 1 || len(progress.Unmatched) != 1 || progress.Unmatched[0] != "Beloved, The / Rarities" {
		t.Fatalf("want the artist and one album added, Rarities unmatched, the photo folder skipped; got %+v", progress)
	}
	albums, _ := store.ListAlbums(ctx, db)
	if len(albums) != 1 || albums[0].Title != "Happiness" || albums[0].Path.String != filepath.Join(root, "Beloved, The", "Happiness (1990)") {
		t.Fatalf("want Happiness at its folder, got %+v", albums)
	}
	var files int
	db.QueryRow(`SELECT COUNT(*) FROM track_files WHERE relative_path IN ('01 - Hello.flac', '02 - Your Love Takes Me Higher.flac')`).Scan(&files)
	if files != 2 {
		t.Fatalf("want both tracks attached under their existing names, got %d", files)
	}
}

func TestFolderNames(t *testing.T) {
	for in, want := range map[string]string{"Beloved, The": "The Beloved", "ABBA": "ABBA", "Sabbath, Black": "Sabbath, Black"} {
		if got := artistFolderName(in); got != want {
			t.Errorf("artist %q: want %q, got %q", in, want, got)
		}
	}
	for in, want := range map[string]string{"Happiness (1990)": "Happiness", "1990 - Happiness": "Happiness", "Midnight Marauders": "Midnight Marauders", "Discovery (Vinyl)": "Discovery", "Nevermind (Vinyl)": "Nevermind", "Queen - Greatest Hits (1981) [24 bit FLAC] vinyl": "Queen - Greatest Hits", "Random Access Memories - Vinyl Edition": "Random Access Memories", "A Rush of Blood to the Head (instrumental)": "A Rush of Blood to the Head (instrumental)"} {
		if got := albumFolderName(in); got != want {
			t.Errorf("album %q: want %q, got %q", in, want, got)
		}
	}
}

// TestImportLibrary_Music_TrackedArtistGetsNewAlbums: an artist already in
// the library still has new album folders matched on a later run.
func TestImportLibrary_Music_TrackedArtistGetsNewAlbums(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/artist":
			json.NewEncoder(w).Encode(map[string]any{"artists": []map[string]any{{"id": "beloved-mbid", "name": "The Beloved", "type": "Group"}}})
		case "/artist/beloved-mbid":
			json.NewEncoder(w).Encode(map[string]any{"id": "beloved-mbid", "name": "The Beloved", "sort-name": "Beloved, The", "type": "Group"})
		case "/release-group":
			json.NewEncoder(w).Encode(map[string]any{"release-group-count": 2, "release-groups": []map[string]any{
				{"id": "happiness-rg", "title": "Happiness", "primary-type": "Album"}, {"id": "conscience-rg", "title": "Conscience", "primary-type": "Album"},
			}})
		case "/release-group/happiness-rg", "/release-group/conscience-rg":
			id := strings.TrimPrefix(r.URL.Path, "/release-group/")
			json.NewEncoder(w).Encode(map[string]any{"id": id, "title": strings.Title(strings.TrimSuffix(id, "-rg")), "primary-type": "Album",
				"releases": []map[string]any{{"id": id + "-rel", "status": "Official"}}})
		case "/release/happiness-rg-rel", "/release/conscience-rg-rel":
			json.NewEncoder(w).Encode(map[string]any{"id": strings.TrimPrefix(r.URL.Path, "/release/"), "media": []map[string]any{{"position": 1, "tracks": []map[string]any{{"id": "t", "number": "1", "title": "One"}}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	db := openTestDB(t)
	ctx := context.Background()
	root := t.TempDir()
	rootID, _ := store.CreateRootFolder(ctx, db, root, "music")
	store.CreateQualityProfile(ctx, db, "Any")
	writeFile(t, filepath.Join(root, "The Beloved", "Happiness", "01.flac"))
	client, _ := musicbrainz.New(musicbrainz.Options{UserAgent: "test/1.0", BaseURL: srv.URL})
	svc := &ImportService{DB: db, Music: &MusicService{DB: db, MusicBrainz: client}}
	first := &LibraryImport{}
	if err := svc.importLibrary(ctx, rootID, func(change func(*LibraryImport)) { change(first) }); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "The Beloved", "Conscience (Vinyl)", "01.flac"))
	second := &LibraryImport{}
	if err := svc.importLibrary(ctx, rootID, func(change func(*LibraryImport)) { change(second) }); err != nil {
		t.Fatal(err)
	}
	if first.Added != 1 || first.Albums != 1 || second.Added != 0 || second.Albums != 1 {
		t.Fatalf("want the first run to add the artist and album, the second only the new album; got %+v then %+v", first, second)
	}
}

func TestMatchKey(t *testing.T) {
	for a, b := range map[string]string{"Marvels Cloak And Dagger": "Marvel's Cloak & Dagger", "The Cleaning Lady (US)": "The Cleaning Lady", "Superman And Lois": "Superman & Lois"} {
		if matchKey(a) != matchKey(b) {
			t.Errorf("want %q to match %q (%q vs %q)", a, b, matchKey(a), matchKey(b))
		}
	}
}
