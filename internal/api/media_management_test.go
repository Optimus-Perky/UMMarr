package api_test

import (
	"database/sql"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// currentMediaManagementForm is the Media management form exactly as the page
// would submit it with nothing changed.
func currentMediaManagementForm(t *testing.T, db *sql.DB) url.Values {
	t.Helper()
	ctx := t.Context()
	ms, err := store.GetMediaSettings(ctx, db)
	if err != nil {
		t.Fatalf("get media settings: %v", err)
	}
	v := url.Values{}
	on := func(name string, b bool) {
		if b {
			v.Set(name, "on")
		}
	}
	on("replace_illegal_characters", ms.ReplaceIllegalCharacters)
	v.Set("colon_replacement", ms.ColonReplacement)
	on("create_empty_folders", ms.CreateEmptyFolders)
	on("delete_empty_folders", ms.DeleteEmptyFolders)
	on("skip_free_space_check", ms.SkipFreeSpaceCheck)
	v.Set("minimum_free_space_mb", strconv.FormatInt(ms.MinimumFreeSpaceMB, 10))
	on("use_hardlinks", ms.UseHardlinks)
	on("import_extra_files", ms.ImportExtraFiles)
	v.Set("extra_file_extensions", ms.ExtraFileExtensions)
	on("unmonitor_deleted", ms.UnmonitorDeleted)
	v.Set("movie_file_date", ms.MovieFileDate)
	v.Set("episode_file_date", ms.EpisodeFileDate)
	v.Set("track_file_date", ms.TrackFileDate)
	on("set_permissions", ms.SetPermissions)
	v.Set("chmod_folder", ms.ChmodFolder)
	v.Set("chown_group", ms.ChownGroup)
	v.Set("propers_repacks", ms.PropersRepacks)
	on("analyze_video_files", ms.AnalyzeVideoFiles)
	on("analyze_audio_files", ms.AnalyzeAudioFiles)
	on("plex_media_info", ms.PlexMediaInfo)
	on("import_using_script", ms.ImportUsingScript)
	v.Set("import_script_path", ms.ImportScriptPath)
	v.Set("rescan_after_refresh", ms.RescanAfterRefresh)
	v.Set("recycle_bin_path", ms.RecycleBinPath)
	v.Set("recycle_bin_cleanup_days", strconv.FormatInt(ms.RecycleBinCleanupDays, 10))
	for _, g := range api.NamingGroups {
		c, err := store.GetNamingConfig(ctx, db, g.MediaType)
		if err != nil {
			t.Fatalf("get naming config: %v", err)
		}
		on(g.RenameName, c.RenameFiles)
		for _, f := range g.Fields {
			v.Set(f.FormName, api.NamingFieldValue(f, c))
		}
	}
	return v
}

func TestSettings_MediaManagementShowsEverySection(t *testing.T) {
	srv := newTestServer(t)
	status, body := get(t, srv, "/settings/media-management")
	if status != http.StatusOK {
		t.Fatalf("want 200, got %d", status)
	}
	for _, heading := range []string{"<h3>Naming</h3>", "<h3>Folders</h3>", "<h3>Importing</h3>", "<h3>File Management</h3>", "<h3>Permissions</h3>", "<h2>Library</h2>"} {
		if !strings.Contains(body, heading) {
			t.Errorf("want section %s", heading)
		}
	}
	for _, name := range []string{
		"replace_illegal_characters", "colon_replacement", "rename_movie", "rename_series", "rename_music",
		"create_empty_folders", "delete_empty_folders", "skip_free_space_check", "minimum_free_space_mb",
		"use_hardlinks", "import_using_script", "import_script_path", "import_extra_files", "extra_file_extensions",
		"unmonitor_deleted", "propers_repacks", "analyze_video_files", "analyze_audio_files", "plex_media_info", "rescan_after_refresh",
		"movie_file_date", "episode_file_date", "track_file_date", "recycle_bin_path", "recycle_bin_cleanup_days",
		"set_permissions", "chmod_folder", "chown_group",
	} {
		if !strings.Contains(body, `name="`+name+`"`) {
			t.Errorf("want a %s field", name)
		}
	}
	if n := strings.Count(body, "Not active yet:"); n != 5 {
		t.Errorf("want the five behaviour-later settings marked not active yet (Analyze Video Files works now), found %d", n)
	}
	if !strings.Contains(body, `id="toggle-advanced"`) || !strings.Contains(body, `class="setting-row advanced"`) {
		t.Errorf("want advanced rows and a toggle for them")
	}
	if !strings.Contains(body, "Folders drwxr-xr-x, files -rw-r--r--") {
		t.Errorf("want the default 755 explained as folder and file modes")
	}
}

func TestUpdateMediaManagement_SavesEverything(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()

	v := currentMediaManagementForm(t, db)
	for name, value := range map[string]string{
		"replace_illegal_characters": "on", "colon_replacement": "smart",
		"create_empty_folders": "on", "delete_empty_folders": "on", "skip_free_space_check": "on",
		"minimum_free_space_mb": "2048", "use_hardlinks": "on", "import_extra_files": "on",
		"extra_file_extensions": "srt, sub", "unmonitor_deleted": "on",
		"movie_file_date": store.MovieFileDatePhysical, "episode_file_date": store.EpisodeFileDateAirDate,
		"track_file_date": store.TrackFileDateReleaseDate, "set_permissions": "on", "chmod_folder": "775",
		"chown_group": strconv.Itoa(os.Getgid()), "propers_repacks": store.PropersPreferAndUpgrade,
		"import_using_script": "on", "import_script_path": "/config/import", "rescan_after_refresh": store.RescanNever,
		"recycle_bin_path": "/data/.recycle", "recycle_bin_cleanup_days": "30", "plex_media_info": "on",
		"movie_file_format": "{Movie Title} ({Release Year}) {Quality Title}",
	} {
		v.Set(name, value)
	}
	v.Del("analyze_video_files")
	v.Del("rename_series")

	resp, err := http.PostForm(srv.URL+"/settings/media-management", v)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Query().Get("saved") != "1" || !strings.Contains(body, "Saved.") {
		t.Fatalf("want a redirect back to Settings showing Saved., got %d at %s", resp.StatusCode, resp.Request.URL)
	}

	got, err := store.GetMediaSettings(ctx, db)
	if err != nil {
		t.Fatalf("get media settings: %v", err)
	}
	want := store.MediaSettings{
		ReplaceIllegalCharacters: true, ColonReplacement: "smart", CreateEmptyFolders: true, DeleteEmptyFolders: true,
		SkipFreeSpaceCheck: true, MinimumFreeSpaceMB: 2048, UseHardlinks: true, ImportExtraFiles: true,
		ExtraFileExtensions: "srt, sub", UnmonitorDeleted: true, MovieFileDate: store.MovieFileDatePhysical,
		EpisodeFileDate: store.EpisodeFileDateAirDate, TrackFileDate: store.TrackFileDateReleaseDate,
		SetPermissions: true, ChmodFolder: "775", ChownGroup: strconv.Itoa(os.Getgid()),
		PropersRepacks: store.PropersPreferAndUpgrade, AnalyzeVideoFiles: false, ImportUsingScript: true,
		ImportScriptPath: "/config/import", RescanAfterRefresh: store.RescanNever,
		RecycleBinPath: "/data/.recycle", RecycleBinCleanupDays: 30, AnalyzeAudioFiles: true, PlexMediaInfo: true,
	}
	if got != want {
		t.Fatalf("saved settings:\n got %+v\nwant %+v", got, want)
	}
	movie, _ := store.GetNamingConfig(ctx, db, "movie")
	series, _ := store.GetNamingConfig(ctx, db, "series")
	if movie.MovieFileFormat.String != "{Movie Title} ({Release Year}) {Quality Title}" || !movie.RenameFiles || series.RenameFiles {
		t.Fatalf("want the movie file template saved, movies still renamed and episodes not; got %q rename movie=%v series=%v",
			movie.MovieFileFormat.String, movie.RenameFiles, series.RenameFiles)
	}
}

func TestUpdateMediaManagement_RejectsInvalidInputAndSavesNothing(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()
	before, err := store.GetMediaSettings(ctx, db)
	if err != nil {
		t.Fatalf("get media settings: %v", err)
	}

	foreignGroup := 424242
	if groups, _ := os.Getgroups(); true {
		for taken := true; taken; {
			taken = foreignGroup == os.Getgid()
			for _, g := range groups {
				taken = taken || g == foreignGroup
			}
			if taken {
				foreignGroup++
			}
		}
	}

	v := currentMediaManagementForm(t, db)
	v.Set("use_hardlinks", "on")
	v.Set("chmod_folder", "644")
	v.Set("chown_group", strconv.Itoa(foreignGroup))
	v.Set("colon_replacement", "sideways")
	v.Set("movie_folder_format", "")
	v.Set("minimum_free_space_mb", "-5")
	v.Set("recycle_bin_path", "relative/bin")

	resp, err := http.PostForm(srv.URL+"/settings/media-management", v)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", resp.StatusCode)
	}
	messages := []string{"Nothing was saved", "owner rwx", "Pick one of the listed options", "Can&#39;t be empty", "whole number", "full path"}
	if os.Geteuid() != 0 {
		messages = append(messages, "isn&#39;t in group")
	}
	for _, msg := range messages {
		if !strings.Contains(body, msg) {
			t.Errorf("want the page to say %q", msg)
		}
	}
	if !strings.Contains(body, `value="644"`) || !strings.Contains(body, "data-show-advanced") {
		t.Errorf("want the submitted values kept and advanced rows shown so the errors are visible")
	}

	after, err := store.GetMediaSettings(ctx, db)
	if err != nil {
		t.Fatalf("get media settings: %v", err)
	}
	if after != before {
		t.Fatalf("want nothing saved, got %+v", after)
	}
	if movie, _ := store.GetNamingConfig(ctx, db, "movie"); movie.MovieFolderFormat.String == "" {
		t.Fatalf("want the movie folder template left as it was")
	}
}

func TestSettings_NamingGuideDescribesTheCharacterSetting(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	// Compare what a browser shows: html/template escapes characters such as
	// + and " in page text.
	rule := func() string {
		_, body := get(t, srv, "/settings/media-management")
		text := html.UnescapeString(body)
		if i := strings.Index(text, "Characters Windows"); i >= 0 {
			return text[i:]
		}
		if i := strings.Index(text, "These characters are removed"); i >= 0 {
			return text[i:]
		}
		return ""
	}

	if got := rule(); !strings.HasPrefix(got, "These characters are removed because Windows") {
		t.Errorf("want the default rule to say characters are removed, got %.120q", got)
	}

	ms, err := store.GetMediaSettings(t.Context(), db)
	if err != nil {
		t.Fatalf("get media settings: %v", err)
	}
	ms.ReplaceIllegalCharacters, ms.ColonReplacement = true, "dash"
	if err := store.UpdateMediaSettings(t.Context(), db, ms); err != nil {
		t.Fatalf("update media settings: %v", err)
	}
	if got := rule(); !strings.Contains(got, `\ and / become +`) || !strings.Contains(got, `a colon becomes "-"`) {
		t.Errorf("want the rule to describe replacing, including the dash colon mode, got %.200q", got)
	}
}

func TestSettings_RootFoldersShowFreeSpaceAndUnmappedFolders(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	root := t.TempDir()
	movieID := seedTestMovieWithRootFolder(t, db, root)
	var moviePath string
	if err := db.QueryRow(`SELECT path FROM movies WHERE id = ?`, movieID).Scan(&moviePath); err != nil {
		t.Fatalf("read movie path: %v", err)
	}
	for _, dir := range []string{moviePath, filepath.Join(root, "Unknown Movie (1999)")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	_, body := get(t, srv, "/settings/media-management")
	if !strings.Contains(body, `<td class="unmapped">1</td>`) {
		t.Errorf("want one unmapped folder (the movie's own folder is mapped)")
	}
	if !regexp.MustCompile(`<td class="free-space">\d+(\.\d)? (B|[KMGTPE]iB)</td>`).MatchString(body) {
		t.Errorf("want the root folder's free space shown")
	}
}

func TestSettings_EverySectionCanCollapse(t *testing.T) {
	srv := newTestServer(t)
	sections := map[string][]string{
		"general":          {"account", "general", "host"},
		"indexers":         {"indexers", "indexer-options"},
		"download-clients": {"download-clients", "failed-download-handling"},
		"custom-formats":   {"custom-formats"},
		"connect":          {"connect"},
		"import-lists":     {"import-lists"},
		"metadata":         {"metadata-sources", "metadata"},
		"media-management": {"media-management", "media-naming", "media-folders", "media-importing", "media-file-management", "media-permissions", "library-scan", "library"},
		"profiles":         {"quality-profiles", "preferred-words"},
	}
	total := 0
	for tab, want := range sections {
		_, body := get(t, srv, "/settings/"+tab)
		for _, section := range want {
			if !strings.Contains(body, `data-section="`+section+`"`) {
				t.Errorf("want the %s section on the %s tab", section, tab)
			}
		}
		if n := strings.Count(body, "data-section="); n != len(want) {
			t.Errorf("want %d collapsible sections on the %s tab, got %d", len(want), tab, n)
		}
		total += len(want)
		if !strings.Contains(body, "ummarr-collapsed-") || !strings.Contains(body, "section-toggle") {
			t.Errorf("want the script that turns section headings into collapse buttons on the %s tab", tab)
		}
	}
	if total != 22 {
		t.Errorf("want all 22 sections spread over the tabs, got %d", total)
	}
}

func TestSettings_Tabs(t *testing.T) {
	srv := newTestServer(t)
	status, body := get(t, srv, "/settings")
	if status != http.StatusOK || !strings.Contains(body, `class="dialog-tab active" href="/settings/media-management">Media Management</a>`) || !strings.Contains(body, `href="/settings/general">General</a>`) {
		t.Fatalf("want /settings to open Media Management with the tab bar, got %d:\n%s", status, body)
	}
	if strings.Contains(body, `data-section="indexers"`) || !strings.Contains(body, `data-section="library"`) {
		t.Fatal("want only the Media Management panels on the default tab")
	}
	if status, _ := get(t, srv, "/settings/nope"); status != http.StatusNotFound {
		t.Fatalf("want an unknown tab to 404, got %d", status)
	}
}

// Preferred release countries decide which pressing of an album gets
// tracked, so the setting has to round-trip like the rest.
func TestMediaManagement_PreferredReleaseCountries(t *testing.T) {
	db := openTestDB(t)
	srv := newTestServerWithDB(t, db)
	ctx := t.Context()

	before, err := store.GetMediaSettings(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if before.PreferredReleaseCountries == "" {
		t.Error("want a default country preference rather than none")
	}

	_, body := get(t, srv, "/settings/media-management")
	if !strings.Contains(body, `name="preferred_release_countries"`) {
		t.Fatalf("want the setting on the page, got:\n%s", body)
	}

	form := currentMediaManagementForm(t, db)
	form.Set("preferred_release_countries", "gb, ie ,us")
	if resp, body := postForm(t, srv, "/settings/media-management", form); resp.StatusCode != 200 {
		t.Fatalf("save = %d: %s", resp.StatusCode, body)
	}
	after, err := store.GetMediaSettings(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if after.PreferredReleaseCountries != "GB, IE ,US" {
		t.Errorf("stored %q", after.PreferredReleaseCountries)
	}
	got := after.ReleaseCountries()
	want := []string{"GB", "IE", "US"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("ReleaseCountries() = %v, want %v", got, want)
		}
	}
}
