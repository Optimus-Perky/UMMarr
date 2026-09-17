package sync

import (
	"context"
	"net/http"
	"net/http/httptest"
	gosync "sync"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func fakeProwlarr(t *testing.T) (url string, calls func() int) {
	t.Helper()
	var mu gosync.Mutex
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		mu.Unlock()
		if r.Header.Get("X-Api-Key") != "prowlarr-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/indexer":
			w.Write([]byte(`[
				{"id":1,"name":"IPTorrents","enable":true,"protocol":"torrent","priority":10,"appProfileId":1,
				 "capabilities":{"categories":[
				   {"id":2000,"name":"Movies","subCategories":[{"id":2040,"name":"Movies/HD"}]},
				   {"id":5000,"name":"TV","subCategories":[{"id":5070,"name":"TV/Anime"}]},
				   {"id":3000,"name":"Audio"},{"id":7000,"name":"Other"}]}},
				{"id":2,"name":"Disabled","enable":false,"protocol":"torrent","priority":25,"appProfileId":1,
				 "capabilities":{"categories":[{"id":2000,"name":"Movies"}]}},
				{"id":3,"name":"Books","enable":true,"protocol":"usenet","priority":25,"appProfileId":1,
				 "capabilities":{"categories":[{"id":8000,"name":"Books"}]}},
				{"id":4,"name":"NZBgeek","enable":true,"protocol":"usenet","priority":0,"appProfileId":9,
				 "capabilities":{"categories":[{"id":5000,"name":"TV"}]}}
			]`))
		case "/api/v1/appprofile":
			w.Write([]byte(`[{"id":1,"enableRss":false,"enableAutomaticSearch":true,"enableInteractiveSearch":true,"minimumSeeders":3}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() int { mu.Lock(); defer mu.Unlock(); return n }
}

func TestConvertProwlarrConnection(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	prowlarrURL, calls := fakeProwlarr(t)
	if _, err := db.ExecContext(ctx, `UPDATE app_settings SET prowlarr_base_url = ?, prowlarr_api_key = 'prowlarr-key'`, prowlarrURL+"/"); err != nil {
		t.Fatalf("save prowlarr connection: %v", err)
	}

	created, err := ConvertProwlarrConnection(ctx, db, ProwlarrConversion{})
	if err != nil || created != 2 {
		t.Fatalf("want 2 indexers created (disabled and book-only skipped), got %d, %v", created, err)
	}
	indexers, _ := store.ListIndexers(ctx, db)
	ipt, geek := indexers[0], indexers[1]
	if ipt.Name != "IPTorrents (Prowlarr)" || ipt.Implementation != "Torznab" || ipt.BaseURL != prowlarrURL+"/1/" ||
		ipt.APIPath != "/api" || ipt.APIKey != "prowlarr-key" || ipt.Priority != 10 || !ipt.Converted || ipt.Synced {
		t.Errorf("want Prowlarr's own shape for the torrent indexer, got %+v", ipt)
	}
	if got := ipt.Categories; len(got) != 4 || got[0] != 2000 || got[1] != 2040 || got[2] != 5000 || got[3] != 3000 {
		t.Errorf("want movie, TV and audio sync categories it supports, got %v", got)
	}
	if len(ipt.AnimeCategories) != 1 || ipt.AnimeCategories[0] != 5070 {
		t.Errorf("want the anime category, got %v", ipt.AnimeCategories)
	}
	if ipt.EnableRSS || !ipt.EnableAutomaticSearch || !ipt.EnableInteractiveSearch || ipt.MinimumSeeders != 3 {
		t.Errorf("want the app profile's search switches and seeders, got %+v", ipt)
	}
	if geek.Name != "NZBgeek (Prowlarr)" || geek.Implementation != "Newznab" || geek.Priority != store.DefaultIndexerPriority || !geek.EnableRSS {
		t.Errorf("want a usenet indexer as Newznab with defaults for an unknown profile, got %+v", geek)
	}

	before := calls()
	again, err := ConvertProwlarrConnection(ctx, db, ProwlarrConversion{})
	if err != nil || again != 0 || calls() != before {
		t.Fatalf("want the conversion to run only once, got %d created, %v, %d more calls", again, err, calls()-before)
	}
}

func TestConvertProwlarrConnection_UsesBootstrapAndRetriesWhenUnreachable(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if _, err := ConvertProwlarrConnection(ctx, db, ProwlarrConversion{BootstrapBaseURL: "http://127.0.0.1:1", BootstrapAPIKey: "prowlarr-key"}); err == nil {
		t.Fatalf("want an error while Prowlarr is unreachable")
	}
	if s, _ := store.GetIndexerSettings(ctx, db); s.ProwlarrConverted {
		t.Fatalf("want an unreachable Prowlarr tried again later")
	}
	prowlarrURL, _ := fakeProwlarr(t)
	created, err := ConvertProwlarrConnection(ctx, db, ProwlarrConversion{BootstrapBaseURL: prowlarrURL, BootstrapAPIKey: "prowlarr-key"})
	if err != nil || created != 2 {
		t.Fatalf("want the .env connection converted once reachable, got %d, %v", created, err)
	}
}

func TestConvertProwlarrConnection_NothingToConvert(t *testing.T) {
	ctx := context.Background()

	db := openTestDB(t)
	if created, err := ConvertProwlarrConnection(ctx, db, ProwlarrConversion{}); err != nil || created != 0 {
		t.Fatalf("want nothing to do without a connection, got %d, %v", created, err)
	}
	if s, _ := store.GetIndexerSettings(ctx, db); !s.ProwlarrConverted {
		t.Fatalf("want it recorded as done")
	}

	withIndexers := openTestDB(t)
	addTestIndexer(t, withIndexers, "Already here", "http://example.invalid", nil)
	prowlarrURL, calls := fakeProwlarr(t)
	created, err := ConvertProwlarrConnection(ctx, withIndexers, ProwlarrConversion{BootstrapBaseURL: prowlarrURL, BootstrapAPIKey: "prowlarr-key"})
	if err != nil || created != 0 || calls() != 0 {
		t.Fatalf("want existing indexers left as they are, got %d, %v, %d calls", created, err, calls())
	}
}
