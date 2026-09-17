package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
)

// ReleaseTypes are Sonarr's, in its order.
var ReleaseTypes = []struct{ Value, Label string }{
	{"", "Unknown"}, {"singleEpisode", "Single Episode"}, {"multiEpisode", "Multi-Episode"}, {"seasonPack", "Season Pack"},
}

// ReleaseTypeLabel is the display name of a stored release type.
func ReleaseTypeLabel(value string) string {
	for _, t := range ReleaseTypes {
		if t.Value == value {
			return t.Label
		}
	}
	return "Unknown"
}

// EpisodeFileDetail is one row of the Manage Episodes dialog: one file,
// with every episode it covers.
type EpisodeFileDetail struct {
	ID             int64 // the first episode_files row for the path
	FileIDs        []int64
	SeasonNumber   int
	EpisodeNumbers []int
	Episodes       string // "10 - Bad Blood", or "23-24 - Hit / Run"
	RelativePath   string
	Size           int64
	Quality        string // catalog key, e.g. HDTV-1080p
	ReleaseGroup   string
	Languages      string // stored, or guessed from the name when blank
	ReleaseType    string // stored value; "" when unknown
}

// EpisodeRange is "10" or "23-24".
func (f EpisodeFileDetail) EpisodeRange() string {
	if len(f.EpisodeNumbers) == 0 {
		return ""
	}
	if len(f.EpisodeNumbers) == 1 {
		return fmt.Sprint(f.EpisodeNumbers[0])
	}
	return fmt.Sprintf("%d-%d", f.EpisodeNumbers[0], f.EpisodeNumbers[len(f.EpisodeNumbers)-1])
}

// ReleaseTypeLabel is the row's display name.
func (f EpisodeFileDetail) ReleaseTypeLabel() string { return ReleaseTypeLabel(f.ReleaseType) }

// ListEpisodeFileDetails lists seriesID's files for Manage Episodes, a
// multi-episode file once.
func ListEpisodeFileDetails(ctx context.Context, q Queryer, seriesID int64) ([]EpisodeFileDetail, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT ef.id, e.season_number, e.episode_number, COALESCE(e.title, ''), ef.relative_path, COALESCE(ef.size, 0), COALESCE(ef.quality, '{}'), ef.languages, ef.release_type, COALESCE(ef.media_info, '{}')
		FROM episode_files ef JOIN episodes e ON e.id = ef.episode_id
		WHERE e.series_id = ? ORDER BY e.season_number, e.episode_number, ef.id`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("list episode files for series %d: %w", seriesID, err)
	}
	defer rows.Close()
	var files []EpisodeFileDetail
	index := map[string]int{}
	titles := map[string][]string{}
	for rows.Next() {
		var f EpisodeFileDetail
		var number int
		var title, quality, mediaInfo string
		if err := rows.Scan(&f.ID, &f.SeasonNumber, &number, &title, &f.RelativePath, &f.Size, &quality, &f.Languages, &f.ReleaseType, &mediaInfo); err != nil {
			return nil, err
		}
		if i, ok := index[f.RelativePath]; ok {
			files[i].FileIDs = append(files[i].FileIDs, f.ID)
			files[i].EpisodeNumbers = append(files[i].EpisodeNumbers, number)
			titles[f.RelativePath] = append(titles[f.RelativePath], title)
			continue
		}
		var fq releaseparse.FileQuality
		_ = json.Unmarshal([]byte(quality), &fq)
		f.FileIDs = []int64{f.ID}
		f.EpisodeNumbers = []int{number}
		f.Quality = fq.Key()
		f.ReleaseGroup = fq.ReleaseGroup
		if mi := mediainfo.Decode(mediaInfo); f.Languages == "" && len(mi.AudioLanguages) > 0 {
			f.Languages = mediainfo.LanguageNames(mi.AudioLanguages)
		}
		if f.Languages == "" {
			f.Languages = strings.Join(releaseparse.Languages(filepath.Base(f.RelativePath)), ", ")
		}
		titles[f.RelativePath] = []string{title}
		index[f.RelativePath] = len(files)
		files = append(files, f)
	}
	for i := range files {
		f := &files[i]
		if f.ReleaseType == "" && len(f.EpisodeNumbers) == 1 {
			f.ReleaseType = "singleEpisode"
		}
		if f.ReleaseType == "" && len(f.EpisodeNumbers) > 1 {
			f.ReleaseType = "multiEpisode"
		}
		f.Episodes = f.EpisodeRange()
		if t := strings.Join(titles[f.RelativePath], " / "); strings.Trim(t, " /") != "" {
			f.Episodes += " - " + t
		}
	}
	return files, rows.Err()
}

// EpisodeFileEdit is what Manage Episodes can change on a file; nil
// fields are left alone.
type EpisodeFileEdit struct {
	Quality      *string // catalog key
	ReleaseGroup *string
	Languages    *string
	ReleaseType  *string
}

// qualityFromKey turns a catalog key back into source and resolution.
func qualityFromKey(key string, into *releaseparse.FileQuality) error {
	switch key {
	case "Unknown":
		into.Source, into.Resolution = "", ""
		return nil
	case "SDTV", "DVD":
		into.Source, into.Resolution = key, ""
		return nil
	}
	for _, k := range releaseparse.AllQualities {
		if k == key {
			parts := strings.SplitN(key, "-", 2)
			into.Source, into.Resolution = parts[0], parts[1]
			return nil
		}
	}
	return fmt.Errorf("unknown quality %q", key)
}

// UpdateEpisodeFiles applies edit to every row of the given files.
func UpdateEpisodeFiles(ctx context.Context, q Queryer, fileIDs []int64, edit EpisodeFileEdit) error {
	for _, id := range fileIDs {
		var raw string
		if err := q.QueryRowContext(ctx, `SELECT COALESCE(quality, '{}') FROM episode_files WHERE id = ?`, id).Scan(&raw); err != nil {
			return fmt.Errorf("episode file %d: %w", id, err)
		}
		var fq releaseparse.FileQuality
		_ = json.Unmarshal([]byte(raw), &fq)
		if edit.Quality != nil {
			if err := qualityFromKey(*edit.Quality, &fq); err != nil {
				return err
			}
		}
		if edit.ReleaseGroup != nil {
			fq.ReleaseGroup = strings.TrimSpace(*edit.ReleaseGroup)
		}
		data, _ := json.Marshal(fq)
		if _, err := q.ExecContext(ctx, `UPDATE episode_files SET quality = ? WHERE id = ?`, string(data), id); err != nil {
			return err
		}
		if edit.Languages != nil {
			if _, err := q.ExecContext(ctx, `UPDATE episode_files SET languages = ? WHERE id = ?`, strings.TrimSpace(*edit.Languages), id); err != nil {
				return err
			}
		}
		if edit.ReleaseType != nil {
			if _, err := q.ExecContext(ctx, `UPDATE episode_files SET release_type = ? WHERE id = ?`, *edit.ReleaseType, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// RemapEpisodeFile points one file (all its rows) at other episodes of the
// same series - Manage Episodes' Select Season / Select Episode(s). A
// target episode that already has a different file is refused.
func RemapEpisodeFile(ctx context.Context, q Queryer, seriesID int64, fileIDs []int64, seasonNumber int, episodeNumbers []int) error {
	if len(fileIDs) == 0 || len(episodeNumbers) == 0 {
		return nil
	}
	var relativePath, quality, languages, releaseType string
	var size sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT relative_path, size, COALESCE(quality, '{}'), languages, release_type FROM episode_files WHERE id = ?`, fileIDs[0]).
		Scan(&relativePath, &size, &quality, &languages, &releaseType); err != nil {
		return fmt.Errorf("episode file %d: %w", fileIDs[0], err)
	}
	sort.Ints(episodeNumbers)
	targets := make([]int64, 0, len(episodeNumbers))
	for _, n := range episodeNumbers {
		episodeID, found, err := FindEpisode(ctx, q, seriesID, seasonNumber, n)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("there is no episode S%02dE%02d", seasonNumber, n)
		}
		var existing sql.NullInt64
		var existingPath string
		_ = q.QueryRowContext(ctx, `SELECT e.episode_file_id, COALESCE(ef.relative_path, '') FROM episodes e LEFT JOIN episode_files ef ON ef.id = e.episode_file_id WHERE e.id = ?`, episodeID).Scan(&existing, &existingPath)
		if existing.Valid && existingPath != relativePath {
			return fmt.Errorf("S%02dE%02d already has a file (%s)", seasonNumber, n, filepath.Base(existingPath))
		}
		targets = append(targets, episodeID)
	}
	for _, id := range fileIDs {
		if _, err := q.ExecContext(ctx, `DELETE FROM episode_files WHERE id = ?`, id); err != nil {
			return err
		}
	}
	if len(targets) > 1 && releaseType == "" {
		releaseType = "multiEpisode"
	}
	for _, episodeID := range targets {
		fileID, err := AttachEpisodeFile(ctx, q, episodeID, relativePath, size.Int64)
		if err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `UPDATE episode_files SET quality = ?, languages = ?, release_type = ? WHERE id = ?`, quality, languages, releaseType, fileID); err != nil {
			return err
		}
	}
	return nil
}

// QualityFromKey turns a catalog key back into source and resolution,
// leaving the rest of into as it was.
func QualityFromKey(key string, into *releaseparse.FileQuality) error {
	return qualityFromKey(key, into)
}
