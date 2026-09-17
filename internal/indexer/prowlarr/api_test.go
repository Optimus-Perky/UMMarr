package prowlarr_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/prowlarr"
)

func TestIndexersAndAppProfiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "secret-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v1/indexer":
			w.Write([]byte(`[{"id":3,"name":"IPTorrents","enable":true,"protocol":"torrent","priority":10,"appProfileId":1,
				"capabilities":{"categories":[{"id":2000,"name":"Movies","subCategories":[{"id":2040,"name":"Movies/HD"}]},{"id":100001,"name":"Custom"}]}}]`))
		case "/api/v1/appprofile":
			w.Write([]byte(`[{"id":1,"name":"Standard","enableRss":true,"enableAutomaticSearch":false,"enableInteractiveSearch":true,"minimumSeeders":2}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := prowlarr.New(prowlarr.Options{BaseURL: srv.URL + "/", APIKey: "secret-key"})
	indexers, err := c.Indexers(context.Background())
	if err != nil {
		t.Fatalf("indexers: %v", err)
	}
	if len(indexers) != 1 || indexers[0].Name != "IPTorrents" || indexers[0].Protocol != "torrent" || indexers[0].Priority != 10 {
		t.Fatalf("unexpected indexers %+v", indexers)
	}
	if ids := indexers[0].CategoryIDs(); len(ids) != 3 || ids[1] != 2040 {
		t.Fatalf("want subcategories included, got %v", ids)
	}
	profiles, err := c.AppProfiles(context.Background())
	if err != nil || len(profiles) != 1 || profiles[0].EnableAutomaticSearch || profiles[0].MinimumSeeders != 2 {
		t.Fatalf("unexpected profiles %+v %v", profiles, err)
	}

	wrongKey := prowlarr.New(prowlarr.Options{BaseURL: srv.URL, APIKey: "nope"})
	if _, err := wrongKey.Indexers(context.Background()); err == nil {
		t.Fatalf("want an error for a rejected key")
	}
	if _, err := prowlarr.New(prowlarr.Options{BaseURL: srv.URL}).Indexers(context.Background()); err != prowlarr.ErrMissingCredential {
		t.Fatalf("want ErrMissingCredential without a key, got %v", err)
	}
}
