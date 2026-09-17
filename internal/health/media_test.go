package health

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func messages(issues []Issue) string {
	var all []string
	for _, i := range issues {
		all = append(all, i.Message)
	}
	return strings.Join(all, "\n")
}

func TestCheck_MediaAnalysis(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c := &Checker{DB: db, TMDBConfigured: true}
	if got := messages(c.Check(ctx)); !strings.Contains(got, "FFprobe isn't installed") {
		t.Fatalf("want the missing FFprobe reported while analysis is on, got:\n%s", got)
	}
	c.FFprobeAvailable = true
	if got := messages(c.Check(ctx)); strings.Contains(got, "FFprobe") || strings.Contains(got, "Plex") {
		t.Fatalf("want no media analysis issue, got:\n%s", got)
	}
	ms, _ := store.GetMediaSettings(ctx, db)
	ms.PlexMediaInfo = true
	store.UpdateMediaSettings(ctx, db, ms)
	if got := messages(c.Check(ctx)); !strings.Contains(got, "Use Plex Media Info is on, but there's no Plex connection") {
		t.Fatalf("want the missing Plex connection reported, got:\n%s", got)
	}
	store.CreateNotification(ctx, db, store.Notification{Name: "Plex", Implementation: store.NotifyPlex, Enabled: true, Settings: map[string]string{"server_url": "http://plex:32400", "token": "t"}})
	c.FFprobeAvailable = false
	ms.AnalyzeVideoFiles, ms.AnalyzeAudioFiles = false, false
	store.UpdateMediaSettings(ctx, db, ms)
	if got := messages(c.Check(ctx)); strings.Contains(got, "FFprobe") || strings.Contains(got, "Plex") {
		t.Fatalf("want nothing reported with analysis off and Plex connected, got:\n%s", got)
	}
}
