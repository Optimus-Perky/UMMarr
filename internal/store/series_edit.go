package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SeriesSettings is what Sonarr's Edit Series dialog changes.
type SeriesSettings struct {
	Monitored        bool
	MonitorNewItems  string // all | none
	SeasonFolder     bool
	QualityProfileID int64
	SeriesType       string // standard | daily | anime
	Path             string
	Tags             []string
}

// SeriesTypes are Sonarr's, in its order.
var SeriesTypes = []string{"standard", "daily", "anime"}

// GetSeriesSettings reads the editable settings of seriesID, tags as labels.
func GetSeriesSettings(ctx context.Context, q Queryer, seriesID int64) (SeriesSettings, error) {
	var s SeriesSettings
	var profile sql.NullInt64
	var path sql.NullString
	var tags string
	err := q.QueryRowContext(ctx, `SELECT monitored, monitor_new_items, season_folder, quality_profile_id, series_type, path, tags FROM series WHERE id = ?`, seriesID).
		Scan(&s.Monitored, &s.MonitorNewItems, &s.SeasonFolder, &profile, &s.SeriesType, &path, &tags)
	if err != nil {
		return s, fmt.Errorf("get series %d settings: %w", seriesID, err)
	}
	s.QualityProfileID, s.Path = profile.Int64, path.String
	s.Tags, err = TagLabels(ctx, q, tags)
	return s, err
}

// UpdateSeriesSettings saves the Edit Series dialog. Tags are created as
// needed; the path is stored as given (moving the folder is the caller's job).
func UpdateSeriesSettings(ctx context.Context, q Queryer, seriesID int64, s SeriesSettings) error {
	if s.MonitorNewItems != "none" {
		s.MonitorNewItems = "all"
	}
	if s.SeriesType != "daily" && s.SeriesType != "anime" {
		s.SeriesType = "standard"
	}
	ids, err := EnsureTags(ctx, q, s.Tags)
	if err != nil {
		return err
	}
	tags, _ := json.Marshal(ids)
	_, err = q.ExecContext(ctx, `UPDATE series SET monitored = ?, monitor_new_items = ?, season_folder = ?, quality_profile_id = ?, series_type = ?, path = ?, tags = ? WHERE id = ?`,
		s.Monitored, s.MonitorNewItems, s.SeasonFolder, s.QualityProfileID, s.SeriesType, s.Path, string(tags), seriesID)
	if err != nil {
		return fmt.Errorf("update series %d settings: %w", seriesID, err)
	}
	return nil
}

// EnsureTags returns the ids of the given tag labels, creating any that
// don't exist. Blank labels are dropped, duplicates collapsed.
func EnsureTags(ctx context.Context, q Queryer, labels []string) ([]int64, error) {
	ids := []int64{}
	seen := map[string]bool{}
	for _, label := range labels {
		label = strings.ToLower(strings.TrimSpace(label))
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		if _, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO tags (label) VALUES (?)`, label); err != nil {
			return nil, fmt.Errorf("create tag %q: %w", label, err)
		}
		var id int64
		if err := q.QueryRowContext(ctx, `SELECT id FROM tags WHERE label = ?`, label).Scan(&id); err != nil {
			return nil, fmt.Errorf("find tag %q: %w", label, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// TagLabels turns a stored JSON id array into labels, sorted.
func TagLabels(ctx context.Context, q Queryer, stored string) ([]string, error) {
	var ids []int64
	if stored != "" {
		if err := json.Unmarshal([]byte(stored), &ids); err != nil {
			return nil, fmt.Errorf("decode tags %q: %w", stored, err)
		}
	}
	labels := []string{}
	for _, id := range ids {
		var label string
		if err := q.QueryRowContext(ctx, `SELECT label FROM tags WHERE id = ?`, id).Scan(&label); err == nil {
			labels = append(labels, label)
		}
	}
	sort.Strings(labels)
	return labels, nil
}

// MonitorOption is one choice of Sonarr's Series Monitoring dialog.
type MonitorOption struct{ Value, Label, Help string }

// MonitorOptions are Sonarr's, in its order.
var MonitorOptions = []MonitorOption{
	{"all", "All Episodes", "Monitor all episodes except specials"},
	{"future", "Future Episodes", "Monitor episodes that have not aired yet"},
	{"missing", "Missing Episodes", "Monitor episodes that do not have files or have not aired yet"},
	{"existing", "Existing Episodes", "Monitor episodes that have files or have not aired yet"},
	{"recent", "Recent Episodes", "Monitor episodes aired within the last 90 days and future episodes"},
	{"pilot", "Pilot Episode", "Monitor only the first episode of the first season"},
	{"firstSeason", "First Season", "Monitor all episodes of the first season. All other seasons will be ignored"},
	{"lastSeason", "Last Season", "Monitor all episodes of the last season"},
	{"monitorSpecials", "Monitor Specials", "Monitor all special episodes without changing the monitored status of other episodes"},
	{"unmonitorSpecials", "Unmonitor Specials", "Unmonitor all special episodes without changing the monitored status of other episodes"},
	{"none", "None", "No episodes will be monitored"},
}

// ApplyMonitorOption sets every episode's and season's monitored flag of
// seriesID the way Sonarr's Series Monitoring dialog does. Specials are
// left alone except by the specials options and None.
func ApplyMonitorOption(ctx context.Context, q Queryer, seriesID int64, option string, now time.Time) error {
	known := false
	for _, o := range MonitorOptions {
		if o.Value == option {
			known = true
		}
	}
	if !known {
		return fmt.Errorf("unknown monitoring option %q", option)
	}
	rows, err := q.QueryContext(ctx, `SELECT id, season_number, episode_number, air_date, monitored, episode_file_id IS NOT NULL FROM episodes WHERE series_id = ? ORDER BY season_number, episode_number`, seriesID)
	if err != nil {
		return fmt.Errorf("list episodes for series %d: %w", seriesID, err)
	}
	type ep struct {
		id             int64
		season, number int
		aired          sql.NullTime
		monitored      bool
		hasFile        bool
	}
	var episodes []ep
	firstSeason, lastSeason := 0, 0
	for rows.Next() {
		var e ep
		if err := rows.Scan(&e.id, &e.season, &e.number, &e.aired, &e.monitored, &e.hasFile); err != nil {
			rows.Close()
			return err
		}
		episodes = append(episodes, e)
		if e.season > 0 {
			if firstSeason == 0 || e.season < firstSeason {
				firstSeason = e.season
			}
			if e.season > lastSeason {
				lastSeason = e.season
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	future := func(e ep) bool { return !e.aired.Valid || e.aired.Time.After(now) }
	want := func(e ep) bool {
		if e.season == 0 {
			switch option {
			case "monitorSpecials":
				return true
			case "unmonitorSpecials", "none":
				return false
			}
			return e.monitored
		}
		switch option {
		case "all":
			return true
		case "future":
			return future(e)
		case "missing":
			return !e.hasFile || future(e)
		case "existing":
			return e.hasFile || future(e)
		case "recent":
			return future(e) || e.aired.Time.After(now.Add(-90*24*time.Hour))
		case "pilot":
			return e.season == firstSeason && e.number == 1
		case "firstSeason":
			return e.season == firstSeason
		case "lastSeason":
			return e.season == lastSeason
		case "none":
			return false
		}
		return e.monitored
	}
	seasonHas := map[int]bool{}
	for _, e := range episodes {
		on := want(e)
		if on != e.monitored {
			if _, err := q.ExecContext(ctx, `UPDATE episodes SET monitored = ? WHERE id = ?`, on, e.id); err != nil {
				return fmt.Errorf("update episode %d monitored: %w", e.id, err)
			}
		}
		if on {
			seasonHas[e.season] = true
		}
	}
	seasonRows, err := q.QueryContext(ctx, `SELECT season_number, monitored FROM seasons WHERE series_id = ?`, seriesID)
	if err != nil {
		return err
	}
	type se struct {
		number    int
		monitored bool
	}
	var seasons []se
	for seasonRows.Next() {
		var s se
		if err := seasonRows.Scan(&s.number, &s.monitored); err != nil {
			seasonRows.Close()
			return err
		}
		seasons = append(seasons, s)
	}
	seasonRows.Close()
	for _, s := range seasons {
		on := seasonHas[s.number]
		switch {
		case s.number == 0 && option != "monitorSpecials" && option != "unmonitorSpecials" && option != "none":
			continue // specials season untouched, like its episodes
		case s.number > 0 && (option == "all" || option == "future"):
			on = true // an empty upcoming season counts too
		case s.number > 0 && (option == "monitorSpecials" || option == "unmonitorSpecials"):
			continue
		}
		if on != s.monitored {
			if _, err := q.ExecContext(ctx, `UPDATE seasons SET monitored = ? WHERE series_id = ? AND season_number = ?`, on, seriesID, s.number); err != nil {
				return fmt.Errorf("update season %d monitored: %w", s.number, err)
			}
		}
	}
	return nil
}
