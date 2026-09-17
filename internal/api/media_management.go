package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/pathbuilder"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

type option struct{ Value, Label string }

var (
	colonOptions = []option{
		{string(pathbuilder.ColonDelete), "Delete"},
		{string(pathbuilder.ColonDash), "Replace with Dash"},
		{string(pathbuilder.ColonSpaceDash), "Replace with Space Dash"},
		{string(pathbuilder.ColonSpaceDashSpace), "Replace with Space Dash Space"},
		{string(pathbuilder.ColonSmart), "Smart Replace"},
	}
	movieFileDateOptions = []option{
		{store.FileDateNone, "None"},
		{store.MovieFileDateInCinemas, "In Cinemas Date"},
		{store.MovieFileDatePhysical, "Physical Release Date"},
		{store.MovieFileDateDigital, "Digital Release Date"},
	}
	episodeFileDateOptions = []option{{store.FileDateNone, "None"}, {store.EpisodeFileDateAirDate, "Air Date"}}
	trackFileDateOptions   = []option{{store.FileDateNone, "None"}, {store.TrackFileDateReleaseDate, "Album Release Date"}}
	propersOptions         = []option{
		{store.PropersPreferAndUpgrade, "Prefer and Upgrade"},
		{store.PropersDoNotUpgrade, "Do Not Upgrade Automatically"},
		{store.PropersDoNotPrefer, "Do Not Prefer"},
	}
	rescanOptions = []option{
		{store.RescanAlways, "Always"},
		{store.RescanAfterManual, "After Manual Refresh"},
		{store.RescanNever, "Never"},
	}
)

func (settingsPageData) ColonOptions() []option           { return colonOptions }
func (settingsPageData) MovieFileDateOptions() []option   { return movieFileDateOptions }
func (settingsPageData) EpisodeFileDateOptions() []option { return episodeFileDateOptions }
func (settingsPageData) TrackFileDateOptions() []option   { return trackFileDateOptions }
func (settingsPageData) PropersOptions() []option         { return propersOptions }
func (settingsPageData) RescanOptions() []option          { return rescanOptions }

// PermissionModes shows what the chmod folder value gives new folders and
// files, like Radarr's breakdown under the field.
func (d settingsPageData) PermissionModes() string {
	mode, err := importer.ParseFolderMode(d.Media.ChmodFolder)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("Folders %s, files %s", mode|os.ModeDir, mode.Perm()&^0o111)
}

// mediaManagementForm is the Media management form as submitted, with a
// message for each field that can't be saved.
type mediaManagementForm struct {
	Media  store.MediaSettings
	Naming map[string]store.NamingConfig
	Errors map[string]string
}

func (f *mediaManagementForm) choice(r *http.Request, name string, options []option) string {
	v := r.FormValue(name)
	for _, o := range options {
		if o.Value == v {
			return v
		}
	}
	f.Errors[name] = "Pick one of the listed options."
	return v
}

func (f *mediaManagementForm) wholeNumber(r *http.Request, name string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(r.FormValue(name)), 10, 64)
	if err != nil || n < 0 {
		f.Errors[name] = "Enter a whole number, 0 or more."
		return 0
	}
	return n
}

// groupProblem accepts an empty group (leave groups alone) or the numeric ID of
// a group UMMarr's own process belongs to: a process that isn't root can only
// give files to its own groups, so anything else would fail on every import.
func groupProblem(v string) string {
	if v == "" {
		return ""
	}
	gid, err := strconv.Atoi(v)
	if err != nil || gid < 0 {
		return "Enter a group ID (a number), not a group name."
	}
	if os.Geteuid() == 0 || gid == os.Getgid() {
		return ""
	}
	groups, _ := os.Getgroups()
	for _, g := range groups {
		if g == gid {
			return ""
		}
	}
	own := []string{strconv.Itoa(os.Getgid())}
	for _, g := range groups {
		if g != os.Getgid() {
			own = append(own, strconv.Itoa(g))
		}
	}
	return fmt.Sprintf("UMMarr isn't in group %d, so it can't give files to it. Its groups are %s.", gid, strings.Join(own, ", "))
}

func parseMediaManagement(r *http.Request) mediaManagementForm {
	f := mediaManagementForm{Naming: map[string]store.NamingConfig{}, Errors: map[string]string{}}
	checked := func(name string) bool { return r.FormValue(name) == "on" }
	text := func(name string) string { return strings.TrimSpace(r.FormValue(name)) }

	for _, g := range namingGroups {
		c := store.NamingConfig{RenameFiles: checked(g.RenameName)}
		for _, field := range g.Fields {
			v := text(field.FormName)
			if v == "" {
				f.Errors[field.FormName] = "Can't be empty."
			}
			field.set(&c, v)
		}
		f.Naming[g.MediaType] = c
	}

	m := &f.Media
	m.ReplaceIllegalCharacters = checked("replace_illegal_characters")
	m.ColonReplacement = f.choice(r, "colon_replacement", colonOptions)
	m.CreateEmptyFolders = checked("create_empty_folders")
	m.DeleteEmptyFolders = checked("delete_empty_folders")
	m.SkipFreeSpaceCheck = checked("skip_free_space_check")
	m.MinimumFreeSpaceMB = f.wholeNumber(r, "minimum_free_space_mb")
	m.UseHardlinks = checked("use_hardlinks")
	m.ImportExtraFiles = checked("import_extra_files")
	m.ExtraFileExtensions = text("extra_file_extensions")
	m.UnmonitorDeleted = checked("unmonitor_deleted")
	m.MovieFileDate = f.choice(r, "movie_file_date", movieFileDateOptions)
	m.EpisodeFileDate = f.choice(r, "episode_file_date", episodeFileDateOptions)
	m.TrackFileDate = f.choice(r, "track_file_date", trackFileDateOptions)
	m.SetPermissions = checked("set_permissions")
	m.ChmodFolder = text("chmod_folder")
	if _, err := importer.ParseFolderMode(m.ChmodFolder); err != nil {
		f.Errors["chmod_folder"] = err.Error() + "."
	}
	m.ChownGroup = text("chown_group")
	if problem := groupProblem(m.ChownGroup); problem != "" {
		f.Errors["chown_group"] = problem
	}
	m.PropersRepacks = f.choice(r, "propers_repacks", propersOptions)
	m.AnalyzeVideoFiles = checked("analyze_video_files")
	m.AnalyzeAudioFiles = checked("analyze_audio_files")
	m.PlexMediaInfo = checked("plex_media_info")
	m.ImportUsingScript = checked("import_using_script")
	m.ImportScriptPath = text("import_script_path")
	m.RescanAfterRefresh = f.choice(r, "rescan_after_refresh", rescanOptions)
	m.RecycleBinPath = text("recycle_bin_path")
	if m.RecycleBinPath != "" && !path.IsAbs(m.RecycleBinPath) {
		f.Errors["recycle_bin_path"] = "Use a full path starting with /, or leave it empty."
	}
	m.RecycleBinCleanupDays = f.wholeNumber(r, "recycle_bin_cleanup_days")
	return f
}

// UpdateMediaManagement saves the whole Media management form at once, like
// Radarr's Save Changes. If any field is invalid nothing is saved, and the page
// comes back with what was submitted and each problem beside its field.
func (h *handler) UpdateMediaManagement(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	form := parseMediaManagement(r)

	if len(form.Errors) > 0 {
		data, err := h.loadSettingsPage(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data.Media = form.Media
		data.MovieNaming, data.SeriesNaming, data.MusicNaming = form.Naming["movie"], form.Naming["series"], form.Naming["music"]
		data.Errors = form.Errors
		data.Tab, data.Tabs = "media-management", settingsTabs
		h.setMediaAnalysisFlags(ctx, &data)
		data.UnmatchedCount, _ = store.CountUnmatchedFolders(ctx, h.deps.DB)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		h.renderPage(w, "settings", data)
		return
	}

	err := store.WithTx(ctx, h.deps.DB, func(tx *sql.Tx) error {
		if err := store.UpdateMediaSettings(ctx, tx, form.Media); err != nil {
			return err
		}
		movie, series, music := form.Naming["movie"], form.Naming["series"], form.Naming["music"]
		if err := store.UpdateMovieNamingConfig(ctx, tx, movie.MovieFolderFormat.String, movie.MovieFileFormat.String); err != nil {
			return err
		}
		if err := store.UpdateSeriesNamingConfig(ctx, tx, series.SeriesFolderFormat.String, series.SeasonFolderFormat.String, series.EpisodeFileFormat.String); err != nil {
			return err
		}
		if err := store.UpdateMusicNamingConfig(ctx, tx, music.ArtistFolderFormat.String, music.AlbumFolderFormat.String, music.VASeriesFolderFormat.String, music.TrackFileFormat.String); err != nil {
			return err
		}
		for mediaType, c := range form.Naming {
			if err := store.UpdateRenameFiles(ctx, tx, mediaType, c.RenameFiles); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.deps.MediaInfo.Kick()
	http.Redirect(w, r, "/settings/media-management?saved=1#media-management", http.StatusSeeOther)
}
