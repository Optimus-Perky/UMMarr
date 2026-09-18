package store_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Lidarr's artist Monitor dropdown, ported to albums: each option decides
// which of an artist's albums stay monitored. The cases that matter are
// the ones where "released" and "has files" disagree - an announced album
// with no date must keep being watched, which is why Future and Missing
// both keep it.

// seedMonitorArtist makes one artist with three albums: an old one with
// files, an old one without, and an announced one with no release date.
func seedMonitorArtist(t *testing.T, db *sql.DB) (artistRowID int64, withFiles, without, announced int64) {
	t.Helper()
	ctx := context.Background()
	metadataID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name: metadata.Field[string]{Value: "Portishead", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "portishead"},
	})
	if err != nil {
		t.Fatalf("upsert artist: %v", err)
	}
	rootID, err := store.CreateRootFolder(ctx, db, "/data/Music", "music")
	if err != nil {
		t.Fatalf("root folder: %v", err)
	}
	res, err := db.ExecContext(ctx, `INSERT INTO artists (artist_metadata_id, root_folder_id, monitored, added, path) VALUES (?, ?, 1, CURRENT_TIMESTAMP, '/data/Music/Portishead')`, metadataID, rootID)
	if err != nil {
		t.Fatalf("insert artist: %v", err)
	}
	artistRowID, _ = res.LastInsertId()

	old := time.Date(1994, 8, 22, 0, 0, 0, 0, time.UTC)
	newer := time.Date(1997, 9, 30, 0, 0, 0, 0, time.UTC)
	album := func(title, mbid string, date *time.Time) int64 {
		m := metadata.AlbumMetadata{
			Title:       metadata.Field[string]{Value: title, Provider: "musicbrainz"},
			ExternalIDs: map[string]string{"musicbrainz": mbid},
		}
		if date != nil {
			m.ReleaseDate = metadata.Field[*time.Time]{Value: date, Provider: "musicbrainz"}
		}
		id, _, err := store.UpsertAlbum(ctx, db, metadataID, m)
		if err != nil {
			t.Fatalf("upsert album %s: %v", title, err)
		}
		return id
	}
	withFiles, without, announced = album("Dummy", "dummy", &old), album("Portishead", "self-titled", &newer), album("Fourth", "fourth", nil)

	// Give Dummy one imported track, so "has files" is true for it alone.
	releaseID, err := store.UpsertAlbumRelease(ctx, db, withFiles, metadata.ReleaseMetadata{
		Title: metadata.Field[string]{Value: "Dummy", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "dummy-release"},
	})
	if err != nil {
		t.Fatalf("upsert release: %v", err)
	}
	trackID, err := store.UpsertTrack(ctx, db, releaseID, metadataID, metadata.TrackSource{Number: "1", Title: "Mysterons", MediumNumber: 1})
	if err != nil {
		t.Fatalf("upsert track: %v", err)
	}
	fileRes, err := db.ExecContext(ctx, `INSERT INTO track_files (relative_path, size, quality) VALUES ('Dummy/01 Mysterons.flac', 1000, '{}')`)
	if err != nil {
		t.Fatalf("insert track file: %v", err)
	}
	fileID, _ := fileRes.LastInsertId()
	if _, err := db.ExecContext(ctx, `UPDATE tracks SET track_file_id = ? WHERE id = ?`, fileID, trackID); err != nil {
		t.Fatalf("attach track file: %v", err)
	}
	return artistRowID, withFiles, without, announced
}

func monitoredAlbums(t *testing.T, db *sql.DB, ids ...int64) []bool {
	t.Helper()
	out := make([]bool, 0, len(ids))
	for _, id := range ids {
		var monitored bool
		if err := db.QueryRow(`SELECT monitored FROM albums WHERE id = ?`, id).Scan(&monitored); err != nil {
			t.Fatalf("read album %d: %v", id, err)
		}
		out = append(out, monitored)
	}
	return out
}

func TestApplyAlbumMonitorOption(t *testing.T) {
	db := openTestDB(t)
	artistID, withFiles, without, announced := seedMonitorArtist(t, db)

	cases := []struct {
		option string
		// want is [Dummy (released, has files), Portishead (released, no
		// files), Fourth (announced, no date)].
		want [3]bool
	}{
		{"all", [3]bool{true, true, true}},
		{"none", [3]bool{false, false, false}},
		{"future", [3]bool{false, false, true}},
		{"missing", [3]bool{false, true, true}},
		{"existing", [3]bool{true, false, true}},
		{"first", [3]bool{true, false, false}},
		{"latest", [3]bool{false, true, false}},
	}
	for _, c := range cases {
		if err := store.ApplyAlbumMonitorOption(context.Background(), db, artistID, c.option); err != nil {
			t.Fatalf("%s: %v", c.option, err)
		}
		got := monitoredAlbums(t, db, withFiles, without, announced)
		if [3]bool{got[0], got[1], got[2]} != c.want {
			t.Errorf("%s: monitored = [Dummy %v, Portishead %v, Fourth %v], want [%v %v %v]",
				c.option, got[0], got[1], got[2], c.want[0], c.want[1], c.want[2])
		}
	}
}

func TestApplyAlbumMonitorOption_RejectsUnknownOption(t *testing.T) {
	db := openTestDB(t)
	artistID, withFiles, _, _ := seedMonitorArtist(t, db)
	if err := store.ApplyAlbumMonitorOption(context.Background(), db, artistID, "everything"); err == nil {
		t.Fatal("want an unknown monitor option refused")
	}
	if got := monitoredAlbums(t, db, withFiles); !got[0] {
		t.Error("a refused option must leave the albums as they were")
	}
}

// ListUpgradableAlbums is the mirror of ListWantedAlbums: the cutoff-unmet
// search looks at albums that DO have files, and would otherwise search the
// whole library for upgrades it can't judge.
func TestListUpgradableAlbums_OnlyAlbumsWithFiles(t *testing.T) {
	db := openTestDB(t)
	_, withFiles, _, _ := seedMonitorArtist(t, db)
	ctx := context.Background()
	if err := store.ApplyAlbumMonitorOption(ctx, db, 1, "all"); err != nil {
		t.Fatalf("monitor all: %v", err)
	}

	albums, err := store.ListUpgradableAlbums(ctx, db)
	if err != nil {
		t.Fatalf("list upgradable: %v", err)
	}
	if len(albums) != 1 || albums[0].ID != withFiles {
		var got []string
		for _, a := range albums {
			got = append(got, a.Title)
		}
		t.Fatalf("want only the album with files, got %v", got)
	}

	qualities, err := store.AlbumFileQualities(ctx, db, withFiles)
	if err != nil {
		t.Fatalf("album qualities: %v", err)
	}
	if len(qualities) != 1 {
		t.Fatalf("want one file quality for the imported track, got %d", len(qualities))
	}
}

// Albums match their files positionally, so a file can land on the wrong
// track. Remapping moves it, leaves the track it came from empty, and
// never lets two tracks share one file row.
func TestRemapTrackFile(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	_, withFiles, _, _ := seedMonitorArtist(t, db)

	var releaseID int64
	if err := db.QueryRow(`SELECT id FROM album_releases WHERE album_id = ?`, withFiles).Scan(&releaseID); err != nil {
		t.Fatalf("find release: %v", err)
	}
	second, err := store.UpsertTrack(ctx, db, releaseID, 1, metadata.TrackSource{Number: "2", Title: "Sour Times", MediumNumber: 1})
	if err != nil {
		t.Fatalf("second track: %v", err)
	}
	files, err := store.ListTrackFileDetails(ctx, db, withFiles)
	if err != nil || len(files) != 1 {
		t.Fatalf("list track files: %v %+v", err, files)
	}
	first := files[0]

	if err := store.RemapTrackFile(ctx, db, withFiles, first.ID, second); err != nil {
		t.Fatalf("remap: %v", err)
	}
	files, err = store.ListTrackFileDetails(ctx, db, withFiles)
	if err != nil || len(files) != 1 {
		t.Fatalf("after remap: %v %+v", err, files)
	}
	if files[0].TrackID != second {
		t.Errorf("want the file on track %d, got %d", second, files[0].TrackID)
	}
	var onOldTrack sql.NullInt64
	if err := db.QueryRow(`SELECT track_file_id FROM tracks WHERE id = ?`, first.TrackID).Scan(&onOldTrack); err != nil {
		t.Fatalf("read old track: %v", err)
	}
	if onOldTrack.Valid {
		t.Error("want the track it came from left with no file")
	}
	if err := store.RemapTrackFile(ctx, db, withFiles, first.ID, 99999); err == nil {
		t.Error("want a track from another album refused")
	}
}
