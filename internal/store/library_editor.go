package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/titleutil"
)

// LibraryEdit is what Radarr's and Sonarr's mass editor changes on every
// selected movie or series. Nil fields and an empty TagMode leave things
// as they are.
type LibraryEdit struct {
	Monitored           *bool
	QualityProfileID    *int64
	MinimumAvailability *string // movies only
	SeriesType          *string // series only
	SeasonFolder        *bool   // series only
	TagMode             string  // add, remove or replace
	Tags                []string
}

// MinimumAvailabilities are Radarr's, in its order.
var MinimumAvailabilities = []struct{ Value, Label string }{
	{"announced", "Announced"}, {"inCinemas", "In Cinemas"}, {"released", "Released"},
}

// EditMovies applies e to every movie in ids.
func EditMovies(ctx context.Context, q Queryer, ids []int64, e LibraryEdit) error {
	return editLibrary(ctx, q, "movies", ids, e)
}

// EditSeries applies e to every series in ids.
func EditSeries(ctx context.Context, q Queryer, ids []int64, e LibraryEdit) error {
	return editLibrary(ctx, q, "series", ids, e)
}

func editLibrary(ctx context.Context, q Queryer, table string, ids []int64, e LibraryEdit) error {
	var sets []string
	var args []any
	set := func(column string, value any) {
		sets = append(sets, column+" = ?")
		args = append(args, value)
	}
	if e.Monitored != nil {
		set("monitored", *e.Monitored)
	}
	if e.QualityProfileID != nil {
		set("quality_profile_id", *e.QualityProfileID)
	}
	if table == "movies" && e.MinimumAvailability != nil {
		known := false
		for _, a := range MinimumAvailabilities {
			known = known || a.Value == *e.MinimumAvailability
		}
		if !known {
			return fmt.Errorf("unknown minimum availability %q", *e.MinimumAvailability)
		}
		set("minimum_availability", *e.MinimumAvailability)
	}
	if table == "series" && e.SeriesType != nil {
		known := false
		for _, t := range SeriesTypes {
			known = known || t == *e.SeriesType
		}
		if !known {
			return fmt.Errorf("unknown series type %q", *e.SeriesType)
		}
		set("series_type", *e.SeriesType)
	}
	if table == "series" && e.SeasonFolder != nil {
		set("season_folder", *e.SeasonFolder)
	}
	switch e.TagMode {
	case "", "add", "remove", "replace":
	default:
		return fmt.Errorf("unknown tag mode %q", e.TagMode)
	}
	for _, id := range ids {
		if len(sets) > 0 {
			rowArgs := append(append([]any{}, args...), id)
			if _, err := q.ExecContext(ctx, `UPDATE `+table+` SET `+strings.Join(sets, ", ")+` WHERE id = ?`, rowArgs...); err != nil {
				return fmt.Errorf("edit %s %d: %w", table, id, err)
			}
		}
		if e.TagMode != "" {
			if err := editTags(ctx, q, table, id, e.TagMode, e.Tags); err != nil {
				return err
			}
		}
	}
	return nil
}

func editTags(ctx context.Context, q Queryer, table string, id int64, mode string, labels []string) error {
	var stored string
	if err := q.QueryRowContext(ctx, `SELECT tags FROM `+table+` WHERE id = ?`, id).Scan(&stored); err != nil {
		return fmt.Errorf("tags of %s %d: %w", table, id, err)
	}
	var have []int64
	_ = json.Unmarshal([]byte(stored), &have)
	var result []int64
	switch mode {
	case "add", "replace":
		given, err := EnsureTags(ctx, q, labels)
		if err != nil {
			return err
		}
		if mode == "add" {
			result = append(result, have...)
		}
		for _, g := range given {
			dup := false
			for _, r := range result {
				dup = dup || r == g
			}
			if !dup {
				result = append(result, g)
			}
		}
	case "remove":
		drop := map[int64]bool{}
		for _, label := range labels {
			var tagID int64
			if err := q.QueryRowContext(ctx, `SELECT id FROM tags WHERE label = ?`, strings.ToLower(strings.TrimSpace(label))).Scan(&tagID); err == nil {
				drop[tagID] = true
			}
		}
		for _, h := range have {
			if !drop[h] {
				result = append(result, h)
			}
		}
	}
	if result == nil {
		result = []int64{}
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	data, _ := json.Marshal(result)
	if _, err := q.ExecContext(ctx, `UPDATE `+table+` SET tags = ? WHERE id = ?`, string(data), id); err != nil {
		return fmt.Errorf("save tags of %s %d: %w", table, id, err)
	}
	return nil
}

// SetRootFolder records that a movie or series now lives in another library
// folder, at path.
func SetRootFolder(ctx context.Context, q Queryer, table string, id, rootFolderID int64, path string) error {
	if table != "movies" && table != "series" {
		return fmt.Errorf("unknown library table %q", table)
	}
	if _, err := q.ExecContext(ctx, `UPDATE `+table+` SET root_folder_id = ?, path = ? WHERE id = ?`, rootFolderID, path, id); err != nil {
		return fmt.Errorf("set root folder of %s %d: %w", table, id, err)
	}
	return nil
}

// SeasonPassSeason is one season on the Season Pass page.
type SeasonPassSeason struct {
	Number            int
	Monitored         bool
	Total, Downloaded int
}

// SeasonPassSeries is one series on the Season Pass page.
type SeasonPassSeries struct {
	ID        int64
	Title     string
	Slug      string
	Monitored bool
	Seasons   []SeasonPassSeason
}

// ListSeasonPass lists every series with its seasons, sorted by title the
// way the TV page sorts (a leading The, A or An ignored).
func ListSeasonPass(ctx context.Context, q Queryer) ([]SeasonPassSeries, error) {
	rows, err := q.QueryContext(ctx, `SELECT s.id, sm.title, s.monitored FROM series s JOIN series_metadata sm ON sm.id = s.series_metadata_id`)
	if err != nil {
		return nil, fmt.Errorf("list season pass series: %w", err)
	}
	var list []SeasonPassSeries
	index := map[int64]int{}
	for rows.Next() {
		var s SeasonPassSeries
		if err := rows.Scan(&s.ID, &s.Title, &s.Monitored); err != nil {
			rows.Close()
			return nil, err
		}
		s.Slug = titleutil.Slug(s.Title)
		index[s.ID] = len(list)
		list = append(list, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	seasons, err := q.QueryContext(ctx, `
		SELECT se.series_id, se.season_number, se.monitored, COUNT(e.id), COUNT(e.episode_file_id)
		FROM seasons se LEFT JOIN episodes e ON e.season_id = se.id
		GROUP BY se.id ORDER BY se.series_id, se.season_number`)
	if err != nil {
		return nil, fmt.Errorf("list season pass seasons: %w", err)
	}
	defer seasons.Close()
	for seasons.Next() {
		var seriesID int64
		var se SeasonPassSeason
		if err := seasons.Scan(&seriesID, &se.Number, &se.Monitored, &se.Total, &se.Downloaded); err != nil {
			return nil, err
		}
		if i, ok := index[seriesID]; ok {
			list[i].Seasons = append(list[i].Seasons, se)
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		return strings.ToLower(titleutil.SortTitle(list[i].Title)) < strings.ToLower(titleutil.SortTitle(list[j].Title))
	})
	return list, seasons.Err()
}
