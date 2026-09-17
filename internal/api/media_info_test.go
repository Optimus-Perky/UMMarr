package api_test

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
)

var dvInfo = mediainfo.Info{
	Schema: mediainfo.Schema, Source: mediainfo.SourceFFprobe, AnalyzedAt: time.Date(2026, 9, 15, 20, 0, 0, 0, time.UTC),
	Container: "MKV", RunTimeSeconds: 5052, VideoCodec: "x265", VideoBitDepth: 10, VideoDynamicRangeType: "DV HDR10", VideoDynamicRange: "HDR",
	Width: 3840, Height: 2076, VideoFPS: 23.976, AudioCodec: "TrueHD Atmos", AudioChannels: 7.1, AudioStreamCount: 2,
	AudioLanguages: []string{"eng", "ger"}, Subtitles: []string{"eng", "fre"},
}

func okProbe(ctx context.Context, path string) (mediainfo.Info, error) { return dvInfo, nil }

func TestMediaManagement_MediaAnalysisSettings(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	_, raw := get(t, srv, "/settings/media-management")
	body := html.UnescapeString(raw)
	for _, want := range []string{
		`name="analyze_video_files" checked`, `name="analyze_audio_files" checked`, `name="plex_media_info">`, "Use Plex Media Info", "Test Plex",
		"FFprobe isn't installed in this UMMarr", "There's no Plex connection", "MediaInfo VideoDynamicRangeType", "MediaInfo AudioBitsPerSample",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q on Media Management", want)
		}
	}
	if strings.Contains(body, "needs a media probe") {
		t.Error("the Not active yet note must be gone")
	}

	store.CreateNotification(t.Context(), db, store.Notification{Name: "Plex", Implementation: store.NotifyPlex, Enabled: true, Settings: map[string]string{"server_url": "http://plex:32400", "token": "t"}})
	withProbe := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Import: &sync.ImportService{DB: db}, MediaInfo: &sync.MediaAnalyzer{DB: db, Probe: okProbe}}))
	defer withProbe.Close()
	_, raw = get(t, withProbe, "/settings/media-management")
	body = html.UnescapeString(raw)
	if strings.Contains(body, "FFprobe isn't installed in this UMMarr") || strings.Contains(body, "There's no Plex connection") {
		t.Error("want the warnings gone once FFprobe and a Plex connection exist")
	}
}

func TestMediaManagement_TestPlexButton(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	movieID := seedTestMovieWithRootFolder(t, db, t.TempDir())
	md, _, _ := store.GetMovieDetail(ctx, db, movieID)
	store.InsertMovieFile(ctx, db, movieID, "Inception.mkv", 1)
	plex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "t" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/library/sections":
			w.Write([]byte(`{"MediaContainer":{"Directory":[{"key":"1","type":"movie","title":"Movies"},{"key":"2","type":"show","title":"TV Shows"}]}}`))
		case "/library/sections/1/all":
			w.Write([]byte(`{"MediaContainer":{"size":2,"totalSize":2,"Metadata":[{"Media":[{"videoCodec":"hevc","Part":[{"file":"` + md.Path.String + `/Inception.mkv"}]}]},{"Media":[{"videoCodec":"h264","Part":[{"file":"/data/Movies/Elsewhere/x.mkv"}]}]}]}}`))
		default:
			w.Write([]byte(`{"MediaContainer":{"size":0,"totalSize":0}}`))
		}
	}))
	defer plex.Close()
	srv := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Import: &sync.ImportService{DB: db}, MediaInfo: &sync.MediaAnalyzer{DB: db}}))
	defer srv.Close()

	_, raw := postForm(t, srv, "/settings/media-management/plex-media-info/test", url.Values{})
	if body := html.UnescapeString(raw); !strings.Contains(body, "no Plex connection") {
		t.Fatalf("want the missing connection explained, got:\n%s", body)
	}
	store.CreateNotification(ctx, db, store.Notification{Name: "Plex", Implementation: store.NotifyPlex, Enabled: true, Settings: map[string]string{"server_url": plex.URL, "token": "t"}})
	_, raw = postForm(t, srv, "/settings/media-management/plex-media-info/test", url.Values{})
	if body := html.UnescapeString(raw); !strings.Contains(body, "Plex answered with 2 libraries (Movies, TV Shows) holding 2 files. 1 of UMMarr's 1 files are in Plex at the same path.") {
		t.Fatalf("want the match counted, got:\n%s", body)
	}
	var saved string
	db.QueryRow(`SELECT media_info FROM movie_files`).Scan(&saved)
	if saved != "{}" {
		t.Fatalf("Test Plex must not save anything, got %s", saved)
	}
}

func TestMovieDetail_ShowsMediaInfo(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()
	movieID := seedTestMovieWithRootFolder(t, db, t.TempDir())
	fileID, _ := store.InsertMovieFile(ctx, db, movieID, "Inception (2010).mkv", 1)
	_, raw := get(t, srv, "/movies/"+itoa(movieID))
	if !strings.Contains(raw, "Media info: not analyzed yet.") || !strings.Contains(raw, "<th>Video</th><th>Audio</th><th>Languages</th><th>Subtitles</th>") {
		t.Fatalf("want the new columns and the not-analyzed note, got:\n%s", raw)
	}
	store.SaveMediaInfo(ctx, db, "movie", fileID, dvInfo)
	_, raw = get(t, srv, "/movies/"+itoa(movieID))
	body := html.UnescapeString(raw)
	for _, want := range []string{"x265 · 10-bit · DV HDR10", "TrueHD Atmos 7.1 +1 more", "<td>English, German</td>", "<td>English, French</td>", "3840x2076 · 23.976 fps · 1h 24m · MKV · read by FFprobe"} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q on the movie page", want)
		}
	}
	store.SaveMediaInfo(ctx, db, "movie", fileID, mediainfo.Failed(mediainfo.SourceFFprobe, errors.New("ffprobe: Invalid data found"), time.Now()))
	_, raw = get(t, srv, "/movies/"+itoa(movieID))
	if body := html.UnescapeString(raw); !strings.Contains(body, "the file couldn't be read (ffprobe: Invalid data found)") {
		t.Fatalf("want the read failure shown, got:\n%s", body)
	}
}

func TestSeriesPage_MediaInfoColumnsAndEpisodeDialog(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()
	seriesID := seedTestSeries(t, db)
	var e1 int64
	db.QueryRow(`SELECT id FROM episodes WHERE series_id = ?`, seriesID).Scan(&e1)
	fileID, _ := store.AttachEpisodeFile(ctx, db, e1, "Season 01/Breaking Bad - S01E01.mkv", 1)
	store.SaveMediaInfo(ctx, db, "episode", fileID, dvInfo)

	_, raw := get(t, srv, "/tv/"+itoa(seriesID))
	body := html.UnescapeString(raw)
	for _, want := range []string{
		`data-default-hidden="dynamicrange,audiolangs,subtitles"`, `<td data-col="videocodec">x265</td>`, `<td data-col="audioinfo">TrueHD Atmos 7.1</td>`,
		`<td data-col="dynamicrange">DV HDR10</td>`, `<td data-col="audiolangs">English, German</td>`, `<td data-col="subtitles">English, French</td>`,
		`data-col="videocodec"> Video Codec</label>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q on the series page", want)
		}
	}
	_, raw = get(t, srv, "/tv/"+itoa(seriesID)+"/episodes/"+itoa(e1))
	body = html.UnescapeString(raw)
	for _, want := range []string{"<th>Video</th><th>Audio</th><th>Subtitles</th>", "x265 · 10-bit · DV HDR10", "TrueHD Atmos 7.1 +1 more", "English, German", "read by FFprobe"} {
		if !strings.Contains(body, want) {
			t.Errorf("want %q in the episode dialog", want)
		}
	}
}

func TestAlbumDetail_TrackAudioColumn(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()
	albumID := seedTestAlbum(t, db)
	tracks, _ := store.ListTracksForAlbum(ctx, db, albumID)
	fileID, _ := store.AttachTrackFile(ctx, db, tracks[0].ID, "01 - Daftendirekt.flac", 1)
	store.SaveMediaInfo(ctx, db, "track", fileID, mediainfo.Info{Schema: mediainfo.Schema, Source: mediainfo.SourceFFprobe, AudioCodec: "FLAC", AudioFormat: "flac", AudioBitsPerSample: 24, AudioSampleRate: 44100})
	_, raw := get(t, srv, "/music/albums/"+itoa(albumID))
	body := html.UnescapeString(raw)
	if !strings.Contains(body, `<td data-col="audio">FLAC · 24-bit · 44.1 kHz</td>`) || !strings.Contains(body, `data-col="audio" checked> Audio`) {
		t.Fatalf("want the track's audio shown with a column toggle, got:\n%s", body)
	}
}

func TestSystemStatus_MediaAnalysisLine(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	srv := newTestServerWithDB(t, db)
	_, raw := get(t, srv, "/system/status")
	if body := html.UnescapeString(raw); !strings.Contains(body, "<dt>Media analysis</dt><dd>FFprobe isn't installed, so files aren't analyzed.</dd>") {
		t.Fatalf("want FFprobe's absence on the status page, got:\n%s", body)
	}
	movieID := seedTestMovieWithRootFolder(t, db, t.TempDir())
	fileID, _ := store.InsertMovieFile(ctx, db, movieID, "a.mkv", 1)
	store.InsertMovieFile(ctx, db, movieID, "b.mkv", 1)
	store.SaveMediaInfo(ctx, db, "movie", fileID, mediainfo.Info{Schema: mediainfo.Schema, Source: mediainfo.SourcePlex, VideoCodec: "h264"})
	withProbe := httptest.NewServer(api.NewRouter(api.Deps{DB: db, Import: &sync.ImportService{DB: db}, MediaInfo: &sync.MediaAnalyzer{DB: db, Probe: okProbe}}))
	defer withProbe.Close()
	_, raw = get(t, withProbe, "/system/status")
	if body := html.UnescapeString(raw); !strings.Contains(body, "<dd>1 of 2 files analyzed (1 from Plex)</dd>") {
		t.Fatalf("want the progress line, got:\n%s", body)
	}
}
