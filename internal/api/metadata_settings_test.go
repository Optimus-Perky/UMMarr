package api_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestMetadataSettings(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	_, body := get(t, srv, "/settings/metadata")
	if !strings.Contains(body, `data-section="metadata"`) || strings.Contains(body, `name="enabled" checked`) {
		t.Fatalf("want the Metadata panel, off by default, got:\n%s", body)
	}
	if !strings.Contains(body, `name="jellyfin"`) || !strings.Contains(body, `name="plex"`) || !strings.Contains(body, "Use local assets") {
		t.Fatalf("want Jellyfin and Plex providers with the Plex note, got:\n%s", body)
	}
	_, body = postForm(t, srv, "/settings/metadata", url.Values{"enabled": {"on"}, "plex": {"on"}, "movie_nfo": {"on"}, "series_nfo": {"on"}, "episode_nfo": {"on"}})
	if !strings.Contains(body, "Saved") {
		t.Fatalf("want saved, got:\n%s", body)
	}
	s, _ := store.GetMetadataSettings(t.Context(), db)
	if !s.Enabled || !s.Plex || s.Jellyfin || !s.MovieNFO || s.MovieImages || !s.EpisodeNFO {
		t.Fatalf("want the ticks saved, got %+v", s)
	}
}
