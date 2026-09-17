package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	gosync "sync"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// magnetItem is a feed item whose download is a magnet, so grabbing needs no
// download request.
func magnetItem(guid, title string) string {
	return fmt.Sprintf(`<item><title>%s</title><guid>%s</guid><torznab:attr name="magneturl" value="magnet:?xt=urn:btih:%s"/><torznab:attr name="seeders" value="20"/><size>1000</size></item>`, title, guid, guid)
}

// fakeDeluge accepts magnets and records what it was asked to do.
type fakeDeluge struct {
	mu      gosync.Mutex
	magnets []string
	options []map[string]any
}

func (d *fakeDeluge) serve(t *testing.T) string {
	return newTestDelugeServer(t, func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
			ID     int    `json:"id"`
		}
		json.NewDecoder(r.Body).Decode(&call)
		d.mu.Lock()
		defer d.mu.Unlock()
		switch call.Method {
		case "auth.login":
			json.NewEncoder(w).Encode(map[string]any{"result": true, "error": nil, "id": call.ID})
		case "core.add_torrent_magnet":
			magnet, _ := call.Params[0].(string)
			d.magnets = append(d.magnets, magnet)
			json.NewEncoder(w).Encode(map[string]any{"result": fmt.Sprintf("hash%d", len(d.magnets)), "error": nil, "id": call.ID})
		case "core.set_torrent_options":
			opts, _ := call.Params[1].(map[string]any)
			d.options = append(d.options, opts)
			json.NewEncoder(w).Encode(map[string]any{"result": nil, "error": nil, "id": call.ID})
		}
	})
}

func newSearchService(t *testing.T, db *sql.DB, feed string, change func(*store.Indexer)) (*SearchService, *fakeDeluge) {
	t.Helper()
	idx := &fakeIndexer{feed: feed}
	addTestIndexer(t, db, "Tracker", idx.serve(t), change)
	deluge := &fakeDeluge{}
	indexers := &IndexerService{DB: db}
	download := &DownloadService{DB: db, Indexers: indexers, BootstrapDelugeBaseURL: deluge.serve(t), BootstrapDelugePassword: "x"}
	return &SearchService{DB: db, Indexers: indexers, Download: download}, deluge
}

func releasedMovie(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	movieID := seedMovie(t, db)
	if _, err := db.Exec(`UPDATE movie_metadata SET title = 'Inception', year = 2010, physical_release = '2010-12-07'
		WHERE id = (SELECT movie_metadata_id FROM movies WHERE id = ?)`, movieID); err != nil {
		t.Fatalf("set movie details: %v", err)
	}
	return movieID
}

func listGrabs(t *testing.T, db *sql.DB) []store.Grab {
	t.Helper()
	grabs, err := store.ListGrabs(context.Background(), db)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	return grabs
}

func TestSearchMovie_GrabsTheBestAcceptableRelease(t *testing.T) {
	db := openTestDB(t)
	movieID := releasedMovie(t, db)
	feed := testFeed(
		magnetItem("hd", "Inception 2010 720p BluRay x264"),
		magnetItem("fhd", "Inception 2010 1080p BluRay x264"),
		magnetItem("other", "Interstellar 2014 2160p BluRay x265"),
	)
	svc, deluge := newSearchService(t, db, feed, func(ix *store.Indexer) {
		ix.SeedRatio = sql.NullFloat64{Float64: 1.5, Valid: true}
	})

	report, err := svc.SearchMovie(context.Background(), movieID)
	if err != nil {
		t.Fatalf("search movie: %v", err)
	}
	if len(report.Grabbed) != 1 || report.Grabbed[0].Release != "Inception 2010 1080p BluRay x264" || report.Grabbed[0].Indexer != "Tracker" {
		t.Fatalf("want the 1080p release grabbed, got %+v", report)
	}
	grabs := listGrabs(t, db)
	if len(grabs) != 1 || grabs[0].GrabbedBy != PurposeAutomatic || grabs[0].MovieID.Int64 != movieID {
		t.Fatalf("want one automatic grab for the movie, got %+v", grabs)
	}
	if len(deluge.magnets) != 1 || deluge.magnets[0] != "magnet:?xt=urn:btih:fhd" {
		t.Fatalf("want only the 1080p magnet sent to Deluge, got %v", deluge.magnets)
	}
	if len(deluge.options) != 1 || deluge.options[0]["stop_ratio"] != 1.5 || deluge.options[0]["stop_at_ratio"] != true {
		t.Fatalf("want the indexer's seed ratio applied, got %v", deluge.options)
	}
}

func TestSearchMovie_NothingAcceptable(t *testing.T) {
	db := openTestDB(t)
	movieID := releasedMovie(t, db)
	if _, err := db.Exec(`UPDATE quality_profiles SET upgrade_allowed = 0`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertMovieFile(context.Background(), db, movieID, "Inception.mkv", 10); err != nil {
		t.Fatalf("insert movie file: %v", err)
	}
	svc, deluge := newSearchService(t, db, testFeed(magnetItem("fhd", "Inception 2010 1080p BluRay x264")), nil)

	report, err := svc.SearchMovie(context.Background(), movieID)
	if err != nil {
		t.Fatalf("search movie: %v", err)
	}
	if len(report.Grabbed) != 0 || len(deluge.magnets) != 0 || report.Releases != 1 || len(report.TopRejections) == 0 ||
		report.TopRejections[0] != "Movie already has a file (Unknown) and the quality profile doesn't allow upgrades" {
		t.Fatalf("want nothing grabbed and the reason reported, got %+v", report)
	}
}

func airSeason(t *testing.T, db *sql.DB, seriesID int64, season int, airDate string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE episodes SET air_date = ? WHERE series_id = ? AND season_number = ?`, airDate, seriesID, season); err != nil {
		t.Fatalf("set air dates: %v", err)
	}
}

func TestSearchSeries_NeverGrabsOverlappingReleases(t *testing.T) {
	ctx := context.Background()

	t.Run("a better single episode, then the pack is skipped", func(t *testing.T) {
		db := openTestDB(t)
		seriesID := seedSeries(t, db)
		airSeason(t, db, seriesID, 1, "2008-01-20")
		airSeason(t, db, seriesID, 2, time.Now().AddDate(1, 0, 0).Format("2006-01-02"))
		svc, _ := newSearchService(t, db, testFeed(
			magnetItem("pack", "Breaking Bad S01 720p BluRay x264"),
			magnetItem("e1", "Breaking Bad S01E01 1080p BluRay x264"),
		), nil)

		report, err := svc.SearchSeries(ctx, seriesID, nil)
		if err != nil {
			t.Fatalf("search series: %v", err)
		}
		grabs := listGrabs(t, db)
		if len(report.Grabbed) != 1 || len(grabs) != 1 || grabs[0].EpisodeNumber.Int64 != 1 || grabs[0].SeasonNumber.Int64 != 1 {
			t.Fatalf("want only S01E01 grabbed (the pack overlaps it), got %+v / %+v", report.Grabbed, grabs)
		}
	})

	t.Run("a better pack covers its episodes", func(t *testing.T) {
		db := openTestDB(t)
		seriesID := seedSeries(t, db)
		airSeason(t, db, seriesID, 1, "2008-01-20")
		airSeason(t, db, seriesID, 2, "2009-03-08")
		svc, _ := newSearchService(t, db, testFeed(
			magnetItem("pack", "Breaking Bad S01 1080p BluRay x264"),
			magnetItem("e1", "Breaking Bad S01E01 720p BluRay x264"),
		), nil)

		season := 1
		report, err := svc.SearchSeries(ctx, seriesID, &season)
		if err != nil {
			t.Fatalf("search season: %v", err)
		}
		grabs := listGrabs(t, db)
		if len(report.Grabbed) != 1 || report.Grabbed[0].For != "season 1" || len(grabs) != 1 || grabs[0].EpisodeNumber.Valid {
			t.Fatalf("want just the season 1 pack grabbed, got %+v / %+v", report.Grabbed, grabs)
		}
	})
}

func TestRSSSync(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	movieID := releasedMovie(t, db)
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	svc, deluge := newSearchService(t, db, testFeed(
		magnetItem("fhd", "Inception 2010 1080p BluRay x264"),
		magnetItem("show", "Some Other Show S03E04 1080p WEB-DL"),
		magnetItem("film", "Some Other Film 2024 1080p WEB-DL"),
	), nil)
	svc.Now = func() time.Time { return now }

	if due, err := svc.RSSDue(ctx); err != nil || due {
		t.Fatalf("want RSS sync off by default, got due=%v err=%v", due, err)
	}
	settings, _ := store.GetIndexerSettings(ctx, db)
	settings.RSSSyncInterval = 15
	if err := store.UpdateIndexerOptions(ctx, db, settings); err != nil {
		t.Fatalf("update options: %v", err)
	}
	if due, _ := svc.RSSDue(ctx); !due {
		t.Fatalf("want RSS sync due once an interval is set and it has never run")
	}

	report, err := svc.RSSSync(ctx)
	if err != nil {
		t.Fatalf("rss sync: %v", err)
	}
	grabs := listGrabs(t, db)
	if report.Releases != 3 || len(report.Grabbed) != 1 || len(grabs) != 1 || grabs[0].MovieID.Int64 != movieID || grabs[0].GrabbedBy != PurposeRSS {
		t.Fatalf("want the one wanted movie grabbed by RSS sync, got %+v / %+v", report, grabs)
	}
	settings, _ = store.GetIndexerSettings(ctx, db)
	if !settings.LastRSSSync.Valid || settings.LastRSSResult != "3 releases from 1 indexers, 1 grabbed" {
		t.Fatalf("want the run recorded, got %+v", settings)
	}
	if due, _ := svc.RSSDue(ctx); due {
		t.Fatalf("want RSS sync not due right after running")
	}
	svc.Now = func() time.Time { return now.Add(16 * time.Minute) }
	if due, _ := svc.RSSDue(ctx); !due {
		t.Fatalf("want RSS sync due once the interval has passed")
	}

	again, err := svc.RSSSync(ctx)
	if err != nil || len(again.Grabbed) != 0 || len(deluge.magnets) != 1 {
		t.Fatalf("want nothing grabbed twice while it's queued, got %+v, %v, magnets %v", again, err, deluge.magnets)
	}
}

func TestStartMissingSearch(t *testing.T) {
	db := openTestDB(t)
	releasedMovie(t, db)
	svc, _ := newSearchService(t, db, testFeed(magnetItem("fhd", "Inception 2010 1080p BluRay x264")), nil)

	if !svc.StartMissingSearch(newznab.MediaMovie, SearchMissing) {
		t.Fatalf("want the search to start")
	}
	deadline := time.Now().Add(10 * time.Second)
	for svc.MissingSearchStatus(newznab.MediaMovie, SearchMissing).Running && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	status := svc.MissingSearchStatus(newznab.MediaMovie, SearchMissing)
	if status.Running || status.Total != 1 || status.Done != 1 || status.Grabbed != 1 || status.LastError != "" || status.Finished.IsZero() {
		t.Fatalf("want one movie searched and grabbed, got %+v", status)
	}
	if len(listGrabs(t, db)) != 1 {
		t.Fatalf("want the grab recorded")
	}
}
