package api_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The Music page used to be a plain grid of "Open" buttons while Movies and
// TV shared the library toolbar (sort, filter, Posters/Overview/Table views
// and the mass editor). These tests pin music to the same contract, so the
// shared JS in static/library-grid.js and static/library-views.js can drive
// it: the toolbar, the grid and the editor bar must all name the library,
// and the cards must carry the keys the toolbar sorts and filters on.

func TestMusicPage_UsesTheSharedLibraryToolbar(t *testing.T) {
	db := openTestDB(t)
	seedTestArtist(t, db)
	srv := newTestServerWithDB(t, db)

	_, body := get(t, srv, "/music")
	for _, want := range []string{
		`class="library-toolbar" data-library="music"`,
		`class="grid poster-grid" data-prefs="ummarr-poster-music" data-library="music"`,
		`class="editor-bar" data-library="music"`,
		`class="letter-bar" data-library="music"`,
		`id="music-selection"`,
		`data-editor="edit"`, `data-editor="monitor"`, `data-editor="delete"`,
		`id="poster-options"`, `id="overview-options"`, `id="table-options"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q on the Music page", want)
		}
	}
	// The card carries every key the toolbar sorts or filters by.
	for _, attr := range []string{"data-sorttitle=", "data-letter=", "data-profile=", "data-added=", "data-status=", "data-albums=", "data-missing=", "data-cutoffunmet=", "data-monitored="} {
		if !strings.Contains(body, attr) {
			t.Errorf("want the artist card to carry %s", attr)
		}
	}
}

func TestMusicPage_MonitoringDialogOffersAlbumOptions(t *testing.T) {
	db := openTestDB(t)
	seedTestArtist(t, db)
	srv := newTestServerWithDB(t, db)

	_, body := get(t, srv, "/music")
	if !strings.Contains(body, `hx-post="/music/editor/monitor"`) {
		t.Fatal("want the Monitoring dialog posting to /music/editor/monitor")
	}
	for _, want := range []string{"All Albums", "Future Albums", "Missing Albums", "Existing Albums", "Latest Album", "First Album"} {
		if !strings.Contains(body, want) {
			t.Errorf("want the album monitor option %q offered", want)
		}
	}
}

func TestMusicPage_HasCutoffSearch(t *testing.T) {
	// Both search buttons only show with an indexer configured.
	srv, db, _ := newTestServerWithIndexer(t, magnetIndexer("irrelevant"), func(w http.ResponseWriter, r *http.Request) {})
	seedTestArtist(t, db)
	_, body := get(t, srv, "/music")
	if !strings.Contains(body, `hx-post="/music/search-cutoff"`) {
		t.Errorf("want Search cutoff unmet on the Music page")
	}
	if !strings.Contains(body, `hx-post="/music/search-missing"`) {
		t.Errorf("want Search all missing on the Music page")
	}
}

// A sort option's value is used directly as a card data attribute
// (library-grid.js: card.dataset[sort.key]), so a hyphenated value like
// "missing-episodes" silently sorts by nothing at all. Both library pages
// must only offer sort keys their cards actually carry.
func TestLibraryPages_SortOptionsMatchCardAttributes(t *testing.T) {
	// Separate databases: both seeds create a quality profile, and the
	// names collide.
	musicDB, tvDB := openTestDB(t), openTestDB(t)
	seedTestArtist(t, musicDB)
	seedTestSeries(t, tvDB)
	servers := map[string]*httptest.Server{"/music": newTestServerWithDB(t, musicDB), "/tv": newTestServerWithDB(t, tvDB)}

	sortBlock := regexp.MustCompile(`(?s)class="release-filter library-sort".*?</select>`)
	optionValue := regexp.MustCompile(`<option value="([^"]+)"`)
	for _, page := range []string{"/music", "/tv"} {
		_, body := get(t, servers[page], page)
		block := sortBlock.FindString(body)
		if block == "" {
			t.Fatalf("%s: no sort control found", page)
		}
		for _, m := range optionValue.FindAllStringSubmatch(block, -1) {
			key := m[1]
			if !strings.Contains(body, "data-"+key+"=") {
				t.Errorf("%s: sort option %q has no matching data-%s attribute on the cards, so it sorts by nothing", page, key, key)
			}
		}
	}
}
