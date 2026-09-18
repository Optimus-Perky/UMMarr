package mediainfo

import (
	"strconv"
	"strings"
)

// What a music file says about itself. Every container spells these
// differently - FLAC/Vorbis uses MUSICBRAINZ_TRACKID, MP4 uses
// "MusicBrainz Track Id", MP3 puts the same names in ID3v2 TXXX frames -
// but FFprobe hands them all back as one flat map, so normalising the key
// (lowercase, no spaces, underscores or hyphens) covers every format with
// one table.
//
// These are what MusicBrainz Picard writes, and they are worth far more
// than the filename: a file that carries its own MusicBrainz ids doesn't
// have to be matched to a track by guessing at its position in the folder.

// AudioTags is the subset of a music file's tags UMMarr uses.
type AudioTags struct {
	Title       string `json:"title,omitempty"`
	Artist      string `json:"artist,omitempty"`
	AlbumArtist string `json:"albumArtist,omitempty"`
	Album       string `json:"album,omitempty"`
	Date        string `json:"date,omitempty"`

	TrackNumber int `json:"trackNumber,omitempty"`
	TrackTotal  int `json:"trackTotal,omitempty"`
	DiscNumber  int `json:"discNumber,omitempty"`
	DiscTotal   int `json:"discTotal,omitempty"`

	// RecordingMBID is Picard's MUSICBRAINZ_TRACKID, which despite the name
	// identifies the *recording* - the same performance wherever it was
	// released. TrackMBID (MUSICBRAINZ_RELEASETRACKID) identifies this
	// track on this particular release, which is what tells two editions of
	// one album apart.
	RecordingMBID    string `json:"recordingMbid,omitempty"`
	TrackMBID        string `json:"trackMbid,omitempty"`
	ReleaseMBID      string `json:"releaseMbid,omitempty"`
	ReleaseGroupMBID string `json:"releaseGroupMbid,omitempty"`
	ArtistMBID       string `json:"artistMbid,omitempty"`
	AlbumArtistMBID  string `json:"albumArtistMbid,omitempty"`
}

// HasMusicBrainz reports whether the file carries anything UMMarr can match
// on exactly, rather than by position.
func (t AudioTags) HasMusicBrainz() bool {
	return t.RecordingMBID != "" || t.TrackMBID != "" || t.ReleaseMBID != "" || t.ReleaseGroupMBID != ""
}

// Empty reports whether nothing useful was tagged at all.
func (t AudioTags) Empty() bool { return t == AudioTags{} }

// normalizeTagKey lowercases and strips the separators the containers
// disagree about: "MusicBrainz Album Id", "MUSICBRAINZ_ALBUMID" and
// "musicbrainz-album-id" all become "musicbrainzalbumid".
func normalizeTagKey(key string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(key) {
		switch r {
		case ' ', '_', '-', '.':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// splitNumber reads a tag that may be "7" or "7/12", returning both parts.
func splitNumber(value string) (number, total int) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0
	}
	first, rest, found := strings.Cut(value, "/")
	number, _ = strconv.Atoi(strings.TrimSpace(first))
	if found {
		total, _ = strconv.Atoi(strings.TrimSpace(rest))
	}
	return number, total
}

// ParseAudioTags turns FFprobe's format tags into AudioTags. Unknown tags
// are ignored, and a tag already set is never overwritten by a later one,
// so the first spelling found wins rather than the last.
func ParseAudioTags(tags map[string]string) AudioTags {
	var t AudioTags
	setString := func(field *string, value string) {
		if *field == "" {
			*field = strings.TrimSpace(value)
		}
	}
	setNumber := func(field *int, value int) {
		if *field == 0 {
			*field = value
		}
	}
	for key, value := range tags {
		if strings.TrimSpace(value) == "" {
			continue
		}
		switch normalizeTagKey(key) {
		case "title":
			setString(&t.Title, value)
		case "artist":
			setString(&t.Artist, value)
		case "albumartist", "album artist", "performer":
			setString(&t.AlbumArtist, value)
		case "album":
			setString(&t.Album, value)
		case "date", "originaldate", "year", "originalyear":
			setString(&t.Date, value)
		case "track", "tracknumber":
			n, total := splitNumber(value)
			setNumber(&t.TrackNumber, n)
			setNumber(&t.TrackTotal, total)
		case "totaltracks", "tracktotal":
			n, _ := splitNumber(value)
			setNumber(&t.TrackTotal, n)
		case "disc", "discnumber":
			n, total := splitNumber(value)
			setNumber(&t.DiscNumber, n)
			setNumber(&t.DiscTotal, total)
		case "totaldiscs", "disctotal":
			n, _ := splitNumber(value)
			setNumber(&t.DiscTotal, n)
		case "musicbrainztrackid":
			setString(&t.RecordingMBID, value)
		case "musicbrainzreleasetrackid":
			setString(&t.TrackMBID, value)
		case "musicbrainzalbumid":
			setString(&t.ReleaseMBID, value)
		case "musicbrainzreleasegroupid":
			setString(&t.ReleaseGroupMBID, value)
		case "musicbrainzartistid":
			setString(&t.ArtistMBID, value)
		case "musicbrainzalbumartistid":
			setString(&t.AlbumArtistMBID, value)
		}
	}
	return t
}
