package merge

import (
	"strconv"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/musicbrainz"
)

func adaptMusicBrainzArtist(a *musicbrainz.Artist) artistSource {
	return artistSource{
		Provider:       "musicbrainz",
		Name:           a.Name,
		Disambiguation: a.Disambiguation,
		ArtistType:     a.Type,
		Status:         a.Status(),
		ExternalIDs:    map[string]string{"musicbrainz": a.ID},
	}
}

// adaptMusicBrainzAlbum converts a release-group into an albumSource,
// extracting its compilation-series membership if the release-group was
// fetched with inc=series-rels and MusicBrainz has modeled it as part of
// one (e.g. "Now That's What I Call Music"). Relationship type
// release_group-series carries the series' position in its
// attribute-values map under the "number" key.
func adaptMusicBrainzAlbum(rg *musicbrainz.ReleaseGroup) albumSource {
	src := albumSource{
		Provider:       "musicbrainz",
		Title:          rg.Title,
		Disambiguation: rg.Disambiguation,
		AlbumType:      rg.PrimaryType,
		SecondaryTypes: rg.SecondaryTypes,
		ReleaseDate:    parseMusicBrainzDate(rg.FirstReleaseDate),
		ExternalIDs:    map[string]string{"musicbrainz": rg.ID},
	}
	src.Series = extractSeriesInfo(rg.Relations)
	return src
}

// adaptMusicBrainzRelease converts a release (a specific pressing/edition)
// into a releaseSource plus the flat list of tracks across all its media,
// each carrying its own artist credits - the mechanism that makes Various
// Artists compilations resolve correctly per-track (album/release artist
// can be "Various Artists" while each track points at its real performer).
func adaptMusicBrainzRelease(r *musicbrainz.Release) (releaseSource, []metadata.TrackSource) {
	src := releaseSource{
		Provider:       "musicbrainz",
		Title:          r.Title,
		Status:         r.Status,
		Disambiguation: r.Disambiguation,
		Country:        nonEmptyString(r.Country),
		ReleaseDate:    parseMusicBrainzDate(r.Date),
		ExternalIDs:    map[string]string{"musicbrainz": r.ID},
	}
	for _, l := range r.LabelInfo {
		if l.Label.Name != "" {
			src.Label = append(src.Label, l.Label.Name)
		}
	}

	var tracks []metadata.TrackSource
	for _, medium := range r.Media {
		src.TrackCount += medium.TrackCount
		for _, t := range medium.Tracks {
			track := metadata.TrackSource{
				Number:       t.Number,
				Title:        t.Title,
				DurationMs:   t.Length,
				MediumNumber: medium.Position,
			}
			for _, credit := range t.ArtistCredit {
				track.ArtistCredits = append(track.ArtistCredits, metadata.ArtistCreditRef{
					Name:                credit.Artist.Name,
					MusicBrainzArtistID: credit.Artist.ID,
				})
			}
			tracks = append(tracks, track)
		}
	}
	return src, tracks
}

// parseMusicBrainzDate parses MusicBrainz's date strings, which can be a
// full "YYYY-MM-DD", just "YYYY-MM", or just "YYYY" (partial dates are
// common for older/obscure releases). Returns nil rather than an error for
// anything that doesn't parse, since an approximate/missing date shouldn't
// block the rest of a release from syncing.
func parseMusicBrainzDate(s string) *time.Time {
	for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

func extractSeriesInfo(relations []musicbrainz.Relation) *metadata.CompilationSeriesInfo {
	for _, rel := range relations {
		if rel.TargetType != "series" || rel.Series == nil {
			continue
		}
		info := &metadata.CompilationSeriesInfo{
			Name:                rel.Series.Name,
			MusicBrainzSeriesID: rel.Series.ID,
		}
		if numStr, ok := rel.AttributeValues["number"]; ok {
			if n, err := strconv.Atoi(numStr); err == nil {
				info.SequenceNumber = n
			}
		}
		return info
	}
	return nil
}
