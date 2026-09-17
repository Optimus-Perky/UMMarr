package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/customformat"
	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// resolver sets one naming box's template and returns what UMMarr's real
// path or filename resolution makes of it.
type resolver func(t *testing.T, template string) string

func resolved(t *testing.T) func(string, error) string {
	return func(s string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		return s
	}
}

func saved(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("save naming template: %v", err)
	}
}

func seedVariousArtistsCompilation(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	ctx := t.Context()
	vaID, err := store.UpsertArtistMetadata(ctx, db, metadata.ArtistMetadata{
		Name:        metadata.Field[string]{Value: "Various Artists", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "89ad4ac3-39f7-470e-963a-56509c546377"},
	})
	if err != nil {
		t.Fatalf("upsert various artists: %v", err)
	}
	albumID, _, err := store.UpsertAlbum(ctx, db, vaID, metadata.AlbumMetadata{
		Title:       metadata.Field[string]{Value: "Now 37", Provider: "musicbrainz"},
		ExternalIDs: map[string]string{"musicbrainz": "now-37-rg-mbid"},
	})
	if err != nil {
		t.Fatalf("upsert compilation album: %v", err)
	}
	seriesID, err := store.UpsertCompilationSeries(ctx, db, "Now That's What I Call Music", "Now That's What I Call Music", "now-series-mbid")
	if err != nil {
		t.Fatalf("upsert compilation series: %v", err)
	}
	if err := store.LinkAlbumToCompilationSeries(ctx, db, seriesID, albumID, 37); err != nil {
		t.Fatalf("link album to series: %v", err)
	}
	return albumID
}

// namingResolvers wires every naming box to the store function that really
// uses it. Each media type gets its own database so the seed helpers don't
// collide.
func namingResolvers(t *testing.T) map[string]resolver {
	t.Helper()
	ctx := t.Context()

	// {Custom Formats} only lists formats set to be included when renaming,
	// so each fixture needs one that matches its file.
	seedRenamingFormat := func(db *sql.DB) {
		t.Helper()
		if _, err := store.SaveCustomFormat(ctx, db, customformat.Format{Name: "x265", IncludeWhenRenaming: true,
			Conditions: []customformat.Condition{{Implementation: customformat.ReleaseTitle, Value: `x265|H\.264`}}}); err != nil {
			t.Fatalf("seed custom format: %v", err)
		}
	}
	movieDB := openTestDB(t)
	movieID := seedTestMovie(t, movieDB)
	seedRenamingFormat(movieDB)

	seriesDB := openTestDB(t)
	seriesID := seedTestSeries(t, seriesDB)
	seedRenamingFormat(seriesDB)
	var episodeID int64
	if err := seriesDB.QueryRowContext(ctx, `SELECT id FROM episodes WHERE series_id = ? AND season_number = 1 AND episode_number = 1`, seriesID).Scan(&episodeID); err != nil {
		t.Fatalf("find episode: %v", err)
	}

	musicDB := openTestDB(t)
	albumID := seedTestAlbum(t, musicDB)
	if _, err := musicDB.ExecContext(ctx, `UPDATE albums SET release_date = '1997-01-20' WHERE id = ?`, albumID); err != nil {
		t.Fatalf("set album release date: %v", err)
	}
	var artistID, trackID int64
	if err := musicDB.QueryRowContext(ctx, `SELECT id FROM artists LIMIT 1`).Scan(&artistID); err != nil {
		t.Fatalf("find artist: %v", err)
	}
	if err := musicDB.QueryRowContext(ctx, `SELECT id FROM tracks LIMIT 1`).Scan(&trackID); err != nil {
		t.Fatalf("find track: %v", err)
	}
	vaAlbumID := seedVariousArtistsCompilation(t, musicDB)

	const (
		artist  = "{Artist Name}"
		album   = "{Album Title}"
		vaSer   = "Various Artists/{Series Name}"
		track   = "{Track Title}"
		series  = "{Series Title}"
		season  = "Season {season}"
		episode = "{Series Title}"
	)
	return map[string]resolver{
		"movie/movie_folder_format": func(t *testing.T, tpl string) string {
			saved(t, store.UpdateMovieNamingConfig(ctx, movieDB, tpl, "{Movie Title}"))
			return resolved(t)(store.ResolveMoviePath(ctx, movieDB, movieID))
		},
		"movie/movie_file_format": func(t *testing.T, tpl string) string {
			saved(t, store.UpdateMovieNamingConfig(ctx, movieDB, "{Movie Title}", tpl))
			return resolved(t)(store.ResolveMovieFileName(ctx, movieDB, movieID, "Inception.2010.1080p.BluRay.x265-GRP.mkv"))
		},
		"series/series_folder_format": func(t *testing.T, tpl string) string {
			saved(t, store.UpdateSeriesNamingConfig(ctx, seriesDB, tpl, season, episode))
			return resolved(t)(store.ResolveSeriesPath(ctx, seriesDB, seriesID))
		},
		"series/season_folder_format": func(t *testing.T, tpl string) string {
			saved(t, store.UpdateSeriesNamingConfig(ctx, seriesDB, series, tpl, episode))
			return resolved(t)(store.ResolveEpisodeFolderPath(ctx, seriesDB, seriesID, 1))
		},
		"series/episode_file_format": func(t *testing.T, tpl string) string {
			saved(t, store.UpdateSeriesNamingConfig(ctx, seriesDB, series, season, tpl))
			return resolved(t)(store.ResolveEpisodeFileName(ctx, seriesDB, episodeID, "Breaking.Bad.S01E01.1080p.WEB-DL.H.264-GRP.mkv"))
		},
		"music/artist_folder_format": func(t *testing.T, tpl string) string {
			saved(t, store.UpdateMusicNamingConfig(ctx, musicDB, tpl, album, vaSer, track))
			return resolved(t)(store.ResolveArtistPath(ctx, musicDB, artistID))
		},
		"music/album_folder_format": func(t *testing.T, tpl string) string {
			saved(t, store.UpdateMusicNamingConfig(ctx, musicDB, artist, tpl, vaSer, track))
			return resolved(t)(store.ResolveAlbumPath(ctx, musicDB, albumID))
		},
		"music/va_series_folder_format": func(t *testing.T, tpl string) string {
			saved(t, store.UpdateMusicNamingConfig(ctx, musicDB, artist, album, tpl, track))
			return resolved(t)(store.ResolveAlbumPath(ctx, musicDB, vaAlbumID))
		},
		"music/track_file_format": func(t *testing.T, tpl string) string {
			saved(t, store.UpdateMusicNamingConfig(ctx, musicDB, artist, album, vaSer, tpl))
			return resolved(t)(store.ResolveTrackFileName(ctx, musicDB, trackID, "download.flac"))
		},
	}
}

// TestNamingGuide_EveryListedTokenIsFilledByItsResolver keeps the guide
// honest: every token it lists for a box must actually be filled in when that
// box is used, and every token it says can be zero-padded must pad.
func TestNamingGuide_EveryListedTokenIsFilledByItsResolver(t *testing.T) {
	// The {MediaInfo ...} tokens come from reading the file; stand in for
	// FFprobe with a file that has every field, so a resolver that never asks
	// for them shows up blank.
	store.MediaInfoTokens = func(ctx context.Context, path string) map[string]string {
		return mediainfo.Info{
			Schema: mediainfo.Schema, VideoCodec: "x265", VideoBitDepth: 10, VideoDynamicRange: "HDR", VideoDynamicRangeType: "DV HDR10",
			AudioCodec: "FLAC", AudioChannels: 2, AudioBitrate: 320000, AudioBitsPerSample: 24, AudioSampleRate: 44100,
			AudioLanguages: []string{"eng", "ger"}, Subtitles: []string{"eng"},
		}.Tokens()
	}
	t.Cleanup(func() { store.MediaInfoTokens = nil })

	resolvers := namingResolvers(t)
	padded := regexp.MustCompile(`Q0\d\dQ`) // fixture numbers are single digits

	// Control: a token nothing supplies must be caught as blank, or the
	// checks below could never fail.
	if got := resolvers["movie/movie_file_format"](t, "Q{Not A Real Token}Q"); !strings.Contains(got, "QQ") {
		t.Fatalf("control: an unknown token should come out blank, got %q", got)
	}

	for _, g := range api.NamingGroups {
		for _, f := range g.Fields {
			key := g.MediaType + "/" + f.FormName
			resolve, ok := resolvers[key]
			if !ok {
				t.Errorf("%s: the guide lists this box but no resolver is wired up for it here", key)
				continue
			}
			for _, tok := range f.Tokens {
				if got := resolve(t, "Q{"+tok.Name+"}Q"); !strings.Contains(got, "Q") || strings.Contains(got, "QQ") {
					t.Errorf("%s: the guide lists {%s}, but it came out blank: %q", key, tok.Name, got)
				}
				if tok.Numeric {
					if got := resolve(t, "Q{"+tok.Name+":000}Q"); !padded.MatchString(got) {
						t.Errorf("%s: the guide says {%s:000} zero-pads, got %q", key, tok.Name, got)
					}
				}
			}
		}
	}
}

func TestSettings_NamingGuideCoversEveryBox(t *testing.T) {
	srv := newTestServer(t)
	status, body := get(t, srv, "/settings/media-management")
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d", status)
	}
	if !strings.Contains(body, `id="naming-guide"`) || !strings.Contains(body, "getElementById('naming-guide').showModal()") {
		t.Fatalf("want a naming guide dialog and a button that opens it, got:\n%s", body)
	}
	for _, g := range api.NamingGroups {
		for _, f := range g.Fields {
			if !strings.Contains(body, `name="`+f.FormName+`"`) {
				t.Errorf("%s: no input named %s", g.MediaType, f.FormName)
			}
			if n := strings.Count(body, ">"+f.Label+"<"); n < 2 {
				t.Errorf("want %q to label both its box and its guide entry, found it %d time(s)", f.Label, n)
			}
			for _, tok := range f.Tokens {
				if !strings.Contains(body, "{"+tok.Name+"}") {
					t.Errorf("%s: guide doesn't show {%s}", f.Label, tok.Name)
				}
			}
		}
	}
	// The seeded movie file template, resolved with the guide's sample values.
	if !strings.Contains(body, "The Matrix (1999).mkv") {
		t.Errorf("want the movie file example built from the saved template")
	}
}
