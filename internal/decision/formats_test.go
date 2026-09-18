package decision

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/customformat"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// formatEngine has two formats: x265 is wanted, CAM is not.
func formatEngine(minScore, cutoffScore int) *Engine {
	e := engine()
	e.Formats = []customformat.Format{
		{ID: 1, Name: "x265", Conditions: []customformat.Condition{{Implementation: customformat.ReleaseTitle, Value: `x265|HEVC`}}},
		{ID: 2, Name: "CAM", Conditions: []customformat.Condition{{Implementation: customformat.ReleaseTitle, Value: `\bCAM\b`}}},
	}
	e.Profiles = Profiles{1: {Items: store.DefaultQualityProfileItems(store.MediaKindVideo), UpgradeAllowed: true, Cutoff: "WEBDL-1080p",
		FormatScores: map[int64]int{1: 25, 2: -1000}, MinFormatScore: minScore, CutoffFormatScore: cutoffScore}}
	return e
}

func profiled(m store.WantedMovie) store.WantedMovie {
	m.QualityProfileID = sql.NullInt64{Int64: 1, Valid: true}
	return m
}

func TestCustomFormats_ScoreAndMinimum(t *testing.T) {
	e := formatEngine(0, 0)
	d := e.Movie(profiled(inception()), []newznab.Release{torrent("Inception 2010 1080p BluRay x265-GRP")})[0]
	wantApproved(t, d)
	if d.CustomFormatScore != 25 || strings.Join(d.CustomFormats, ",") != "x265" {
		t.Errorf("want the x265 format scored 25, got %+v (%d)", d.CustomFormats, d.CustomFormatScore)
	}

	cam := e.Movie(profiled(inception()), []newznab.Release{torrent("Inception 2010 CAM x264-GRP")})[0]
	wantRejected(t, cam, "Custom Formats CAM have score -1000 below the quality profile's minimum 0")

	// A release matching nothing passes a minimum of 0 but not a positive one.
	plain := torrent("Inception 2010 1080p BluRay x264-GRP")
	wantApproved(t, e.Movie(profiled(inception()), []newznab.Release{plain})[0])
	strict := formatEngine(10, 0)
	wantRejected(t, strict.Movie(profiled(inception()), []newznab.Release{plain})[0], "Custom Formats None have score 0 below")
}

func TestCustomFormats_Ranking(t *testing.T) {
	e := formatEngine(-2000, 0)
	// WEBDL-1080p outweighs HDTV-1080p in the default profile, so only the
	// format score can put the x265 HDTV release first.
	ds := e.Movie(profiled(inception()), []newznab.Release{
		torrent("Inception 2010 1080p WEBDL x264-GRP"),
		torrent("Inception 2010 1080p HDTV x265-GRP"),
	})
	if !strings.Contains(ds[0].Release.Title, "x265") {
		t.Errorf("want the scored format first, got %q", ds[0].Release.Title)
	}
}

func TestCustomFormats_Upgrade(t *testing.T) {
	e := formatEngine(-2000, 25)
	m := profiled(inception())
	m.HasFile, m.FileQuality, m.FileRelease = true, mustQuality("Inception 2010 1080p WEBDL x264-GRP"), "Inception 2010 1080p WEBDL x264-GRP"

	// Same quality, better formats, and the file is below the format cutoff.
	upgrade := e.Movie(m, []newznab.Release{torrent("Inception 2010 1080p WEBDL x265-GRP")})[0]
	wantApproved(t, upgrade)

	// Once the file scores the cutoff, its quality cutoff counts as met.
	m.FileRelease = "Inception 2010 1080p WEBDL x265-GRP"
	m.FileQuality = mustQuality(m.FileRelease)
	done := e.Movie(m, []newznab.Release{torrent("Inception 2010 1080p WEBDL x265-OTHER")})[0]
	wantRejected(t, done, "already meets the profile's cutoff")
}

func mustQuality(title string) releaseparse.FileQuality { return releaseparse.Parse(title) }
