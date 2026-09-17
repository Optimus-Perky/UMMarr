package sync

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// A search run from the series page has no season to pass on, so the grab
// used to record none - which reads as "the whole series" and put a
// Downloading badge on every episode of every season. The release title
// says which season it is; a grab should read it.
func TestGrabSeries_SeasonPackFromASeriesSearchCoversOnlyItsSeason(t *testing.T) {
	db := openTestDB(t)
	seriesID := seedSeries(t, db)
	title := "Breaking Bad S02 1080p DSNP WEB-DL AAC2 0 H 264-FLUX"
	svc, _ := newSearchService(t, db, testFeed(magnetItem("pack", title)), nil)
	download := svc.Download

	release := newznab.Release{Title: title, MagnetURL: "magnet:?xt=urn:btih:pack", Protocol: "torrent", IndexerID: onlyIndexerID(t, db)}
	if _, err := download.GrabSeries(context.Background(), seriesID, nil, release, PurposeInteractive); err != nil {
		t.Fatalf("grab series: %v", err)
	}

	g := onlyGrab(t, db)
	if !g.SeasonNumber.Valid || g.SeasonNumber.Int64 != 2 {
		t.Errorf("want the grab recorded against season 2, got %+v", g.SeasonNumber)
	}
	if g.EpisodeNumber.Valid {
		t.Errorf("a season pack covers the whole season, not episode %d", g.EpisodeNumber.Int64)
	}

	statuses, err := store.EpisodeGrabStatuses(context.Background(), db, seriesID)
	if err != nil {
		t.Fatalf("episode grab statuses: %v", err)
	}
	for _, e := range listEpisodes(t, db, seriesID) {
		_, downloading := statuses[e.id]
		if want := e.season == 2; downloading != want {
			t.Errorf("s%02de%02d: downloading = %v, want %v", e.season, e.episode, downloading, want)
		}
	}
}

// A release naming one episode claims that episode alone.
func TestGrabSeries_SingleEpisodeFromASeriesSearchCoversOnlyThatEpisode(t *testing.T) {
	db := openTestDB(t)
	seriesID := seedSeries(t, db)
	title := "Breaking Bad S01E02 1080p WEB H264-GROUP"
	svc, _ := newSearchService(t, db, testFeed(magnetItem("one", title)), nil)
	download := svc.Download

	release := newznab.Release{Title: title, MagnetURL: "magnet:?xt=urn:btih:one", Protocol: "torrent", IndexerID: onlyIndexerID(t, db)}
	if _, err := download.GrabSeries(context.Background(), seriesID, nil, release, PurposeInteractive); err != nil {
		t.Fatalf("grab series: %v", err)
	}

	g := onlyGrab(t, db)
	if !g.SeasonNumber.Valid || g.SeasonNumber.Int64 != 1 || !g.EpisodeNumber.Valid || g.EpisodeNumber.Int64 != 2 {
		t.Errorf("want s01e02, got season %+v episode %+v", g.SeasonNumber, g.EpisodeNumber)
	}
}

// Grabs made before that worked are already in the download client, so
// startup re-reads their titles.
func TestBackfillGrabCoverage_RepairsGrabsThatClaimTheWholeSeries(t *testing.T) {
	db := openTestDB(t)
	seriesID := seedSeries(t, db)
	ctx := context.Background()
	download := &DownloadService{DB: db}

	pack, err := store.InsertGrab(ctx, db, store.Grab{
		SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}, Status: "downloading",
		ReleaseTitle: "Breaking Bad S02 1080p DSNP WEB-DL AAC2 0 H 264-FLUX", Protocol: "torrent",
	})
	if err != nil {
		t.Fatalf("insert season pack grab: %v", err)
	}
	whole, err := store.InsertGrab(ctx, db, store.Grab{
		SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}, Status: "downloading",
		ReleaseTitle: "Breaking Bad Complete Series 1080p BluRay x264", Protocol: "torrent",
	})
	if err != nil {
		t.Fatalf("insert whole series grab: %v", err)
	}

	if err := download.BackfillGrabCoverage(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	byID := map[int64]store.Grab{}
	for _, g := range listGrabs(t, db) {
		byID[g.ID] = g
	}
	if got := byID[pack].SeasonNumber; !got.Valid || got.Int64 != 2 {
		t.Errorf("the season pack should now cover season 2, got %+v", got)
	}
	if got := byID[whole].SeasonNumber; got.Valid {
		t.Errorf("a real whole-series pack should be left alone, got season %d", got.Int64)
	}
}

type testEpisode struct {
	id      int64
	season  int
	episode int
}

func listEpisodes(t *testing.T, db *sql.DB, seriesID int64) []testEpisode {
	t.Helper()
	rows, err := db.Query(`SELECT id, season_number, episode_number FROM episodes WHERE series_id = ? ORDER BY season_number, episode_number`, seriesID)
	if err != nil {
		t.Fatalf("list episodes: %v", err)
	}
	defer rows.Close()
	var out []testEpisode
	for rows.Next() {
		var e testEpisode
		if err := rows.Scan(&e.id, &e.season, &e.episode); err != nil {
			t.Fatalf("scan episode: %v", err)
		}
		out = append(out, e)
	}
	if len(out) == 0 {
		t.Fatal("the seeded series should have episodes")
	}
	return out
}

func onlyGrab(t *testing.T, db *sql.DB) store.Grab {
	t.Helper()
	grabs := listGrabs(t, db)
	if len(grabs) != 1 {
		t.Fatalf("want exactly one grab, got %d", len(grabs))
	}
	return grabs[0]
}

func onlyIndexerID(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT id FROM indexers`).Scan(&id); err != nil {
		t.Fatalf("read test indexer: %v", err)
	}
	return id
}

// A genuine whole-series pack still covers the series, but it cannot be
// downloading an episode that has not aired - that one is Unreleased.
func TestEpisodeGrabStatuses_WholeSeriesGrabSkipsUnairedEpisodes(t *testing.T) {
	db := openTestDB(t)
	seriesID := seedSeries(t, db)
	ctx := context.Background()

	episodes := listEpisodes(t, db, seriesID)
	aired, unaired := episodes[0], episodes[len(episodes)-1]
	if _, err := db.Exec(`UPDATE episodes SET air_date = date('now', '-30 days') WHERE id = ?`, aired.id); err != nil {
		t.Fatalf("set aired date: %v", err)
	}
	if _, err := db.Exec(`UPDATE episodes SET air_date = date('now', '+30 days') WHERE id = ?`, unaired.id); err != nil {
		t.Fatalf("set unaired date: %v", err)
	}
	if _, err := store.InsertGrab(ctx, db, store.Grab{
		SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}, Status: "downloading",
		ReleaseTitle: "Breaking Bad Complete Series 1080p BluRay x264", Protocol: "torrent",
	}); err != nil {
		t.Fatalf("insert whole series grab: %v", err)
	}

	statuses, err := store.EpisodeGrabStatuses(ctx, db, seriesID)
	if err != nil {
		t.Fatalf("episode grab statuses: %v", err)
	}
	if _, ok := statuses[aired.id]; !ok {
		t.Error("an aired episode should be covered by the whole-series grab")
	}
	if _, ok := statuses[unaired.id]; ok {
		t.Error("an episode that has not aired cannot be downloading")
	}
}

// A check that outlives its timeout keeps running detached and writes
// last_checked only when it finishes, so the grab still looks overdue.
// Without a guard the next tick starts a second copy of the same import -
// live on 2026-09-16 that re-copied an eight-file season pack every few
// minutes and attached three episode_files rows to one episode.
func TestCheckOneGrab_DoesNotStartASecondCheckWhileOneIsRunning(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	download := &DownloadService{DB: db}

	release := "Breaking Bad S01E01 1080p WEB H264-GROUP"
	grabID, err := store.InsertGrab(ctx, db, store.Grab{
		SeriesID: sql.NullInt64{Int64: seedSeries(t, db), Valid: true}, Status: "downloading",
		ReleaseTitle: release, Protocol: "torrent",
	})
	if err != nil {
		t.Fatalf("insert grab: %v", err)
	}
	g := store.Grab{ID: grabID, ReleaseTitle: release, Status: "downloading"}

	if !download.beginCheck(g.ID) {
		t.Fatal("the first check should claim the grab")
	}
	if download.beginCheck(g.ID) {
		t.Error("a second check claimed a grab that is already being processed")
	}

	// A different grab is unaffected.
	if !download.beginCheck(g.ID + 1) {
		t.Error("another grab should still be checkable")
	}

	download.endCheck(g.ID)
	if !download.beginCheck(g.ID) {
		t.Error("once the first check finishes the grab should be checkable again")
	}
}

// An episode has one file: re-importing replaces it rather than leaving the
// old row behind with nothing pointing at it.
func TestAttachEpisodeFile_ReplacesTheEpisodesExistingFile(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seriesID := seedSeries(t, db)
	episodeID := listEpisodes(t, db, seriesID)[0].id

	first, err := store.AttachEpisodeFile(ctx, db, episodeID, "Season 01/first.mkv", 100)
	if err != nil {
		t.Fatalf("attach first: %v", err)
	}
	second, err := store.AttachEpisodeFile(ctx, db, episodeID, "Season 01/second.mkv", 200)
	if err != nil {
		t.Fatalf("attach second: %v", err)
	}

	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM episode_files WHERE episode_id = ?`, episodeID).Scan(&rows); err != nil {
		t.Fatalf("count episode_files: %v", err)
	}
	if rows != 1 {
		t.Errorf("want one file row for the episode, got %d", rows)
	}
	var current int64
	if err := db.QueryRow(`SELECT episode_file_id FROM episodes WHERE id = ?`, episodeID).Scan(&current); err != nil {
		t.Fatalf("read episode pointer: %v", err)
	}
	if current != second {
		t.Errorf("the episode should point at the new file %d, got %d", second, current)
	}
	var stale int
	if err := db.QueryRow(`SELECT COUNT(*) FROM episode_files WHERE id = ?`, first).Scan(&stale); err != nil {
		t.Fatalf("count stale row: %v", err)
	}
	if stale != 0 {
		t.Error("the replaced file row should be gone, not orphaned")
	}
}

// Migration 34 clears the file rows the duplicate imports left behind.
func TestMigration_ClearsOrphanedEpisodeFileRows(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	episodeID := listEpisodes(t, db, seedSeries(t, db))[0].id

	kept, err := store.AttachEpisodeFile(ctx, db, episodeID, "Season 01/kept.mkv", 100)
	if err != nil {
		t.Fatalf("attach kept: %v", err)
	}
	// An orphan of the shape the old import produced: a row for the episode
	// that the episode does not point at.
	var orphan int64
	if err := db.QueryRow(`INSERT INTO episode_files (episode_id, relative_path, size) VALUES (?, ?, ?) RETURNING id`,
		episodeID, "Season 01/orphan.mkv", 200).Scan(&orphan); err != nil {
		t.Fatalf("insert orphan: %v", err)
	}
	if _, err := db.Exec(`UPDATE episodes SET episode_file_id = ? WHERE id = ?`, kept, episodeID); err != nil {
		t.Fatalf("point episode back at the kept file: %v", err)
	}

	if _, err := db.Exec(`DELETE FROM episode_files
		WHERE id NOT IN (SELECT episode_file_id FROM episodes WHERE episode_file_id IS NOT NULL)`); err != nil {
		t.Fatalf("run the migration's delete: %v", err)
	}

	var keptRows, orphanRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM episode_files WHERE id = ?`, kept).Scan(&keptRows); err != nil {
		t.Fatalf("count kept: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM episode_files WHERE id = ?`, orphan).Scan(&orphanRows); err != nil {
		t.Fatalf("count orphan: %v", err)
	}
	if keptRows != 1 {
		t.Error("the file the episode points at must survive")
	}
	if orphanRows != 0 {
		t.Error("the orphaned row should be gone")
	}
}
