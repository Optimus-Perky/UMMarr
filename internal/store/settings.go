package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
)

// RootFolder mirrors one root_folders row (migration 00001).
type RootFolder struct {
	ID        int64
	Path      string
	MediaType string
}

// QualityProfile mirrors one quality_profiles row (migration 00003).
type QualityProfile struct {
	ID        int64
	Name      string
	IsDefault bool // preselected by the Add forms
	// UpgradeAllowed and Cutoff are Radarr's upgrade rule: a file below the
	// cutoff quality is replaced by a better release. "" means the best
	// allowed quality.
	UpgradeAllowed bool
	Cutoff         string
}

// ListRootFolders lists root folders for one media type
// ("movie"/"series"/"music") - what each media page's Add form uses to
// populate its root-folder picker.
func ListRootFolders(ctx context.Context, q Queryer, mediaType string) ([]RootFolder, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, path, media_type FROM root_folders WHERE media_type = ? ORDER BY path`, mediaType)
	if err != nil {
		return nil, fmt.Errorf("list root folders: %w", err)
	}
	defer rows.Close()

	var folders []RootFolder
	for rows.Next() {
		var f RootFolder
		if err := rows.Scan(&f.ID, &f.Path, &f.MediaType); err != nil {
			return nil, fmt.Errorf("scan root folder: %w", err)
		}
		folders = append(folders, f)
	}
	return folders, rows.Err()
}

// CreateRootFolder adds a new root folder for mediaType.
func CreateRootFolder(ctx context.Context, q Queryer, path, mediaType string) (int64, error) {
	res, err := q.ExecContext(ctx, `INSERT INTO root_folders (path, media_type) VALUES (?, ?)`, path, mediaType)
	if err != nil {
		return 0, fmt.Errorf("create root folder: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted root folder id: %w", err)
	}
	return id, nil
}

// ListQualityProfiles lists all quality profiles - these aren't scoped by
// media type in the schema (migration 00003), so every page's Add form
// shares the same list.
func ListQualityProfiles(ctx context.Context, q Queryer) ([]QualityProfile, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name, is_default, upgrade_allowed, cutoff_quality FROM quality_profiles ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list quality profiles: %w", err)
	}
	defer rows.Close()

	var profiles []QualityProfile
	for rows.Next() {
		var p QualityProfile
		if err := rows.Scan(&p.ID, &p.Name, &p.IsDefault, &p.UpgradeAllowed, &p.Cutoff); err != nil {
			return nil, fmt.Errorf("scan quality profile: %w", err)
		}
		profiles = append(profiles, p)
	}
	return profiles, rows.Err()
}

// CreateQualityProfile adds a new quality profile, seeded with a default
// weight table (see defaultQualityProfileItems) so it's immediately
// useful without the user having to configure anything - every catalog
// entry allowed, weight = its index in releaseparse.AllQualities (higher
// tiers default to higher weight).
func CreateQualityProfile(ctx context.Context, q Queryer, name string) (int64, error) {
	items, err := marshalJSON(defaultQualityProfileItems(), "[]")
	if err != nil {
		return 0, fmt.Errorf("marshal default quality profile items: %w", err)
	}
	res, err := q.ExecContext(ctx, `INSERT INTO quality_profiles (name, items, is_default) VALUES (?, ?, NOT EXISTS (SELECT 1 FROM quality_profiles))`, name, items)
	if err != nil {
		return 0, fmt.Errorf("create quality profile: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted quality profile id: %w", err)
	}
	return id, nil
}

// defaultQualityProfileItems seeds every releaseparse.AllQualities entry
// as allowed, weighted by catalog order (worst=0, best=len-1).
func defaultQualityProfileItems() []releaseparse.QualityProfileItem {
	items := make([]releaseparse.QualityProfileItem, len(releaseparse.AllQualities))
	for i, quality := range releaseparse.AllQualities {
		items[i] = releaseparse.QualityProfileItem{Quality: quality, Weight: i, Allowed: true}
	}
	return items
}

// GetQualityProfileItems returns one row per releaseparse.AllQualities
// entry, in that catalog's order (NOT saved weight order) - Weight/
// Allowed default to 0/false for any quality never saved (e.g. a
// profile created before this feature existed, or before AllQualities
// grew a new entry), so a stale/partial saved list degrades safely
// instead of erroring or omitting a row the UI needs to render.
func GetQualityProfileItems(ctx context.Context, q Queryer, profileID int64) ([]releaseparse.QualityProfileItem, error) {
	var raw string
	if err := q.QueryRowContext(ctx, `SELECT items FROM quality_profiles WHERE id = ?`, profileID).Scan(&raw); err != nil {
		return nil, fmt.Errorf("get quality profile %d items: %w", profileID, err)
	}
	var saved []releaseparse.QualityProfileItem
	_ = json.Unmarshal([]byte(raw), &saved)
	savedByQuality := make(map[string]releaseparse.QualityProfileItem, len(saved))
	for _, it := range saved {
		savedByQuality[it.Quality] = it
	}

	items := make([]releaseparse.QualityProfileItem, len(releaseparse.AllQualities))
	for i, quality := range releaseparse.AllQualities {
		items[i] = releaseparse.QualityProfileItem{Quality: quality, Weight: savedByQuality[quality].Weight, Allowed: savedByQuality[quality].Allowed}
	}
	return items, nil
}

// UpdateQualityProfileItems saves profileID's weight table.
func UpdateQualityProfileItems(ctx context.Context, q Queryer, profileID int64, items []releaseparse.QualityProfileItem) error {
	j, err := marshalJSON(items, "[]")
	if err != nil {
		return fmt.Errorf("marshal quality profile items: %w", err)
	}
	if _, err := q.ExecContext(ctx, `UPDATE quality_profiles SET items = ? WHERE id = ?`, j, profileID); err != nil {
		return fmt.Errorf("update quality profile %d items: %w", profileID, err)
	}
	return nil
}

// ItemFolderPaths lists the saved folders of every movie, series or artist -
// whichever mediaType's root folders hold ("movie"/"series"/"music"). Used to
// tell which folders inside a root folder UMMarr doesn't know about.
func ItemFolderPaths(ctx context.Context, q Queryer, mediaType string) ([]string, error) {
	table := map[string]string{"movie": "movies", "series": "series", "music": "artists", "album": "albums"}[mediaType]
	if table == "" {
		return nil, fmt.Errorf("item folder paths: unknown media type %q", mediaType)
	}
	rows, err := q.QueryContext(ctx, `SELECT path FROM `+table+` WHERE path IS NOT NULL AND path != ''`)
	if err != nil {
		return nil, fmt.Errorf("list %s folder paths: %w", table, err)
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("scan %s folder path: %w", table, err)
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// SetDefaultQualityProfile makes profile id the one the Add forms
// preselect, clearing the flag on every other profile.
func SetDefaultQualityProfile(ctx context.Context, q Queryer, id int64) error {
	res, err := q.ExecContext(ctx, `UPDATE quality_profiles SET is_default = (id = ?)`, id)
	if err != nil {
		return fmt.Errorf("set default quality profile: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("set default quality profile: no profiles")
	}
	return nil
}

// DefaultQualityProfileID is the profile the Add forms preselect, or the
// oldest profile when none is flagged.
func DefaultQualityProfileID(ctx context.Context, q Queryer) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM quality_profiles ORDER BY is_default DESC, id LIMIT 1`).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("default quality profile: %w", err)
	}
	return id, nil
}

// UpdateQualityProfileUpgrades saves a profile's upgrade rule.
func UpdateQualityProfileUpgrades(ctx context.Context, q Queryer, profileID int64, allowed bool, cutoff string) error {
	if _, err := q.ExecContext(ctx, `UPDATE quality_profiles SET upgrade_allowed = ?, cutoff_quality = ? WHERE id = ?`, allowed, cutoff, profileID); err != nil {
		return fmt.Errorf("update quality profile %d upgrades: %w", profileID, err)
	}
	return nil
}

// QualityProfileUsage counts what a profile is attached to, so deleting one
// that is still in use can be refused rather than silently leaving movies,
// series or artists pointing at a profile that no longer exists.
func QualityProfileUsage(ctx context.Context, q Queryer, id int64) (movies, series, artists int, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM movies WHERE quality_profile_id = ?),
		       (SELECT COUNT(*) FROM series WHERE quality_profile_id = ?),
		       (SELECT COUNT(*) FROM artists WHERE quality_profile_id = ?)`, id, id, id).
		Scan(&movies, &series, &artists)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("quality profile %d usage: %w", id, err)
	}
	return movies, series, artists, nil
}

// DeleteQualityProfile removes a profile nothing uses. The last profile
// stays: with none at all, nothing could be added. When the default is
// removed, the oldest remaining profile takes over, so there is always one
// for the Add forms to preselect.
func DeleteQualityProfile(ctx context.Context, q Queryer, id int64) error {
	movies, series, artists, err := QualityProfileUsage(ctx, q, id)
	if err != nil {
		return err
	}
	if n := movies + series + artists; n > 0 {
		var parts []string
		for _, use := range []struct {
			n    int
			noun string
		}{{movies, "movie"}, {series, "series"}, {artists, "artist"}} {
			if use.n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", use.n, plural(use.n, use.noun)))
			}
		}
		return fmt.Errorf("%s still using this profile - move them to another profile first", strings.Join(parts, ", "))
	}
	var total int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM quality_profiles`).Scan(&total); err != nil {
		return fmt.Errorf("count quality profiles: %w", err)
	}
	if total <= 1 {
		return fmt.Errorf("this is the only quality profile - add another before removing it")
	}
	var wasDefault bool
	_ = q.QueryRowContext(ctx, `SELECT is_default FROM quality_profiles WHERE id = ?`, id).Scan(&wasDefault)
	if _, err := q.ExecContext(ctx, `DELETE FROM quality_profiles WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete quality profile %d: %w", id, err)
	}
	if wasDefault {
		var next int64
		if err := q.QueryRowContext(ctx, `SELECT id FROM quality_profiles ORDER BY id LIMIT 1`).Scan(&next); err == nil {
			return SetDefaultQualityProfile(ctx, q, next)
		}
	}
	return nil
}

// plural is "movie"/"movies", and leaves an already-plural noun alone.
func plural(n int, noun string) string {
	if n == 1 || strings.HasSuffix(noun, "s") {
		return noun
	}
	return noun + "s"
}
