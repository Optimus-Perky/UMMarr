package store_test

import (
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/customformat"
	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// TestCustomFormatNames: the {Custom Formats} token lists only the formats
// set to be included when renaming.
func TestCustomFormatNames(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	if _, err := store.SaveCustomFormat(ctx, db, customformat.Format{Name: "x265", IncludeWhenRenaming: true,
		Conditions: []customformat.Condition{{Implementation: customformat.ReleaseTitle, Value: `x265|HEVC`}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveCustomFormat(ctx, db, customformat.Format{Name: "Remux", IncludeWhenRenaming: true,
		Conditions: []customformat.Condition{{Implementation: customformat.QualityModifier, Value: "REMUX"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveCustomFormat(ctx, db, customformat.Format{Name: "Not in names",
		Conditions: []customformat.Condition{{Implementation: customformat.ReleaseTitle, Value: `\bBluRay\b`}}}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"Inception.2010.1080p.BluRay.REMUX.AVC.x265-GRP": "Remux x265",
		"Inception.2010.1080p.BluRay.x264-GRP":           "",
	} {
		if got := store.CustomFormatNames(ctx, db, name); got != want {
			t.Errorf("CustomFormatNames(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestNamingUsesCustomFormatsToken: a file name template with {Custom
// Formats} gets the matching formats' names.
func TestNamingUsesCustomFormatsToken(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	rootFolderID := seedRootFolder(t, db, "movie")
	qualityProfileID := seedQualityProfile(t, db)
	metadataID, err := store.UpsertMovieMetadata(ctx, db, metadata.MovieMetadata{
		Title: metadata.Field[string]{Value: "Inception", Provider: "tmdb"}, Year: metadata.Field[int]{Value: 2010, Provider: "tmdb"},
		ExternalIDs: map[string]string{"tmdb": "27205"}})
	if err != nil {
		t.Fatal(err)
	}
	movieID, err := store.UpsertMovie(ctx, db, metadataID, qualityProfileID, rootFolderID, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveCustomFormat(ctx, db, customformat.Format{Name: "Remux Tier 01", IncludeWhenRenaming: true,
		Conditions: []customformat.Condition{{Implementation: customformat.QualityModifier, Value: "REMUX"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateMovieNamingConfig(ctx, db, "{Movie Title} ({Release Year})", "{Movie Title} ({Release Year}) [{Quality Title}] {Custom Formats}"); err != nil {
		t.Fatal(err)
	}
	name, err := store.ResolveMovieFileName(ctx, db, movieID, "Inception.2010.1080p.BluRay.REMUX.AVC-GRP.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if name != "Inception (2010) [Remux-1080p] Remux Tier 01.mkv" {
		t.Fatalf("want the format in the name, got %q", name)
	}
}
