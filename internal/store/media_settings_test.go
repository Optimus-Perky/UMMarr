package store_test

import (
	"context"
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// TestGetMediaSettings_DefaultsKeepTodaysBehaviour pins the migration's
// defaults: every setting that changes how files are handled starts off, so
// deploying the settings changes nothing until they're turned on.
func TestGetMediaSettings_DefaultsKeepTodaysBehaviour(t *testing.T) {
	db := openTestDB(t)
	got, err := store.GetMediaSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("get media settings: %v", err)
	}
	want := store.MediaSettings{
		ColonReplacement:      "delete",
		MinimumFreeSpaceMB:    100,
		ExtraFileExtensions:   "srt",
		MovieFileDate:         store.FileDateNone,
		EpisodeFileDate:       store.FileDateNone,
		TrackFileDate:         store.FileDateNone,
		ChmodFolder:           "755",
		PropersRepacks:        store.PropersDoNotPrefer,
		AnalyzeVideoFiles:     true,
		AnalyzeAudioFiles:     true,
		RescanAfterRefresh:    store.RescanAlways,
		RecycleBinCleanupDays: 7,
		// Not a behaviour switch: it only orders which pressing of an album
		// is chosen when MusicBrainz lists several, and something has to be
		// preferred. See migration 00046.
		PreferredReleaseCountries: "GB,US",
	}
	if got != want {
		t.Fatalf("defaults:\n got %+v\nwant %+v", got, want)
	}
}

func TestUpdateMediaSettings_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	want := store.MediaSettings{
		ReplaceIllegalCharacters: true,
		ColonReplacement:         "smart",
		CreateEmptyFolders:       true,
		DeleteEmptyFolders:       true,
		SkipFreeSpaceCheck:       true,
		MinimumFreeSpaceMB:       2048,
		UseHardlinks:             true,
		ImportExtraFiles:         true,
		ExtraFileExtensions:      "srt,nfo",
		UnmonitorDeleted:         true,
		MovieFileDate:            store.MovieFileDatePhysical,
		EpisodeFileDate:          store.EpisodeFileDateAirDate,
		TrackFileDate:            store.TrackFileDateReleaseDate,
		SetPermissions:           true,
		ChmodFolder:              "775",
		ChownGroup:               "100512",
		PropersRepacks:           store.PropersPreferAndUpgrade,
		AnalyzeVideoFiles:        false,
		ImportUsingScript:        true,
		ImportScriptPath:         "/config/import.sh",
		RescanAfterRefresh:       store.RescanNever,
		RecycleBinPath:           "/data/.recycle",
		RecycleBinCleanupDays:    30,
	}
	if err := store.UpdateMediaSettings(ctx, db, want); err != nil {
		t.Fatalf("update media settings: %v", err)
	}
	got, err := store.GetMediaSettings(ctx, db)
	if err != nil {
		t.Fatalf("get media settings: %v", err)
	}
	if got != want {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, want)
	}
}

func TestGetNamingConfig_RenameFilesDefaultsOn(t *testing.T) {
	db := openTestDB(t)
	for _, mediaType := range []string{"movie", "series", "music"} {
		c, err := store.GetNamingConfig(context.Background(), db, mediaType)
		if err != nil {
			t.Fatalf("get naming config %s: %v", mediaType, err)
		}
		if !c.RenameFiles {
			t.Errorf("%s: want renaming on by default", mediaType)
		}
	}
}
