package store_test

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

type mediaLibrary struct {
	db                                                                               *sql.DB
	movieID, movieFileID, seriesID, episodeFile1, episodeFile2, albumID, trackFileID int64
	moviePath, episodePath, trackPath                                                string
}

func (l *mediaLibrary) pending(t *testing.T) []store.MediaFile {
	t.Helper()
	files, err := store.ListFilesToAnalyze(context.Background(), l.db, true, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// seedMediaLibrary makes a movie with a file, a two-episode file and an
// album track.
func seedMediaLibrary(t *testing.T) *mediaLibrary {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	profile := seedQualityProfile(t, db)
	lib := &mediaLibrary{db: db}

	movieMeta, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{Title: metadata.Field[string]{Value: "Inception", Provider: "tmdb"}, Year: metadata.Field[int]{Value: 2010, Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "27205"}})
	if err != nil {
		t.Fatal(err)
	}
	if lib.movieID, err = store.UpsertMovie(ctx, db, movieMeta, profile, seedRootFolder(t, db, "movie"), true); err != nil {
		t.Fatal(err)
	}
	md, _, _ := store.GetMovieDetail(ctx, db, lib.movieID)
	lib.movieFileID, _ = store.InsertMovieFile(ctx, db, lib.movieID, "Inception (2010).mkv", 1)
	lib.moviePath = md.Path.String + "/Inception (2010).mkv"

	seriesMeta, _ := store.UpsertSeriesMetadata(ctx, db, metadata.SeriesMetadata{Title: metadata.Field[string]{Value: "Show", Provider: "tmdb"}, ExternalIDs: map[string]string{"tmdb": "1"}})
	lib.seriesID, _ = store.UpsertSeries(ctx, db, seriesMeta, profile, seedRootFolder(t, db, "series"), true)
	seasonID, _ := store.UpsertSeason(ctx, db, lib.seriesID, metadata.SeasonMetadata{SeasonNumber: 1})
	e1, _ := store.UpsertEpisode(ctx, db, lib.seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: 1, Title: metadata.Field[string]{Value: "A", Provider: "tmdb"}})
	e2, _ := store.UpsertEpisode(ctx, db, lib.seriesID, seasonID, 1, metadata.EpisodeMetadata{EpisodeNumber: 2, Title: metadata.Field[string]{Value: "B", Provider: "tmdb"}})
	sd, _, _ := store.GetSeriesDetail(ctx, db, lib.seriesID)
	lib.episodeFile1, _ = store.AttachEpisodeFile(ctx, db, e1, "Season 01/Show - S01E01-E02.mkv", 1)
	lib.episodeFile2, _ = store.AttachEpisodeFile(ctx, db, e2, "Season 01/Show - S01E01-E02.mkv", 1)
	lib.episodePath = sd.Path.String + "/Season 01/Show - S01E01-E02.mkv"

	artistMeta, _ := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{Name: metadata.Field[string]{Value: "Daft Punk", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "dp"}})
	store.UpsertArtist(ctx, db, artistMeta, profile, seedRootFolder(t, db, "music"), true)
	lib.albumID, _, _ = store.UpsertAlbum(ctx, db, artistMeta, metadata.AlbumMetadata{Title: metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "hw"}})
	releaseID, _ := store.UpsertAlbumRelease(ctx, db, lib.albumID, metadata.ReleaseMetadata{Title: metadata.Field[string]{Value: "Homework", Provider: "musicbrainz"}, ExternalIDs: map[string]string{"musicbrainz": "hwr"}})
	trackID, err := store.UpsertTrack(ctx, db, releaseID, artistMeta, metadata.TrackSource{Number: "1", Title: "Daftendirekt", DurationMs: 1000, MediumNumber: 1})
	if err != nil {
		t.Fatal(err)
	}
	store.SetAlbumPathTo(ctx, db, lib.albumID, "/media/music/Daft Punk/Homework")
	lib.trackFileID, _ = store.AttachTrackFile(ctx, db, trackID, "01 - Daftendirekt.flac", 1)
	lib.trackPath = "/media/music/Daft Punk/Homework/01 - Daftendirekt.flac"
	return lib
}

func key(kind string, id int64) string { return kind + ":" + strconv.FormatInt(id, 10) }

func TestMediaInfo_PendingSaveResetCount(t *testing.T) {
	lib := seedMediaLibrary(t)
	db := lib.db
	ctx := context.Background()

	files := lib.pending(t)
	paths := map[string]string{}
	for _, f := range files {
		paths[key(f.Kind, f.ID)] = f.Path
	}
	if len(files) != 4 || paths[key("movie", lib.movieFileID)] != lib.moviePath || paths[key("episode", lib.episodeFile1)] != lib.episodePath || paths[key("track", lib.trackFileID)] != lib.trackPath {
		t.Fatalf("want the movie, both episode rows and the track with full paths, got %+v", files)
	}
	if v, _ := store.ListFilesToAnalyze(ctx, db, true, false, 0); len(v) != 3 {
		t.Errorf("video only: want 3, got %d", len(v))
	}
	if a, _ := store.ListFilesToAnalyze(ctx, db, false, true, 0); len(a) != 1 || a[0].Kind != "track" {
		t.Errorf("audio only: want the track, got %+v", a)
	}
	if l, _ := store.ListFilesToAnalyze(ctx, db, true, true, 2); len(l) != 2 {
		t.Errorf("limit: want 2, got %d", len(l))
	}
	if all, _ := store.ListMediaFilePaths(ctx, db); len(all) != 3 || !all[lib.episodePath] {
		t.Errorf("want 3 distinct paths, got %v", all)
	}

	now := time.Date(2026, 9, 15, 20, 0, 0, 0, time.UTC)
	movieInfo := mediainfo.Info{Schema: mediainfo.Schema, Source: mediainfo.SourceFFprobe, AnalyzedAt: now, VideoCodec: "x265", Width: 1920, Height: 800, AudioCodec: "TrueHD Atmos", AudioChannels: 7.1, AudioLanguages: []string{"eng", "ger"}}
	if err := store.SaveMediaInfo(ctx, db, "movie", lib.movieFileID, movieInfo); err != nil {
		t.Fatal(err)
	}
	md, _, _ := store.GetMovieDetail(ctx, db, lib.movieID)
	if md.File == nil || md.File.MediaInfo.AudioCodec != "TrueHD Atmos" || md.File.Quality.Resolution != "1080p" || md.File.Quality.Codec != "x265" {
		t.Fatalf("want media info stored and the missing resolution and codec filled in, got %+v", md.File)
	}
	db.Exec(`UPDATE episode_files SET quality = '{"source":"WEBDL","resolution":"720p"}' WHERE id = ?`, lib.episodeFile1)
	store.SaveMediaInfo(ctx, db, "episode", lib.episodeFile1, mediainfo.Info{Schema: mediainfo.Schema, Source: mediainfo.SourcePlex, AnalyzedAt: now, Width: 1920, Height: 1080, VideoCodec: "h264"})
	store.SaveMediaInfo(ctx, db, "episode", lib.episodeFile2, mediainfo.Failed(mediainfo.SourceFFprobe, errors.New("ffprobe: Invalid data"), now))
	eps, _ := store.ListEpisodesForSeries(ctx, db, lib.seriesID)
	if eps[0].Quality.Resolution != "720p" || eps[0].MediaInfo.Source != mediainfo.SourcePlex || eps[1].MediaInfo.Error == "" {
		t.Fatalf("want a resolution from the name kept, and both episode rows' info, got %+v / %+v", eps[0], eps[1])
	}
	if err := store.SaveMediaInfo(ctx, db, "bogus", 1, movieInfo); err == nil {
		t.Error("want an unknown kind refused")
	}

	if left := lib.pending(t); len(left) != 1 || left[0].Kind != "track" {
		t.Fatalf("want only the track left, a failed file isn't retried, got %+v", left)
	}
	c, err := store.CountMediaInfo(ctx, db)
	if err != nil || c != (store.MediaInfoCounts{Total: 4, Analyzed: 2, Failed: 1, FromPlex: 1}) {
		t.Fatalf("counts: %+v (%v)", c, err)
	}

	// An older schema and a value that isn't JSON both count as not analyzed.
	db.Exec(`UPDATE movie_files SET media_info = '{"schema":0}'`)
	db.Exec(`UPDATE track_files SET media_info = 'garbage'`)
	if left := lib.pending(t); len(left) != 2 {
		t.Fatalf("want the stale movie and the garbage track pending, got %+v", left)
	}
	if _, err := store.CountMediaInfo(ctx, db); err != nil {
		t.Fatalf("a bad value mustn't break counting: %v", err)
	}

	if n, err := store.ResetMediaInfo(ctx, db, "series", lib.seriesID); err != nil || n != 2 {
		t.Fatalf("want both episode rows reset, got %d (%v)", n, err)
	}
	if n, _ := store.ResetMediaInfo(ctx, db, "album", lib.albumID); n != 1 {
		t.Errorf("want the track reset, got %d", n)
	}
	if n, _ := store.ResetMediaInfo(ctx, db, "movie", lib.movieID); n != 1 {
		t.Errorf("want the movie reset, got %d", n)
	}
	if len(lib.pending(t)) != 4 {
		t.Error("want everything pending after the resets")
	}
	if _, err := store.ResetMediaInfo(ctx, db, "bogus", 1); err == nil {
		t.Error("want an unknown owner refused")
	}
	tracks, _ := store.ListTracksForAlbum(ctx, db, lib.albumID)
	if len(tracks) != 1 || tracks[0].MediaInfo.Analyzed() {
		t.Errorf("want the reset track unanalyzed, got %+v", tracks)
	}
}

func TestPlexConnection(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, _, found, err := store.PlexConnection(ctx, db); found || err != nil {
		t.Fatalf("want none, got %v %v", found, err)
	}
	store.CreateNotification(ctx, db, store.Notification{Name: "Old Plex", Implementation: store.NotifyPlex, Enabled: false, Settings: map[string]string{"server_url": "http://old:32400", "token": "old"}})
	store.CreateNotification(ctx, db, store.Notification{Name: "Half Plex", Implementation: store.NotifyPlex, Enabled: true, Settings: map[string]string{"server_url": "http://half:32400"}})
	if url, token, found, _ := store.PlexConnection(ctx, db); !found || url != "http://old:32400" || token != "old" {
		t.Fatalf("want the only complete connection, got %q %v", url, found)
	}
	store.CreateNotification(ctx, db, store.Notification{Name: "Plex", Implementation: store.NotifyPlex, Enabled: true, Settings: map[string]string{"server_url": " http://plex:32400 ", "token": "new"}})
	if url, token, _, _ := store.PlexConnection(ctx, db); url != "http://plex:32400" || token != "new" {
		t.Fatalf("want the enabled complete connection first, got %q", url)
	}
}

func TestMediaInfoNamingTokens(t *testing.T) {
	lib := seedMediaLibrary(t)
	ctx := context.Background()
	calls := 0
	store.MediaInfoTokens = func(ctx context.Context, path string) map[string]string {
		calls++
		if path != "/downloads/Inception.2010.mkv" {
			t.Errorf("want the source file probed, got %s", path)
		}
		return map[string]string{"MediaInfo VideoCodec": "x265", "MediaInfo AudioCodec": "DTS"}
	}
	t.Cleanup(func() { store.MediaInfoTokens = nil })
	lib.db.Exec(`UPDATE naming_config SET rename_files = 1 WHERE media_type = 'movie'`)
	name, err := store.ResolveMovieFileName(ctx, lib.db, lib.movieID, "/downloads/Inception.2010.mkv")
	if err != nil || calls != 0 || strings.Contains(name, "x265") {
		t.Fatalf("a template without {MediaInfo} mustn't read the file, got %q calls %d (%v)", name, calls, err)
	}
	lib.db.Exec(`UPDATE naming_config SET movie_file_format = '{Movie Title} {MediaInfo VideoCodec} {MediaInfo AudioCodec}' WHERE media_type = 'movie'`)
	name, err = store.ResolveMovieFileName(ctx, lib.db, lib.movieID, "/downloads/Inception.2010.mkv")
	if err != nil || calls != 1 || !strings.HasPrefix(name, "Inception x265 DTS") {
		t.Fatalf("want the media info tokens in the name, got %q calls %d (%v)", name, calls, err)
	}
}
