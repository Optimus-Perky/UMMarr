package merge

import (
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
)

// pickString returns a Field[string] set to the first non-empty value
// found in priority order among values (keyed by provider), or a zero
// Field if none of the listed providers set it.
func pickString(priority []string, values map[string]string) metadata.Field[string] {
	for _, p := range priority {
		if v, ok := values[p]; ok && v != "" {
			return metadata.Field[string]{Value: v, Provider: p}
		}
	}
	return metadata.Field[string]{}
}

func pickInt(priority []string, values map[string]int) metadata.Field[int] {
	for _, p := range priority {
		if v, ok := values[p]; ok && v != 0 {
			return metadata.Field[int]{Value: v, Provider: p}
		}
	}
	return metadata.Field[int]{}
}

func pickStrings(priority []string, values map[string][]string) metadata.Field[[]string] {
	for _, p := range priority {
		if v, ok := values[p]; ok && len(v) > 0 {
			return metadata.Field[[]string]{Value: v, Provider: p}
		}
	}
	return metadata.Field[[]string]{}
}

func pickTime(priority []string, values map[string]*time.Time) metadata.Field[*time.Time] {
	for _, p := range priority {
		if v, ok := values[p]; ok && v != nil {
			return metadata.Field[*time.Time]{Value: v, Provider: p}
		}
	}
	return metadata.Field[*time.Time]{}
}

// provenanceIfSet returns a Provenance for fieldName if the picked field
// actually has a provider attributed to it (i.e. some provider set it).
func provenanceIfSet(entityType, fieldName, provider string) (metadata.Provenance, bool) {
	if provider == "" {
		return metadata.Provenance{}, false
	}
	return metadata.Provenance{EntityType: entityType, FieldName: fieldName, Provider: provider}, true
}

// mergeExternalIDs unions provider->id maps from multiple sources into the
// slice shape external_ids rows are written from. Later sources don't
// override earlier ones for the same provider - each source should only
// ever contribute its own provider's id(s) anyway.
func mergeExternalIDs(sources ...map[string]string) []metadata.ExternalID {
	seen := map[string]string{}
	for _, src := range sources {
		for provider, id := range src {
			if id == "" {
				continue
			}
			if _, ok := seen[provider]; !ok {
				seen[provider] = id
			}
		}
	}
	ids := make([]metadata.ExternalID, 0, len(seen))
	for provider, id := range seen {
		ids = append(ids, metadata.ExternalID{Provider: provider, ExternalID: id})
	}
	return ids
}
