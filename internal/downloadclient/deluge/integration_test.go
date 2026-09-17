package deluge_test

import (
	"context"
	"os"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/downloadclient/deluge"
)

// TestLive_LoginAndGetTorrentsStatus hits a real, self-hosted Deluge Web UI
// - skipped unless UMMARR_DELUGE_BASE_URL is set, matching every other live
// integration test in this project.
func TestLive_LoginAndGetTorrentsStatus(t *testing.T) {
	baseURL := os.Getenv("UMMARR_DELUGE_BASE_URL")
	if baseURL == "" {
		t.Skip("UMMARR_DELUGE_BASE_URL not set, skipping live Deluge test")
	}

	c, err := deluge.New(deluge.Options{BaseURL: baseURL, Password: os.Getenv("UMMARR_DELUGE_PASSWORD")})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	statuses, err := c.GetTorrentsStatus(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetTorrentsStatus: %v", err)
	}
	t.Logf("got %d torrents", len(statuses))
}
