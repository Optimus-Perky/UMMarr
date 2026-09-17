package merge

import (
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
)

// seasonSource is one provider's view of one season, produced by
// adapt_tmdb.go/adapt_tvmaze.go. TVMaze has no season entity of its own -
// its adapter groups the show's flat episode list by episode.Season to
// produce these.
type seasonSource struct {
	Provider     string
	SeasonNumber int
	Poster       string
	Episodes     []episodeSource
}

// episodeSource is one provider's view of one episode.
type episodeSource struct {
	Provider      string
	EpisodeNumber int
	Title         string
	Overview      string
	AirDate       *time.Time
	Runtime       int
}

// MergeSeasons unions season/episode lists from multiple providers (e.g.
// TMDB's explicit seasons, TVMaze's flat episode list grouped by season)
// into one reconciled list, per the plan's note that providers can
// disagree on season/episode existence and need real reconciliation, not
// just field-level provenance on a single row.
//
// Unlike MergeMovie/MergeSeries, this does NOT return a flat Provenance
// slice: a single call here covers many season/episode entities, and
// their entity_ids only exist once a later pass actually persists each
// row. Provenance is still fully captured, just embedded per-field in the
// returned Field[T].Provider values - the persisting pass reads it off
// each episode/season when it has an entity_id to attach it to.
func MergeSeasons(sourceLists [][]seasonSource, opts Options) ([]metadata.SeasonMetadata, metadata.MergeReport) {
	episodePriority := withDefaults(opts.EpisodeFieldPriority, DefaultEpisodeFieldPriority())
	listPriority := EpisodeListPriority()
	if len(opts.EpisodeListPriority) > 0 {
		listPriority = opts.EpisodeListPriority
	}

	seasonsByNumber := map[int]map[string]seasonSource{}
	for _, list := range sourceLists {
		for _, s := range list {
			if seasonsByNumber[s.SeasonNumber] == nil {
				seasonsByNumber[s.SeasonNumber] = map[string]seasonSource{}
			}
			seasonsByNumber[s.SeasonNumber][s.Provider] = s
		}
	}

	var report metadata.MergeReport
	var seasons []metadata.SeasonMetadata
	for _, seasonNumber := range sortedIntKeys(seasonsByNumber) {
		byProvider := seasonsByNumber[seasonNumber]

		episodeCounts := map[string]int{}
		episodesByNumber := map[int]map[string]episodeSource{}
		for provider, s := range byProvider {
			episodeCounts[provider] = len(s.Episodes)
			for _, ep := range s.Episodes {
				if episodesByNumber[ep.EpisodeNumber] == nil {
					episodesByNumber[ep.EpisodeNumber] = map[string]episodeSource{}
				}
				episodesByNumber[ep.EpisodeNumber][provider] = ep
			}
		}
		if conflict := describeCountConflict(episodeCounts); conflict != "" {
			report.Conflicts = append(report.Conflicts, fmt.Sprintf("season %d: %s", seasonNumber, conflict))
		}

		// One provider decides which episodes the season has; the others
		// only describe those. See EpisodeListPriority.
		owner := episodeListOwner(byProvider, listPriority)
		for number, byEpisodeProvider := range episodesByNumber {
			if _, ok := byEpisodeProvider[owner]; !ok {
				delete(episodesByNumber, number)
			}
		}

		var episodes []metadata.EpisodeMetadata
		for _, episodeNumber := range sortedIntKeys(episodesByNumber) {
			byEpisodeProvider := episodesByNumber[episodeNumber]
			titles, overviews := map[string]string{}, map[string]string{}
			airDates := map[string]*time.Time{}
			runtimes := map[string]int{}
			placeholders := map[string]string{}
			for provider, ep := range byEpisodeProvider {
				if placeholderEpisodeTitle(ep.Title) {
					placeholders[provider] = ep.Title
				} else {
					titles[provider] = ep.Title
				}
				overviews[provider] = ep.Overview
				airDates[provider] = ep.AirDate
				runtimes[provider] = ep.Runtime
			}
			title := pickString(episodePriority["title"], titles)
			if title.Value == "" {
				title = pickString(episodePriority["title"], placeholders)
			}
			episodes = append(episodes, metadata.EpisodeMetadata{
				EpisodeNumber: episodeNumber,
				Title:         title,
				Overview:      pickString(episodePriority["overview"], overviews),
				AirDate:       pickTime(episodePriority["air_date"], airDates),
				Runtime:       pickInt(episodePriority["runtime"], runtimes),
			})
		}

		poster := ""
		for _, provider := range []string{"tmdb", "tvmaze"} {
			if src, ok := seasonsByNumber[seasonNumber][provider]; ok && src.Poster != "" {
				poster = src.Poster
				break
			}
		}
		seasons = append(seasons, metadata.SeasonMetadata{
			Poster:       poster,
			SeasonNumber: seasonNumber,
			Monitored:    true,
			Episodes:     episodes,
		})
	}

	return seasons, report
}

// describeCountConflict returns a human-readable note when providers that
// both reported episodes for a season disagree on how many, or "" if
// there's nothing to flag (only one provider has data, or they agree).
func describeCountConflict(counts map[string]int) string {
	if len(counts) < 2 {
		return ""
	}
	first := -1
	agree := true
	for _, c := range counts {
		if first == -1 {
			first = c
			continue
		}
		if c != first {
			agree = false
		}
	}
	if agree {
		return ""
	}
	desc := ""
	for _, provider := range sortedStringKeys(counts) {
		if desc != "" {
			desc += ", "
		}
		desc += fmt.Sprintf("%s=%d episodes", provider, counts[provider])
	}
	return desc
}

func sortedIntKeys[V any](m map[int]V) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// episodeListOwner picks the provider whose episode numbering a season
// takes, the first in EpisodeListPriority that returned any episode for
// it (priority is EpisodeListPriority unless overridden). A season only TVMaze knows about is still TVMaze's - the rule keeps
// providers from inventing episodes alongside a better source, not from
// being the source when there is no other.
func episodeListOwner(byProvider map[string]seasonSource, priority []string) string {
	for _, provider := range priority {
		if s, ok := byProvider[provider]; ok && len(s.Episodes) > 0 {
			return provider
		}
	}
	for _, provider := range sortedStringKeys(byProvider) {
		if len(byProvider[provider].Episodes) > 0 {
			return provider
		}
	}
	return ""
}

// placeholderTitle matches the stand-in names providers give episodes
// before the real ones are announced.
var placeholderTitle = regexp.MustCompile(`(?i)^\s*(episode|ep\.?|chapter|part)\s*#?\s*\d+\s*$|^\s*(tba|tbd|to be announced)\s*$`)

// placeholderEpisodeTitle reports whether title is a stand-in such as
// "Episode 5" or "TBA". TMDB lists a new season's episodes that way for
// months, while TVmaze often already has the real names - MobLand season
// 2, 2026-09-17: TMDB "Episode 1" to "Episode 10", TVmaze "I Wanna Be Your
// Dog", "Song 2"... for the first four. A real title from any provider
// beats a placeholder from a higher-priority one.
func placeholderEpisodeTitle(title string) bool {
	return placeholderTitle.MatchString(title)
}

// PlaceholderEpisodeTitle reports whether a title is a stand-in such as
// "Episode 5" or "TBA", for callers deciding whether a name is worth having.
func PlaceholderEpisodeTitle(title string) bool { return placeholderEpisodeTitle(title) }
