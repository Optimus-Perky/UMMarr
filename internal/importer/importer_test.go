package importer_test

import (
	"testing"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
)

func TestIsVideoFile(t *testing.T) {
	for _, path := range []string{"movie.mkv", "movie.MKV", "movie.mp4"} {
		if !importer.IsVideoFile(path) {
			t.Errorf("IsVideoFile(%q) = false, want true", path)
		}
	}
	for _, path := range []string{"readme.txt", "movie.nfo", "subs.srt"} {
		if importer.IsVideoFile(path) {
			t.Errorf("IsVideoFile(%q) = true, want false", path)
		}
	}
}

func TestIsAudioFile(t *testing.T) {
	for _, path := range []string{"track.flac", "track.MP3"} {
		if !importer.IsAudioFile(path) {
			t.Errorf("IsAudioFile(%q) = false, want true", path)
		}
	}
	for _, path := range []string{"cover.jpg", "album.nfo"} {
		if importer.IsAudioFile(path) {
			t.Errorf("IsAudioFile(%q) = true, want false", path)
		}
	}
}

func TestLargestVideoFile(t *testing.T) {
	files := []importer.File{
		{Path: "sample.mkv", Size: 1000},
		{Path: "movie.mkv", Size: 5_000_000},
		{Path: "readme.nfo", Size: 10_000_000}, // bigger but not a video extension
	}
	best, ok := importer.LargestVideoFile(files)
	if !ok || best.Path != "movie.mkv" {
		t.Fatalf("want movie.mkv, got %+v ok=%v", best, ok)
	}

	if _, ok := importer.LargestVideoFile([]importer.File{{Path: "readme.nfo", Size: 100}}); ok {
		t.Fatalf("want ok=false when no video files present")
	}
}

func TestAudioFilesSorted(t *testing.T) {
	files := []importer.File{
		{Path: "03 - Track.flac"},
		{Path: "cover.jpg"},
		{Path: "01 - Track.flac"},
		{Path: "02 - Track.flac"},
	}
	got := importer.AudioFilesSorted(files)
	want := []string{"01 - Track.flac", "02 - Track.flac", "03 - Track.flac"}
	if len(got) != len(want) {
		t.Fatalf("want %d audio files, got %d: %+v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i].Path != w {
			t.Errorf("want got[%d]=%q, got %q", i, w, got[i].Path)
		}
	}
}

func TestHasArchiveFile(t *testing.T) {
	if !importer.HasArchiveFile([]importer.File{{Path: "release.zip"}}) {
		t.Errorf("want true for a .zip file")
	}
	if importer.HasArchiveFile([]importer.File{{Path: "movie.mkv"}}) {
		t.Errorf("want false when no archive present")
	}
}
