package merge

import "sort"

// ProviderOrder is Settings -> Metadata -> Sources: which provider is asked
// first for each media type. An empty list keeps the built-in order.
type ProviderOrder struct {
	Movie  []string
	Series []string
}

// OptionsFromOrder turns a provider order into field priorities. Each
// field keeps the providers that can supply it, sorted by the order; a
// field only one provider supplies (movie images, say) is unaffected. For
// TV the order also decides which provider's episode list a season takes.
func OptionsFromOrder(o ProviderOrder) Options {
	var opts Options
	if len(o.Movie) > 0 {
		opts.MovieFieldPriority = reorder(DefaultMovieFieldPriority(), o.Movie)
	}
	if len(o.Series) > 0 {
		opts.SeriesFieldPriority = reorder(DefaultSeriesFieldPriority(), o.Series)
		opts.EpisodeFieldPriority = reorder(DefaultEpisodeFieldPriority(), o.Series)
		opts.EpisodeListPriority = sortedBy(EpisodeListPriority(), o.Series)
	}
	return opts
}

func reorder(defaults map[string][]string, order []string) map[string][]string {
	out := make(map[string][]string, len(defaults))
	for field, providers := range defaults {
		out[field] = sortedBy(providers, order)
	}
	return out
}

// sortedBy orders providers by their place in order; any not in order keep
// their relative place after the ones that are.
func sortedBy(providers, order []string) []string {
	rank := map[string]int{}
	for i, p := range order {
		rank[p] = i
	}
	out := append([]string(nil), providers...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, iok := rank[out[i]]
		rj, jok := rank[out[j]]
		switch {
		case iok && jok:
			return ri < rj
		default:
			return iok && !jok
		}
	})
	return out
}
