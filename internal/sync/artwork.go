package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Importing artwork the library already has.
//
// MusicBrainz hands back no images at all, so every album and artist here
// had no cover - while the folders themselves were full of them: a
// hand-managed library keeps folder.jpg beside the tracks, often with a
// Kodi-style discart, fanart and banner too. This finds those files and
// records which one to use, rather than fetching anything.

// albumCoverNames and artistCoverNames are the names to look for, best
// first. Case is ignored: Folder.jpg and folder.jpg are both common.
var (
	albumCoverNames = []string{
		"cover", "folder", "front", "album", "albumart", "albumartsmall", "thumb", "poster",
	}
	artistCoverNames = []string{
		"poster", "folder", "artist", "thumb", "banner", "fanart",
	}
	coverExtensions = []string{".jpg", ".jpeg", ".png", ".webp"}
)

// findCover picks the best artwork file in dir, or "" when there is none.
// The name decides first (cover before folder before front), the extension
// second, so the directory order never does.
func findCover(dir string, names []string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best, bestScore := "", len(names)*10+10
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		extRank := indexOf(coverExtensions, ext)
		if extRank < 0 {
			continue
		}
		nameRank := indexOf(names, strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name))))
		if nameRank < 0 {
			continue
		}
		if score := nameRank*10 + extRank; score < bestScore {
			best, bestScore = name, score
		}
	}
	return best
}

func indexOf(list []string, want string) int {
	for i, v := range list {
		if v == want {
			return i
		}
	}
	return -1
}

// ArtworkReport is what an artwork import found.
type ArtworkReport struct {
	Albums  int
	Artists int
	Missing int // albums with a folder but no artwork in it
}

// Summary is the report in one line.
func (r ArtworkReport) Summary() string {
	return fmt.Sprintf("%d album cover(s) and %d artist image(s) imported; %d album(s) have none in their folder.",
		r.Albums, r.Artists, r.Missing)
}

// ImportArtwork records the cover art already sitting in the library's
// folders. Safe to run again: it only writes when the file it finds is
// different from what is recorded, and never deletes a recorded cover
// because a folder was briefly unreadable.
func (s *ImportService) ImportArtwork(ctx context.Context) (ArtworkReport, error) {
	var report ArtworkReport
	artists, err := store.ListArtistLibrary(ctx, s.DB)
	if err != nil {
		return report, err
	}
	for _, artist := range artists {
		if artist.Path.String != "" {
			if cover := findCover(artist.Path.String, artistCoverNames); cover != "" {
				changed, err := store.SetArtistCover(ctx, s.DB, artist.ID, cover)
				if err != nil {
					return report, err
				}
				if changed {
					report.Artists++
				}
			}
		}
		albums, err := store.ListAlbumsForArtist(ctx, s.DB, artist.ArtistMetadataID)
		if err != nil {
			return report, err
		}
		for _, album := range albums {
			folder, err := store.AlbumFolderPath(ctx, s.DB, album.ID)
			if err != nil || folder == "" {
				continue
			}
			cover := findCover(folder, albumCoverNames)
			if cover == "" {
				if _, err := os.Stat(folder); err == nil {
					report.Missing++
				}
				continue
			}
			changed, err := store.SetAlbumCover(ctx, s.DB, album.ID, cover)
			if err != nil {
				return report, err
			}
			if changed {
				report.Albums++
			}
		}
	}
	return report, nil
}

// BackfillAudioQuality fills in the quality of music files that were
// analyzed before UMMarr read audio formats: the answer is already in
// their stored media info, so this needs no disk access and no re-read.
func (s *ImportService) BackfillAudioQuality(ctx context.Context) (int, error) {
	files, err := store.TrackFilesNeedingQuality(ctx, s.DB)
	if err != nil {
		return 0, err
	}
	filled := 0
	for _, f := range files {
		audio := AudioQualityFromInfo(f.Info)
		if audio.Empty() || audio.Key() == "Unknown" {
			continue
		}
		if err := store.UpdateTrackFileQuality(ctx, s.DB, f.ID, releaseparse.FileQuality{Audio: audio}); err != nil {
			return filled, err
		}
		filled++
	}
	return filled, nil
}
