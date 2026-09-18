package releaseparse

// AllQualities is every quality tier UMMarr recognizes, worst to best -
// the fixed row set every quality profile's weight table is built from
// (see QualityProfileItem, internal/store's GetQualityProfileItems).
// Deliberately closed and small, matched to what Parse can actually
// produce (Source x Resolution combos) rather than an open-ended
// Custom-Formats system.
var AllQualities = []string{
	"Unknown", "SDTV", "DVD",
	"WEBRip-480p", "WEBDL-480p",
	"HDTV-720p", "WEBRip-720p", "WEBDL-720p", "Bluray-720p",
	"HDTV-1080p", "WEBRip-1080p", "WEBDL-1080p", "Bluray-1080p", "Remux-1080p",
	"HDTV-2160p", "WEBRip-2160p", "WEBDL-2160p", "Bluray-2160p", "Remux-2160p",
}

// Key maps a parsed FileQuality onto one row of AllQualities - "Unknown"
// if neither source nor resolution was detected, a bare resolution
// falling back to "Unknown" too if paired with no recognized source
// (there's no meaningful catalog entry for e.g. "unknown source,
// 1080p" - it's not reliably any of the listed tiers), otherwise
// "Source-Resolution" if that combination is in the catalog, else
// "Unknown" as a safe fallback (e.g. "DVD-720p", which the catalog
// doesn't model since DVD never comes in HD resolutions in practice).
func (q FileQuality) Key() string {
	if !q.Audio.Empty() {
		return q.Audio.Key()
	}
	if q.Source == "SDTV" || (q.Source == "HDTV" && q.Resolution == "480p") {
		return "SDTV" // standard definition TV, whatever the resolution tag
	}
	if q.Source == "DVD" {
		return "DVD" // DVD has no resolution tiers in the catalog
	}
	if q.Source == "" || q.Resolution == "" {
		return "Unknown"
	}
	key := q.Source + "-" + q.Resolution
	for _, k := range AllQualities {
		if k == key {
			return key
		}
	}
	return "Unknown"
}

// QualityProfileItem is one row of a quality profile's user-editable
// weight table - one per AllQualities entry. Weight is the user-chosen
// preference (higher = more preferred); Allowed gates whether a release
// of this quality may be grabbed at all. Lives here, not internal/store,
// so internal/store can depend on releaseparse (as it already does for
// FileQuality) without a cycle.
type QualityProfileItem struct {
	Quality string `json:"quality"`
	Weight  int    `json:"weight"`
	Allowed bool   `json:"allowed"`
}

// Score returns quality's configured weight in items, and whether it's
// allowed at all - 0/false if quality's key isn't present in items
// (shouldn't happen once GetQualityProfileItems always returns the full
// catalog, but a caller passing a stale/partial list stays safe). This
// is the weighting primitive a future "pick best release" will call -
// not wired to anything yet.
func Score(items []QualityProfileItem, quality FileQuality) (weight int, allowed bool) {
	key := quality.Key()
	for _, it := range items {
		if it.Quality == key {
			return it.Weight, it.Allowed
		}
	}
	return 0, false
}
