package store_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

func TestSeriesSettings_RoundTripWithTags(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rootFolderID := seedRootFolder(t, db, "series")
	profile := seedQualityProfile(t, db)
	metaID, _ := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{Title: metadata.Field[string]{Value: "Show", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "1"}})
	seriesID, _ := store.UpsertSeries(ctx, db, metaID, profile, rootFolderID, true)

	before, err := store.GetSeriesSettings(ctx, db, seriesID)
	if err != nil || !before.Monitored || before.MonitorNewItems != "all" || !before.SeasonFolder || before.SeriesType != "standard" || len(before.Tags) != 0 {
		t.Fatalf("want Sonarr's defaults, got %+v (%v)", before, err)
	}
	err = store.UpdateSeriesSettings(ctx, db, seriesID, store.SeriesSettings{Monitored: false, MonitorNewItems: "none", SeasonFolder: false, QualityProfileID: profile, SeriesType: "anime", Path: "/media/series/Show", Tags: []string{" Kids", "4k", "kids", ""}})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := store.GetSeriesSettings(ctx, db, seriesID)
	if after.Monitored || after.MonitorNewItems != "none" || after.SeasonFolder || after.SeriesType != "anime" || after.Path != "/media/series/Show" {
		t.Fatalf("want the settings saved, got %+v", after)
	}
	if len(after.Tags) != 2 || after.Tags[0] != "4k" || after.Tags[1] != "kids" {
		t.Fatalf("want tags created once, lower-cased and sorted, got %v", after.Tags)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM tags`).Scan(&n)
	if n != 2 {
		t.Fatalf("want 2 tag rows, got %d", n)
	}
}

func TestApplyMonitorOption(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rootFolderID := seedRootFolder(t, db, "series")
	profile := seedQualityProfile(t, db)
	metaID, _ := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{Title: metadata.Field[string]{Value: "Show", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "1"}})
	seriesID, _ := store.UpsertSeries(ctx, db, metaID, profile, rootFolderID, true)
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	// S0E1 special (aired); S1E1 old with file; S1E2 old missing; S2E1 aired 10 days ago; S2E2 unaired; S2E3 no date.
	type spec struct {
		season, number int
		aired          *time.Time
		file           bool
	}
	ptr := func(t time.Time) *time.Time { return &t }
	specs := []spec{{0, 1, ptr(now.Add(-400 * day)), false}, {1, 1, ptr(now.Add(-400 * day)), true}, {1, 2, ptr(now.Add(-399 * day)), false}, {2, 1, ptr(now.Add(-10 * day)), false}, {2, 2, ptr(now.Add(10 * day)), false}, {2, 3, nil, false}}
	seasonIDs := map[int]int64{}
	key := func(s, n int) string { return string(rune('0'+s)) + "x" + string(rune('0'+n)) }
	ids := map[string]int64{}
	for _, s := range specs {
		if _, ok := seasonIDs[s.season]; !ok {
			seasonIDs[s.season], _ = store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: s.season, Monitored: true})
		}
		e := metadata.EpisodeMetadata{EpisodeNumber: s.number, Title: metadata.Field[string]{Value: "Ep", Provider: "tmdb"}}
		if s.aired != nil {
			e.AirDate = metadata.Field[*time.Time]{Value: s.aired, Provider: "tmdb"}
		}
		id, err := store.UpsertEpisode(ctx, db, seriesID, seasonIDs[s.season], s.season, e)
		if err != nil {
			t.Fatal(err)
		}
		ids[key(s.season, s.number)] = id
		if s.file {
			store.AttachEpisodeFile(ctx, db, id, "Season 01/ep.mkv", 1)
		}
	}
	monitored := func() map[string]bool {
		out := map[string]bool{}
		for k, id := range ids {
			var on bool
			db.QueryRow(`SELECT monitored FROM episodes WHERE id = ?`, id).Scan(&on)
			out[k] = on
		}
		return out
	}
	seasonOn := func(n int) bool {
		var on bool
		db.QueryRow(`SELECT monitored FROM seasons WHERE series_id = ? AND season_number = ?`, seriesID, n).Scan(&on)
		return on
	}
	cases := []struct {
		option string
		want   []string // monitored episode keys (season 1-2 only; specials checked separately)
	}{
		{"none", nil},
		{"all", []string{"1x1", "1x2", "2x1", "2x2", "2x3"}},
		{"future", []string{"2x2", "2x3"}},
		{"missing", []string{"1x2", "2x1", "2x2", "2x3"}},
		{"existing", []string{"1x1", "2x2", "2x3"}},
		{"recent", []string{"2x1", "2x2", "2x3"}},
		{"pilot", []string{"1x1"}},
		{"firstSeason", []string{"1x1", "1x2"}},
		{"lastSeason", []string{"2x1", "2x2", "2x3"}},
	}
	for _, c := range cases {
		if err := store.ApplyMonitorOption(ctx, db, seriesID, c.option, now); err != nil {
			t.Fatalf("%s: %v", c.option, err)
		}
		got := monitored()
		want := map[string]bool{}
		for _, k := range c.want {
			want[k] = true
		}
		for _, k := range []string{"1x1", "1x2", "2x1", "2x2", "2x3"} {
			if got[k] != want[k] {
				t.Errorf("%s: episode %s monitored=%v, want %v", c.option, k, got[k], want[k])
			}
		}
		if c.option == "none" && (got["0x1"] || seasonOn(0)) {
			t.Errorf("none must unmonitor specials too")
		}
		if c.option == "pilot" && (!seasonOn(1) || seasonOn(2)) {
			t.Errorf("pilot: want only season 1 monitored, got s1=%v s2=%v", seasonOn(1), seasonOn(2))
		}
		if c.option == "all" && got["0x1"] {
			t.Errorf("all must leave specials alone")
		}
	}
	store.ApplyMonitorOption(ctx, db, seriesID, "monitorSpecials", now)
	if got := monitored(); !got["0x1"] || !got["2x1"] || !seasonOn(0) {
		t.Fatalf("monitorSpecials: want specials on and the rest untouched, got %v", got)
	}
	store.ApplyMonitorOption(ctx, db, seriesID, "unmonitorSpecials", now)
	if got := monitored(); got["0x1"] || !got["2x1"] || seasonOn(0) {
		t.Fatalf("unmonitorSpecials: want specials off and the rest untouched, got %v", got)
	}
	if err := store.ApplyMonitorOption(ctx, db, seriesID, "bogus", now); err == nil {
		t.Fatal("want an unknown option rejected")
	}
}

func TestEpisodeFiles_EditAndRemap(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	rootFolderID := seedRootFolder(t, db, "series")
	profile := seedQualityProfile(t, db)
	metaID, _ := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{Title: metadata.Field[string]{Value: "Show", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "1"}})
	seriesID, _ := store.UpsertSeries(ctx, db, metaID, profile, rootFolderID, true)
	seasonID, _ := store.UpsertSeason(ctx, db, seriesID, metadata.SeasonMetadata{SeasonNumber: 1})
	ids := map[int]int64{}
	for n := 1; n <= 4; n++ {
		ids[n], _ = store.UpsertEpisode(ctx, db, seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: n, Title: metadata.Field[string]{Value: []string{"", "Pilot", "Cat", "Dog", "Emu"}[n], Provider: "tmdb"}})
	}
	f1, _ := store.AttachEpisodeFile(ctx, db, ids[1], "Season 01/Show - S01E01 - Pilot - HDTV-720p FRENCH x265-GRP.mkv", 100)
	db.ExecContext(ctx, `UPDATE episode_files SET quality = '{"source":"HDTV","resolution":"720p","releaseGroup":"GRP"}' WHERE id = ?`, f1)
	f2a, _ := store.AttachEpisodeFile(ctx, db, ids[2], "Season 01/Show - S01E02-E03.mkv", 200)
	f2b, _ := store.AttachEpisodeFile(ctx, db, ids[3], "Season 01/Show - S01E02-E03.mkv", 200)

	files, err := store.ListEpisodeFileDetails(ctx, db, seriesID)
	if err != nil || len(files) != 2 {
		t.Fatalf("want the multi-episode file listed once, got %d (%v)", len(files), err)
	}
	if files[0].Quality != "HDTV-720p" || files[0].ReleaseGroup != "GRP" || files[0].Languages != "French" || files[0].Episodes != "1 - Pilot" || files[0].ReleaseType != "singleEpisode" {
		t.Fatalf("want quality, group, guessed language and episode title, got %+v", files[0])
	}
	if files[1].Episodes != "2-3 - Cat / Dog" || files[1].ReleaseType != "multiEpisode" || len(files[1].FileIDs) != 2 || files[1].FileIDs[0] != f2a || files[1].FileIDs[1] != f2b {
		t.Fatalf("want the two-episode file merged, got %+v", files[1])
	}

	q, g, l, rt := "Bluray-1080p", "NEWGRP", "English, French", "singleEpisode"
	if err := store.UpdateEpisodeFiles(ctx, db, []int64{f1}, store.EpisodeFileEdit{Quality: &q, ReleaseGroup: &g, Languages: &l, ReleaseType: &rt}); err != nil {
		t.Fatal(err)
	}
	files, _ = store.ListEpisodeFileDetails(ctx, db, seriesID)
	if files[0].Quality != "Bluray-1080p" || files[0].ReleaseGroup != "NEWGRP" || files[0].Languages != "English, French" || files[0].ReleaseType != "singleEpisode" {
		t.Fatalf("want the edits saved, got %+v", files[0])
	}
	bad := "Bluray-999p"
	if err := store.UpdateEpisodeFiles(ctx, db, []int64{f1}, store.EpisodeFileEdit{Quality: &bad}); err == nil {
		t.Fatal("want an unknown quality refused")
	}

	// Re-map the pilot's file onto episode 4; episode 1 reads as missing again.
	if err := store.RemapEpisodeFile(ctx, db, seriesID, []int64{f1}, 1, []int{4}); err != nil {
		t.Fatal(err)
	}
	var fileOf1, fileOf4 sql.NullInt64
	db.QueryRow(`SELECT episode_file_id FROM episodes WHERE id = ?`, ids[1]).Scan(&fileOf1)
	db.QueryRow(`SELECT episode_file_id FROM episodes WHERE id = ?`, ids[4]).Scan(&fileOf4)
	if fileOf1.Valid || !fileOf4.Valid {
		t.Fatalf("want the file moved from E01 to E04, got %v %v", fileOf1, fileOf4)
	}
	files, _ = store.ListEpisodeFileDetails(ctx, db, seriesID)
	if files[1].Episodes != "4 - Emu" || files[1].Quality != "Bluray-1080p" || files[1].Languages != "English, French" {
		t.Fatalf("want the remapped file to keep its details, got %+v", files[1])
	}
	// A target that already has another file is refused.
	if err := store.RemapEpisodeFile(ctx, db, seriesID, []int64{f2a, f2b}, 1, []int{4}); err == nil || !strings.Contains(err.Error(), "already has a file") {
		t.Fatalf("want the clash refused, got %v", err)
	}
	if err := store.RemapEpisodeFile(ctx, db, seriesID, []int64{f2a, f2b}, 2, []int{1}); err == nil || !strings.Contains(err.Error(), "no episode S02E01") {
		t.Fatalf("want a missing episode refused, got %v", err)
	}
}
