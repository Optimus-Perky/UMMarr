// Package decision decides whether a release should be grabbed for a library
// item, following Radarr's, Sonarr's and Lidarr's download decision rules,
// and orders releases best first with Radarr's comparer.
package decision

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/customformat"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/titleutil"
)

// Target is what an approved release would be grabbed for.
type Target struct {
	MovieID    int64
	SeriesID   int64
	FullSeason bool // a season pack for Season
	Season     int
	Episodes   []store.WantedEpisode
	AlbumID    int64
	TrackID    int64
}

// Decision is one release judged for one item.
type Decision struct {
	Release  newznab.Release
	Quality  releaseparse.FileQuality
	Revision releaseparse.Revision
	Weight   int
	// QualityAllowed is whether the release's quality is wanted by the
	// item's quality profile - broken out from Rejections so callers (the
	// TV search page, at least) can filter on this specific reason without
	// string-matching a rejection message.
	QualityAllowed bool
	// KeywordScore is the sum of every matching store.PreferredWord's
	// score found in the release's title (see internal/store.PreferredWord).
	KeywordScore int
	// CustomFormats are the custom formats the release matches, and
	// CustomFormatScore what the item's quality profile scores them.
	CustomFormats     []string
	CustomFormatScore int
	// Upgrade means the item already has a file this release would replace.
	Upgrade    bool
	Rejections []string
	Target     Target
}

// Approved reports whether nothing rejected the release.
func (d Decision) Approved() bool { return len(d.Rejections) == 0 }

func (d *Decision) reject(format string, args ...any) {
	d.Rejections = append(d.Rejections, fmt.Sprintf(format, args...))
}

// Engine holds what decisions depend on, read once per search or RSS sync.
type Engine struct {
	Settings store.IndexerSettings
	Propers  string // a store.Propers* value
	Indexers map[int64]store.Indexer
	Profiles Profiles
	// PreferredWords are checked against every release title (see
	// keywordScore) - not scoped per profile/media type, since asked
	// for keyword weighting generally, not per quality profile.
	PreferredWords []store.PreferredWord
	Now            time.Time
	// UserInvoked is a search someone started: as in Radarr, monitoring and
	// availability aren't checked, since asking for it is the point.
	UserInvoked bool
	// Protocols says which protocols have an enabled download client.
	Protocols map[string]bool

	// Formats are the custom formats releases are scored against.
	Formats []customformat.Format

	// blocklist holds failed releases by the item they failed for.
	blocklist map[blocklistKey][]store.BlocklistEntry
}

// SetBlocklist replaces the blocklist the engine checks.
func (e *Engine) SetBlocklist(entries []store.BlocklistEntry) {
	e.blocklist = indexBlocklist(entries)
}

// Load reads the settings, indexers and quality profiles decisions need.
func Load(ctx context.Context, q store.Queryer, userInvoked bool) (*Engine, error) {
	settings, err := store.GetIndexerSettings(ctx, q)
	if err != nil {
		return nil, err
	}
	media, err := store.GetMediaSettings(ctx, q)
	if err != nil {
		return nil, err
	}
	indexers, err := store.ListIndexers(ctx, q)
	if err != nil {
		return nil, err
	}
	profiles, err := store.ListQualityProfiles(ctx, q)
	if err != nil {
		return nil, err
	}
	preferredWords, err := store.ListPreferredWords(ctx, q)
	if err != nil {
		return nil, err
	}
	blocklist, err := store.ListAllBlocklist(ctx, q)
	if err != nil {
		return nil, err
	}
	formats, err := store.ListCustomFormats(ctx, q)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		Settings: settings, Propers: media.PropersRepacks, Now: time.Now(), UserInvoked: userInvoked,
		Indexers: map[int64]store.Indexer{}, Profiles: Profiles{},
		PreferredWords: preferredWords, Protocols: enabledProtocols(ctx, q), Formats: formats,
	}
	e.SetBlocklist(blocklist)
	for _, ix := range indexers {
		e.Indexers[ix.ID] = ix
	}
	for _, p := range profiles {
		items, err := store.GetQualityProfileItems(ctx, q, p.ID)
		if err != nil {
			return nil, err
		}
		scores, err := store.GetProfileFormatScores(ctx, q, p.ID)
		if err != nil {
			return nil, err
		}
		e.Profiles[p.ID] = Profile{Items: items, UpgradeAllowed: p.UpgradeAllowed, Cutoff: p.Cutoff, FormatScores: scores.Scores, MinFormatScore: scores.MinScore, CutoffFormatScore: scores.CutoffScore}
	}
	return e, nil
}

// keywordScore sums the score of every store.PreferredWord whose term
// appears (case-insensitively, plain substring - no regex) in title.
// Sonarr's classic Preferred Words behaves the same way: presence, not
// occurrence count, so a repeated term can't run away with the score.
func keywordScore(title string, words []store.PreferredWord) int {
	if len(words) == 0 {
		return 0
	}
	lower := strings.ToLower(title)
	total := 0
	for _, w := range words {
		if w.Term == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(w.Term)) {
			total += w.Score
		}
	}
	return total
}

func (e *Engine) profile(id sql.NullInt64) []releaseparse.QualityProfileItem {
	return e.Profiles.Get(id).Items
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// release judges what doesn't depend on the item: quality, protocol, age,
// size, the indexer's own rules and (for video) hardcoded subtitles.
func (e *Engine) release(r newznab.Release, profileID sql.NullInt64, video bool) Decision {
	d := Decision{Release: r, Quality: releaseparse.Parse(r.Title), Revision: releaseparse.ParseRevision(r.Title)}
	weight, allowed := releaseparse.Score(e.profile(profileID), d.Quality)
	d.Weight = weight
	d.QualityAllowed = allowed
	d.KeywordScore = keywordScore(r.Title, e.PreferredWords)
	e.customFormatRule(&d, e.Profiles.Get(profileID), r)
	if !allowed {
		d.reject("%s isn't wanted in the quality profile", d.Quality.Key())
	}
	if !e.Protocols[r.Protocol] {
		d.reject("No enabled %s download client", r.Protocol)
	}
	if r.Protocol == newznab.ProtocolUsenet && !r.PublishDate.IsZero() {
		age := e.Now.Sub(r.PublishDate)
		if minAge := e.Settings.MinimumAge; minAge > 0 && age < time.Duration(minAge)*time.Minute {
			d.reject("Only %d minutes old, minimum age is %d minutes", int(age.Minutes()), minAge)
		}
		if retention := e.Settings.Retention; retention > 0 && age > time.Duration(retention)*24*time.Hour {
			d.reject("Older than the %d day retention", retention)
		}
	}
	if maxMB := e.Settings.MaximumSize; maxMB > 0 && r.Size > int64(maxMB)<<20 {
		d.reject("%s is too big, maximum size is %s (Settings → Indexers → Maximum Size)", formatSize(r.Size), formatSize(int64(maxMB)<<20))
	}
	if ix, ok := e.Indexers[r.IndexerID]; ok && r.Protocol == newznab.ProtocolTorrent {
		if r.Seeders >= 0 && r.Seeders < ix.MinimumSeeders {
			d.reject("Not enough seeders: %d. Minimum seeders: %d", r.Seeders, ix.MinimumSeeders)
		}
		if missing := missingFlags(r.Flags, ix.RequiredFlags); missing != "" {
			d.reject("Release does not have any of the required flags: %s", missing)
		}
	}
	if video {
		if subs := releaseparse.HardcodedSubs(r.Title); subs != "" && !e.Settings.AllowHardcodedSubs && !whitelisted(subs, e.Settings.WhitelistedHardcodedSubs) {
			d.reject("Hardcoded subs found: %s", subs)
		}
	}
	return d
}

// missingFlags follows Radarr's RequiredIndexerFlagsSpecification: a release
// needs at least one of the required flags. It returns the flags' names when
// the release has none of them.
func missingFlags(have newznab.Flags, required []int) string {
	if len(required) == 0 {
		return ""
	}
	var names []string
	for _, flag := range required {
		if have&newznab.Flags(flag) == newznab.Flags(flag) {
			return ""
		}
		names = append(names, flagName(newznab.Flags(flag)))
	}
	return strings.Join(names, ", ")
}

func flagName(f newznab.Flags) string {
	for _, n := range newznab.FlagNames {
		if n.Flag == f {
			return n.Name
		}
	}
	return strconv.Itoa(int(f))
}

// whitelisted follows Radarr: allowed when the tag contains any listed word.
func whitelisted(subs, list string) bool {
	for _, word := range strings.Split(list, ",") {
		if word = strings.TrimSpace(word); word != "" && strings.Contains(strings.ToLower(subs), strings.ToLower(word)) {
			return true
		}
	}
	return false
}

// MatchMovie reports whether a release is for m: by the indexer's TMDb or
// IMDb id when it gives one, otherwise by title and year (within a year, as
// release dates differ between countries).
func MatchMovie(m store.WantedMovie, r newznab.Release) (bool, string) {
	if r.TMDbID > 0 && m.TMDbID > 0 {
		if r.TMDbID == m.TMDbID {
			return true, ""
		}
		return false, fmt.Sprintf("Wrong movie: the indexer says it's TMDb %d", r.TMDbID)
	}
	if imdb, _ := strconv.Atoi(strings.TrimPrefix(m.IMDbID, "tt")); r.IMDbID > 0 && imdb > 0 {
		if r.IMDbID == imdb {
			return true, ""
		}
		return false, fmt.Sprintf("Wrong movie: the indexer says it's IMDb tt%07d", r.IMDbID)
	}
	info, ok := releaseparse.ParseMovie(r.Title)
	if !ok {
		return false, "Unable to parse the release title"
	}
	clean := titleutil.CleanTitle(info.Title)
	if clean != titleutil.CleanTitle(m.Title) && (m.OriginalTitle == "" || clean != titleutil.CleanTitle(m.OriginalTitle)) {
		if info.Year > 0 {
			return false, fmt.Sprintf("Wrong movie: release is for %s (%d)", info.Title, info.Year)
		}
		return false, fmt.Sprintf("Wrong movie: release is for %s", info.Title)
	}
	if info.Year > 0 && m.Year > 0 && (info.Year < m.Year-1 || info.Year > m.Year+1) {
		return false, fmt.Sprintf("Wrong year: release is from %d", info.Year)
	}
	return true, ""
}

// MovieAvailableFrom is Radarr's Movie.IsAvailable: the date a movie counts
// as available under its minimum availability, before the delay. ok is false
// when there's no date to go by; always is true for TBA/announced.
func MovieAvailableFrom(m store.WantedMovie) (date time.Time, always, ok bool) {
	switch strings.ToLower(m.MinimumAvailability) {
	case "tba", "announced":
		return time.Time{}, true, true
	case "incinemas":
		if m.InCinemas.Valid {
			return m.InCinemas.Time, false, true
		}
	}
	switch {
	case m.PhysicalRelease.Valid && m.DigitalRelease.Valid:
		if m.PhysicalRelease.Time.Before(m.DigitalRelease.Time) {
			return m.PhysicalRelease.Time, false, true
		}
		return m.DigitalRelease.Time, false, true
	case m.PhysicalRelease.Valid:
		return m.PhysicalRelease.Time, false, true
	case m.DigitalRelease.Valid:
		return m.DigitalRelease.Time, false, true
	case m.InCinemas.Valid:
		return m.InCinemas.Time.AddDate(0, 0, 90), false, true
	}
	return time.Time{}, false, false
}

// Movie judges releases for m, best first.
func (e *Engine) Movie(m store.WantedMovie, releases []newznab.Release) []Decision {
	out := make([]Decision, 0, len(releases))
	for _, r := range releases {
		d := e.release(r, m.QualityProfileID, true)
		d.Target = Target{MovieID: m.ID}
		if ok, reason := MatchMovie(m, r); !ok {
			d.reject("%s", reason)
		}
		if !e.UserInvoked && !m.Monitored {
			d.reject("Movie isn't monitored")
		}
		e.upgradeRule(&d, m.QualityProfileID, m.HasFile, m.FileQuality, m.FileRelease, "Movie")
		if m.Queued {
			d.reject("Movie is already in the download queue")
		}
		e.blocklistRule(&d, "movie", m.ID)
		if !e.UserInvoked {
			date, always, ok := MovieAvailableFrom(m)
			switch {
			case always:
			case !ok:
				d.reject("Movie has no release date yet, so it isn't considered available")
			case e.Now.Before(date.AddDate(0, 0, e.Settings.AvailabilityDelay)):
				d.reject("Movie won't be considered available until %s", date.AddDate(0, 0, e.Settings.AvailabilityDelay).Format("2 Jan 2006"))
			}
		}
		out = append(out, d)
	}
	e.Sort(out)
	return out
}

var yearSuffix = regexp.MustCompile(`\s*\(?((?:19|20)\d{2})\)?\s*$`)

// MatchSeriesTitle compares a parsed series name (and year, if the release
// gave one) with a series in the library, whose title may carry its year.
func MatchSeriesTitle(s store.WantedSeries, info releaseparse.EpisodeInfo) bool {
	name, year := s.Title, s.Year
	if m := yearSuffix.FindStringSubmatchIndex(s.Title); m != nil && m[0] > 0 {
		name = s.Title[:m[0]]
		year, _ = strconv.Atoi(s.Title[m[2]:m[3]])
	}
	clean := titleutil.CleanTitle(info.SeriesTitle)
	switch {
	case clean == titleutil.CleanTitle(s.Title) && info.Year == 0:
		return true
	case clean == titleutil.CleanTitle(name):
		return info.Year == 0 || year == 0 || info.Year == year
	}
	return false
}

// SeriesScope narrows a series search to a season or one episode.
type SeriesScope struct {
	Season  *int
	Episode *int
}

func episodeCode(e store.WantedEpisode) string {
	return fmt.Sprintf("S%02dE%02d", e.SeasonNumber, e.EpisodeNumber)
}

// episodesFor finds the library episodes a parsed release covers.
func episodesFor(s store.WantedSeries, info releaseparse.EpisodeInfo) ([]store.WantedEpisode, string) {
	switch {
	case !info.AirDate.IsZero():
		for _, ep := range s.Episodes {
			if ep.AirDate.Valid && ep.AirDate.Time.Format("2006-01-02") == info.AirDate.Format("2006-01-02") {
				return []store.WantedEpisode{ep}, ""
			}
		}
		return nil, fmt.Sprintf("No episode aired on %s", info.AirDate.Format("2 Jan 2006"))
	case info.FullSeason:
		if eps := s.Season(info.Season); len(eps) > 0 {
			return eps, ""
		}
		return nil, fmt.Sprintf("Season %d isn't in the library", info.Season)
	}
	var eps []store.WantedEpisode
	for _, n := range info.Episodes {
		ep, ok := s.Episode(info.Season, n)
		if !ok {
			return nil, fmt.Sprintf("Unknown episode S%02dE%02d", info.Season, n)
		}
		eps = append(eps, ep)
	}
	return eps, ""
}

// Series judges releases for s within scope, best first.
func (e *Engine) Series(s store.WantedSeries, scope SeriesScope, releases []newznab.Release) []Decision {
	out := make([]Decision, 0, len(releases))
	for _, r := range releases {
		d := e.release(r, s.QualityProfileID, true)
		d.Target = Target{SeriesID: s.ID}
		e.judgeSeries(&d, s, scope)
		out = append(out, d)
	}
	e.Sort(out)
	return out
}

func (e *Engine) judgeSeries(d *Decision, s store.WantedSeries, scope SeriesScope) {
	info, ok := releaseparse.ParseEpisode(d.Release.Title)
	if !ok {
		d.reject("Unable to parse the release title")
		return
	}
	if !MatchSeriesTitle(s, info) {
		d.reject("Wrong series: release is for %s", info.SeriesTitle)
		return
	}
	if info.MultiSeason {
		d.reject("Multi-season packs aren't supported")
		return
	}
	episodes, reason := episodesFor(s, info)
	if reason != "" {
		d.reject("%s", reason)
		return
	}
	d.Target.Episodes = episodes
	if info.FullSeason {
		d.Target.FullSeason, d.Target.Season = true, info.Season
	}
	if scope.Season != nil && episodes[0].SeasonNumber != *scope.Season {
		d.reject("Wrong season: release is season %d", episodes[0].SeasonNumber)
	}
	if scope.Episode != nil && scope.Season != nil {
		want := fmt.Sprintf("S%02dE%02d", *scope.Season, *scope.Episode)
		switch {
		case info.FullSeason:
			d.reject("Season pack, but only %s was searched for", want)
		case !containsEpisode(episodes, *scope.Season, *scope.Episode):
			d.reject("Wrong episode: release is %s", episodeCode(episodes[0]))
		}
	}

	if !e.UserInvoked && !s.Monitored {
		d.reject("Series isn't monitored")
	}
	e.blocklistRule(d, "series", s.ID)
	aired := true
	for _, ep := range episodes {
		if !e.UserInvoked && !ep.Monitored {
			d.reject("%s isn't monitored", episodeCode(ep))
		}
		e.upgradeRule(d, s.QualityProfileID, ep.HasFile, ep.FileQuality, ep.FileRelease, episodeCode(ep))
		if ep.Queued {
			d.reject("%s is already in the download queue", episodeCode(ep))
		}
		if !ep.AirDate.Valid || ep.AirDate.Time.After(e.Now) {
			aired = false
			if !info.FullSeason && !e.UserInvoked {
				d.reject("%s hasn't aired yet", episodeCode(ep))
			}
		}
	}
	if info.FullSeason && !aired {
		d.reject("Full season pack, but not every episode in season %d has aired", info.Season)
	}
}

func containsEpisode(eps []store.WantedEpisode, season, episode int) bool {
	for _, ep := range eps {
		if ep.SeasonNumber == season && ep.EpisodeNumber == episode {
			return true
		}
	}
	return false
}

// MatchAlbum reports whether a release title names both the artist and album.
func MatchAlbum(a store.WantedAlbum, title string) bool {
	clean := titleutil.CleanTitle(title)
	artist, album := titleutil.CleanTitle(a.Artist), titleutil.CleanTitle(a.Title)
	return artist != "" && album != "" && strings.Contains(clean, artist) && strings.Contains(clean, album)
}

func (e *Engine) albumRules(d *Decision, a store.WantedAlbum) {
	if !e.UserInvoked && !a.Monitored {
		d.reject("Album isn't monitored")
	}
	if a.Queued {
		d.reject("Album is already in the download queue")
	}
	e.blocklistRule(d, "album", a.ID)
	if !e.UserInvoked && a.ReleaseDate.Valid && a.ReleaseDate.Time.After(e.Now) {
		d.reject("Album isn't released until %s", a.ReleaseDate.Time.Format("2 Jan 2006"))
	}
}

// Album judges releases for a, best first.
func (e *Engine) Album(a store.WantedAlbum, releases []newznab.Release) []Decision {
	out := make([]Decision, 0, len(releases))
	for _, r := range releases {
		d := e.release(r, a.QualityProfileID, false)
		d.Target = Target{AlbumID: a.ID}
		if !MatchAlbum(a, r.Title) {
			d.reject("Wrong album: release doesn't name %s - %s", a.Artist, a.Title)
		}
		if a.FileCount() > 0 {
			d.reject("Album already has files - upgrades apply to movies and TV")
		}
		e.albumRules(&d, a)
		out = append(out, d)
	}
	e.Sort(out)
	return out
}

// Track judges releases for one track of a, best first.
func (e *Engine) Track(a store.WantedAlbum, track store.WantedTrack, releases []newznab.Release) []Decision {
	out := make([]Decision, 0, len(releases))
	artist, title := titleutil.CleanTitle(a.Artist), titleutil.CleanTitle(track.Title)
	for _, r := range releases {
		d := e.release(r, a.QualityProfileID, false)
		d.Target = Target{AlbumID: a.ID, TrackID: track.ID}
		if clean := titleutil.CleanTitle(r.Title); title == "" || !strings.Contains(clean, artist) || !strings.Contains(clean, title) {
			d.reject("Wrong track: release doesn't name %s - %s", a.Artist, track.Title)
		}
		if track.HasFile {
			d.reject("Track already has a file - upgrades apply to movies and TV")
		}
		e.albumRules(&d, a)
		out = append(out, d)
	}
	e.Sort(out)
	return out
}

// Best is the first approved decision of a sorted list.
func Best(decisions []Decision) (Decision, bool) {
	for _, d := range decisions {
		if d.Approved() {
			return d, true
		}
	}
	return Decision{}, false
}

// Sort puts approved decisions first, each group ordered by Radarr's
// DownloadDecisionComparer.
func (e *Engine) Sort(decisions []Decision) {
	sort.SliceStable(decisions, func(i, j int) bool {
		a, b := decisions[i], decisions[j]
		if a.Approved() != b.Approved() {
			return a.Approved()
		}
		return e.Better(a, b)
	})
}

// flagScore follows Radarr's ScoreFlags.
func flagScore(f newznab.Flags) int {
	score := 0
	for _, flag := range []newznab.Flags{newznab.FlagDoubleUpload, newznab.FlagFreeleech, newznab.FlagInternal} {
		if f&flag == flag {
			score += 2
		}
	}
	if f&newznab.FlagHalfleech == newznab.FlagHalfleech {
		score++
	}
	return score
}

func roundedLog10(n int) float64 {
	if n <= 0 {
		return 0
	}
	return math.Round(math.Log10(float64(n)))
}

// usenetAgeScore follows Radarr's CompareAgeIfUsenet: newer is better.
func usenetAgeScore(published, now time.Time) float64 {
	age := now.Sub(published)
	switch {
	case age < time.Hour:
		return 1000
	case age <= 24*time.Hour:
		return 100
	case age <= 7*24*time.Hour:
		return 10
	}
	return -math.Round(math.Log10(age.Hours() / 24))
}

// Better reports whether a is preferred over b: keyword score, then
// quality weight, then revision (unless propers aren't preferred),
// indexer priority, indexer flags (when preferred), seeders and peers,
// usenet age, then size.
//
// Keyword score outranks quality weight deliberately (the user, 2026-09-15,
// after Bar Rescue's auto-search grabbed a 1.3 GiB WEBDL-1080p x264
// release, score +10, over a 518 MiB HDTV-1080p x265-MeGusta release,
// score +40 and much better-seeded, purely because the profile weighted
// WEBDL-1080p at 100 vs HDTV-1080p's 80 - a real preference expressed via
// Preferred Words losing to a fairly small, often-arbitrary weight gap
// between two qualities the profile equally allows). Quality still
// matters - Decision.QualityAllowed (a separate, hard yes/no straight
// from the profile) rejects anything the profile doesn't want at all,
// before Better ever runs - this only changes the ORDER two already-ALLOWED
// qualities get ranked against each other once a keyword preference exists.
func (e *Engine) Better(a, b Decision) bool {
	// Preferred words and custom formats are both explicit preferences, so
	// together they rank ahead of quality weight - Bar Rescue, 2026-09-15:
	// a preferred HDTV x265 release lost to WEBDL on weight alone.
	if sa, sb := a.KeywordScore+a.CustomFormatScore, b.KeywordScore+b.CustomFormatScore; sa != sb {
		return sa > sb
	}
	if a.Weight != b.Weight {
		return a.Weight > b.Weight
	}
	if e.Propers != store.PropersDoNotPrefer {
		if c := a.Revision.Compare(b.Revision); c != 0 {
			return c > 0
		}
	}
	if a.Release.IndexerPriority != b.Release.IndexerPriority {
		return a.Release.IndexerPriority < b.Release.IndexerPriority
	}
	if e.Settings.PreferIndexerFlags {
		if sa, sb := flagScore(a.Release.Flags), flagScore(b.Release.Flags); sa != sb {
			return sa > sb
		}
	}
	if a.Release.Protocol == newznab.ProtocolTorrent && b.Release.Protocol == newznab.ProtocolTorrent {
		if sa, sb := roundedLog10(a.Release.Seeders), roundedLog10(b.Release.Seeders); sa != sb {
			return sa > sb
		}
		if pa, pb := roundedLog10(a.Release.Peers), roundedLog10(b.Release.Peers); pa != pb {
			return pa > pb
		}
	}
	if a.Release.Protocol == newznab.ProtocolUsenet && b.Release.Protocol == newznab.ProtocolUsenet {
		if sa, sb := usenetAgeScore(a.Release.PublishDate, e.Now), usenetAgeScore(b.Release.PublishDate, e.Now); sa != sb {
			return sa > sb
		}
	}
	const step = 200 << 20
	return a.Release.Size/step > b.Release.Size/step
}

// Profile is a quality profile as decisions use it.
type Profile struct {
	Items          []releaseparse.QualityProfileItem
	UpgradeAllowed bool
	Cutoff         string // quality key; "" means the best allowed quality
	// FormatScores scores custom formats by id; MinFormatScore rejects
	// releases below it; CutoffFormatScore stops upgrades once a file has it.
	FormatScores      map[int64]int
	MinFormatScore    int
	CutoffFormatScore int
}

// Profiles is every quality profile by id.
type Profiles map[int64]Profile

// LoadProfiles reads the quality profiles alone - enough to tell whether a
// file is below its cutoff, without the rest of the engine.
func LoadProfiles(ctx context.Context, q store.Queryer) (Profiles, error) {
	profiles, err := store.ListQualityProfiles(ctx, q)
	if err != nil {
		return nil, err
	}
	out := Profiles{}
	for _, p := range profiles {
		items, err := store.GetQualityProfileItems(ctx, q, p.ID)
		if err != nil {
			return nil, err
		}
		scores, err := store.GetProfileFormatScores(ctx, q, p.ID)
		if err != nil {
			return nil, err
		}
		out[p.ID] = Profile{Items: items, UpgradeAllowed: p.UpgradeAllowed, Cutoff: p.Cutoff, FormatScores: scores.Scores, MinFormatScore: scores.MinScore, CutoffFormatScore: scores.CutoffScore}
	}
	return out, nil
}

// Get is the profile for id, or the default weights when the item has none.
func (ps Profiles) Get(id sql.NullInt64) Profile {
	if id.Valid {
		if p, ok := ps[id.Int64]; ok {
			return p
		}
	}
	return Profile{Items: store.DefaultQualityProfileItems()}
}

// CutoffWeight is the weight a file must reach for upgrades to stop: the
// cutoff quality's, or the best allowed quality's when no cutoff is set.
func (p Profile) CutoffWeight() (weight int, name string) {
	best, bestName := math.MinInt, ""
	for _, it := range p.Items {
		if !it.Allowed {
			continue
		}
		if it.Quality == p.Cutoff {
			return it.Weight, it.Quality
		}
		if it.Weight > best {
			best, bestName = it.Weight, it.Quality
		}
	}
	if best == math.MinInt {
		return 0, ""
	}
	return best, bestName
}

// CutoffUnmet reports whether a file of quality q, under profile id, is
// below its cutoff and so still wanted - Radarr's Cutoff Unmet.
func (ps Profiles) CutoffUnmet(id sql.NullInt64, q releaseparse.FileQuality) bool {
	p := ps.Get(id)
	if !p.UpgradeAllowed {
		return false
	}
	weight, _ := releaseparse.Score(p.Items, q)
	cutoff, _ := p.CutoffWeight()
	return weight < cutoff
}

// upgradeRule is Radarr's UpgradableSpecification for an item that already
// has a file: the profile must allow upgrades, the file must be below the
// cutoff, and the release must be better - a higher weight, or the same
// weight with a newer proper/repack when those are preferred and upgraded.
func (e *Engine) upgradeRule(d *Decision, profileID sql.NullInt64, hasFile bool, current releaseparse.FileQuality, currentRelease, what string) {
	if !hasFile {
		return
	}
	d.Upgrade = true
	p := e.Profiles.Get(profileID)
	currentKey := current.Key()
	if !p.UpgradeAllowed {
		d.reject("%s already has a file (%s) and the quality profile doesn't allow upgrades", what, currentKey)
		return
	}
	currentWeight, _ := releaseparse.Score(p.Items, current)
	cutoff, cutoffName := p.CutoffWeight()
	// As in Radarr, the cutoff is only met when the file reaches both the
	// cutoff quality and the Upgrade Until Custom Format Score.
	currentFormatScore := e.fileFormatScore(p, currentRelease)
	formatCutoffMet := len(e.Formats) == 0 || currentFormatScore >= p.CutoffFormatScore
	if currentWeight >= cutoff && formatCutoffMet {
		d.reject("%s's file (%s) already meets the profile's cutoff (%s)", what, currentKey, cutoffName)
		return
	}
	switch {
	case d.Weight > currentWeight:
	case d.Weight == currentWeight && e.Propers == store.PropersPreferAndUpgrade && d.Revision.Compare(releaseparse.ParseRevision(currentRelease)) > 0:
	case d.Weight == currentWeight && !formatCutoffMet && d.CustomFormatScore > currentFormatScore:
	default:
		d.reject("Not an upgrade: %s's file is already %s", what, currentKey)
	}
}

// enabledProtocols reads which protocols have an enabled download client. With
// no client saved at all, torrents are assumed: that's the .env Deluge of an
// older setup, which the download service still honours.
func enabledProtocols(ctx context.Context, q store.Queryer) map[string]bool {
	out := map[string]bool{}
	clients, err := store.ListDownloadClients(ctx, q)
	if err != nil {
		return out
	}
	if len(clients) == 0 {
		out[newznab.ProtocolTorrent] = true
		return out
	}
	for _, c := range clients {
		if c.Enabled {
			out[c.Protocol()] = true
		}
	}
	return out
}
