package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/pathbuilder"
)

// File date choices: which date an imported file's modification time is set to.
const (
	FileDateNone             = "none"
	MovieFileDateInCinemas   = "in_cinemas"
	MovieFileDatePhysical    = "physical_release"
	MovieFileDateDigital     = "digital_release"
	EpisodeFileDateAirDate   = "air_date"
	TrackFileDateReleaseDate = "release_date"
)

// Choices for settings that are stored and shown but not acted on yet.
const (
	PropersPreferAndUpgrade = "prefer_and_upgrade"
	PropersDoNotUpgrade     = "do_not_upgrade"
	PropersDoNotPrefer      = "do_not_prefer"

	RescanAlways      = "always"
	RescanAfterManual = "after_manual"
	RescanNever       = "never"
)

// MediaSettings is the singleton media_settings row (migration 00017): how
// UMMarr names, imports and manages files, shared by movies, TV and music.
type MediaSettings struct {
	ReplaceIllegalCharacters bool
	ColonReplacement         string // a pathbuilder.Colon* value
	CreateEmptyFolders       bool
	DeleteEmptyFolders       bool
	SkipFreeSpaceCheck       bool
	MinimumFreeSpaceMB       int64
	UseHardlinks             bool
	ImportExtraFiles         bool
	ExtraFileExtensions      string // comma separated, e.g. "srt,nfo"
	UnmonitorDeleted         bool
	MovieFileDate            string
	EpisodeFileDate          string
	TrackFileDate            string
	SetPermissions           bool
	ChmodFolder              string // octal, e.g. "755"
	ChownGroup               string // gid, "" to leave the group alone

	// AnalyzeVideoFiles reads movie and episode files with FFprobe (or Plex).
	AnalyzeVideoFiles bool

	// Stored and shown, not acted on yet.
	PropersRepacks        string
	ImportUsingScript     bool
	ImportScriptPath      string
	RescanAfterRefresh    string
	RecycleBinPath        string
	RecycleBinCleanupDays int64

	// Media analysis: AnalyzeVideoFiles (above) covers movies and episodes,
	// AnalyzeAudioFiles music; PlexMediaInfo reads Plex's analysis first.
	AnalyzeAudioFiles bool
	PlexMediaInfo     bool

	// PreferredReleaseCountries orders which pressing of an album to track
	// when MusicBrainz lists several - "GB,US" means a British release,
	// then an American one, then anything. Empty means no preference.
	PreferredReleaseCountries string
}

// ReleaseCountries is PreferredReleaseCountries as an upper-cased list.
func (s MediaSettings) ReleaseCountries() []string {
	var out []string
	for _, c := range strings.Split(s.PreferredReleaseCountries, ",") {
		if c = strings.ToUpper(strings.TrimSpace(c)); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// PathOptions is the part of s that path and file name resolution needs.
func (s MediaSettings) PathOptions() pathbuilder.Options {
	return pathbuilder.Options{
		ReplaceIllegal: s.ReplaceIllegalCharacters,
		Colon:          pathbuilder.ColonReplacement(s.ColonReplacement),
	}
}

// GetMediaSettings reads the media_settings row - always present, seeded by
// migration 00017.
func GetMediaSettings(ctx context.Context, q Queryer) (MediaSettings, error) {
	var s MediaSettings
	err := q.QueryRowContext(ctx, `
		SELECT replace_illegal_characters, colon_replacement, create_empty_folders, delete_empty_folders,
		       skip_free_space_check, minimum_free_space_mb, use_hardlinks, import_extra_files,
		       extra_file_extensions, unmonitor_deleted, movie_file_date, episode_file_date,
		       track_file_date, set_permissions, chmod_folder, chown_group,
		       propers_repacks, analyze_video_files, import_using_script, import_script_path,
		       rescan_after_refresh, recycle_bin_path, recycle_bin_cleanup_days, analyze_audio_files, plex_media_info,
		       preferred_release_countries
		FROM media_settings WHERE id = 1
	`).Scan(&s.ReplaceIllegalCharacters, &s.ColonReplacement, &s.CreateEmptyFolders, &s.DeleteEmptyFolders,
		&s.SkipFreeSpaceCheck, &s.MinimumFreeSpaceMB, &s.UseHardlinks, &s.ImportExtraFiles,
		&s.ExtraFileExtensions, &s.UnmonitorDeleted, &s.MovieFileDate, &s.EpisodeFileDate,
		&s.TrackFileDate, &s.SetPermissions, &s.ChmodFolder, &s.ChownGroup,
		&s.PropersRepacks, &s.AnalyzeVideoFiles, &s.ImportUsingScript, &s.ImportScriptPath,
		&s.RescanAfterRefresh, &s.RecycleBinPath, &s.RecycleBinCleanupDays, &s.AnalyzeAudioFiles, &s.PlexMediaInfo,
		&s.PreferredReleaseCountries)
	if err != nil {
		return MediaSettings{}, fmt.Errorf("get media settings: %w", err)
	}
	return s, nil
}

// UpdateMediaSettings saves every media setting at once. Takes effect
// immediately - nothing caches these.
func UpdateMediaSettings(ctx context.Context, q Queryer, s MediaSettings) error {
	_, err := q.ExecContext(ctx, `
		UPDATE media_settings SET
		       replace_illegal_characters = ?, colon_replacement = ?, create_empty_folders = ?, delete_empty_folders = ?,
		       skip_free_space_check = ?, minimum_free_space_mb = ?, use_hardlinks = ?, import_extra_files = ?,
		       extra_file_extensions = ?, unmonitor_deleted = ?, movie_file_date = ?, episode_file_date = ?,
		       track_file_date = ?, set_permissions = ?, chmod_folder = ?, chown_group = ?,
		       propers_repacks = ?, analyze_video_files = ?, import_using_script = ?, import_script_path = ?,
		       rescan_after_refresh = ?, recycle_bin_path = ?, recycle_bin_cleanup_days = ?, analyze_audio_files = ?, plex_media_info = ?,
		       preferred_release_countries = ?
		WHERE id = 1
	`, s.ReplaceIllegalCharacters, s.ColonReplacement, s.CreateEmptyFolders, s.DeleteEmptyFolders,
		s.SkipFreeSpaceCheck, s.MinimumFreeSpaceMB, s.UseHardlinks, s.ImportExtraFiles,
		s.ExtraFileExtensions, s.UnmonitorDeleted, s.MovieFileDate, s.EpisodeFileDate,
		s.TrackFileDate, s.SetPermissions, s.ChmodFolder, s.ChownGroup,
		s.PropersRepacks, s.AnalyzeVideoFiles, s.ImportUsingScript, s.ImportScriptPath,
		s.RescanAfterRefresh, s.RecycleBinPath, s.RecycleBinCleanupDays, s.AnalyzeAudioFiles, s.PlexMediaInfo,
		s.PreferredReleaseCountries)
	if err != nil {
		return fmt.Errorf("update media settings: %w", err)
	}
	return nil
}

// pathOptions loads the saved character-handling options for resolving a
// path or file name.
func pathOptions(ctx context.Context, q Queryer) (pathbuilder.Options, error) {
	s, err := GetMediaSettings(ctx, q)
	if err != nil {
		return pathbuilder.Options{}, err
	}
	return s.PathOptions(), nil
}

// resolveFileName resolves a file name template into a single cleaned path
// segment, using the saved character-handling options.
func resolveFileName(ctx context.Context, q Queryer, template string, tokens map[string]string) (string, error) {
	opts, err := pathOptions(ctx, q)
	if err != nil {
		return "", err
	}
	return pathbuilder.SanitizeSegment(pathbuilder.ResolveTemplate(template, tokens), opts), nil
}
