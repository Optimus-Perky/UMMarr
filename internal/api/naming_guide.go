package api

import (
	"database/sql"

	"github.com/Optimus-Perky/UMMarr/internal/pathbuilder"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// namingToken is one {Token} a naming box can use. Name must match, character
// for character, the key internal/store puts in that box's token map;
// naming_guide_test.go runs every one through the real resolver, so the guide
// can't list a token that silently comes out blank.
type namingToken struct {
	Name        string
	Description string
	Sample      string // value used to build the guide's example
	Numeric     bool   // accepts zero-padding, e.g. {season:00}
}

type namingField struct {
	FormName    string // the form value the media management form submits; unique across media types
	Label       string
	Folder      bool // folder templates split on "/" into subfolders; file templates strip it
	Placeholder string
	Note        string
	Extension   string // appended to a file example, as the importer appends the real one
	Tokens      []namingToken
	value       func(store.NamingConfig) string
	set         func(*store.NamingConfig, string)
}

type namingGroup struct {
	MediaType  string // naming_config.media_type
	Label      string
	RenameName string // form value for this media type's rename toggle
	RenameText string
	Fields     []namingField
}

var (
	tokMovieTitle    = namingToken{Name: "Movie Title", Description: "The movie's title.", Sample: "The Matrix"}
	tokMovieYear     = namingToken{Name: "Release Year", Description: "Year the movie came out. Blank if it isn't known.", Sample: "1999"}
	tokSeriesTitle   = namingToken{Name: "Series Title", Description: "The series' title.", Sample: "Breaking Bad"}
	tokSeason        = namingToken{Name: "season", Description: "Season number.", Sample: "1", Numeric: true}
	tokEpisode       = namingToken{Name: "episode", Description: "Episode number.", Sample: "5", Numeric: true}
	tokEpisodeTitle  = namingToken{Name: "Episode Title", Description: "The episode's title. Blank if it isn't known.", Sample: "Gray Matter"}
	tokQualityTitle  = namingToken{Name: "Quality Title", Description: "Source and resolution from the downloaded file's name, e.g. Bluray-1080p. Blank if the name doesn't say.", Sample: "Bluray-1080p"}
	tokVideoCodec    = namingToken{Name: "Video Codec", Description: "Video codec from the downloaded file's name, e.g. x265 or HEVC. Blank if the name doesn't say.", Sample: "x265"}
	tokReleaseGroup  = namingToken{Name: "Release Group", Description: "The group at the end of the downloaded file's name. Blank if there isn't one.", Sample: "GRP"}
	tokCustomFormats = namingToken{Name: "Custom Formats", Description: "Names of the custom formats the file matches that are set to be included when renaming (Settings → Custom Formats). Blank when none are.", Sample: "Remux Tier 01 x265"}
	tokArtistName    = namingToken{Name: "Artist Name", Description: "The artist's name.", Sample: "Daft Punk"}
	tokAlbumTitle    = namingToken{Name: "Album Title", Description: "The album's title.", Sample: "Discovery"}
	tokAlbumYear     = namingToken{Name: "Release Year", Description: "Year the album came out. Blank if it isn't known.", Sample: "2001"}
	tokSeriesName    = namingToken{Name: "Series Name", Description: "The compilation series the album belongs to.", Sample: "Now That's What I Call Music"}
	tokTrack         = namingToken{Name: "track", Description: "Track number as MusicBrainz lists it. Vinyl-style numbers such as A1 aren't padded.", Sample: "3", Numeric: true}
	tokMedium        = namingToken{Name: "medium", Description: "Disc number.", Sample: "1", Numeric: true}
	tokTrackTitle    = namingToken{Name: "Track Title", Description: "The track's title.", Sample: "Digital Love"}
)

func savedTemplate(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }

var namingGroups = []namingGroup{
	{MediaType: "movie", Label: "Movies", RenameName: "rename_movie", RenameText: "Rename Movies", Fields: []namingField{
		{
			FormName: "movie_folder_format", Label: "Movie folder", Folder: true,
			Placeholder: "{Movie Title} ({Release Year})",
			Tokens:      []namingToken{tokMovieTitle, tokMovieYear},
			value:       func(c store.NamingConfig) string { return c.MovieFolderFormat.String },
			set:         func(c *store.NamingConfig, v string) { c.MovieFolderFormat = savedTemplate(v) },
		},
		{
			FormName: "movie_file_format", Label: "Movie file", Extension: ".mkv",
			Placeholder: "{Movie Title} ({Release Year}) {Quality Title}",
			Tokens:      append([]namingToken{tokMovieTitle, tokMovieYear, tokQualityTitle, tokVideoCodec, tokReleaseGroup, tokCustomFormats}, mediaInfoVideoTokens...),
			value:       func(c store.NamingConfig) string { return c.MovieFileFormat.String },
			set:         func(c *store.NamingConfig, v string) { c.MovieFileFormat = savedTemplate(v) },
		},
	}},
	{MediaType: "series", Label: "TV Series", RenameName: "rename_series", RenameText: "Rename Episodes", Fields: []namingField{
		{
			FormName: "series_folder_format", Label: "Series folder", Folder: true,
			Placeholder: "{Series Title}",
			Tokens:      []namingToken{tokSeriesTitle},
			value:       func(c store.NamingConfig) string { return c.SeriesFolderFormat.String },
			set:         func(c *store.NamingConfig, v string) { c.SeriesFolderFormat = savedTemplate(v) },
		},
		{
			FormName: "season_folder_format", Label: "Season folder", Folder: true,
			Placeholder: "Season {season}",
			Note:        "Goes inside the series folder, for series that have season folders turned on (the default).",
			Tokens:      []namingToken{tokSeason},
			value:       func(c store.NamingConfig) string { return c.SeasonFolderFormat.String },
			set:         func(c *store.NamingConfig, v string) { c.SeasonFolderFormat = savedTemplate(v) },
		},
		{
			FormName: "episode_file_format", Label: "Episode file", Extension: ".mkv",
			Placeholder: "{Series Title} - S{season:00}E{episode:00} - {Episode Title}",
			Tokens:      append([]namingToken{tokSeriesTitle, tokSeason, tokEpisode, tokEpisodeTitle, tokQualityTitle, tokVideoCodec, tokReleaseGroup, tokCustomFormats}, mediaInfoVideoTokens...),
			value:       func(c store.NamingConfig) string { return c.EpisodeFileFormat.String },
			set:         func(c *store.NamingConfig, v string) { c.EpisodeFileFormat = savedTemplate(v) },
		},
	}},
	{MediaType: "music", Label: "Music", RenameName: "rename_music", RenameText: "Rename Tracks", Fields: []namingField{
		{
			FormName: "artist_folder_format", Label: "Artist folder", Folder: true,
			Placeholder: "{Artist Name}",
			Tokens:      []namingToken{tokArtistName},
			value:       func(c store.NamingConfig) string { return c.ArtistFolderFormat.String },
			set:         func(c *store.NamingConfig, v string) { c.ArtistFolderFormat = savedTemplate(v) },
		},
		{
			FormName: "album_folder_format", Label: "Album folder", Folder: true,
			Placeholder: "{Album Title} ({Release Year})",
			Note:        "Goes inside the artist folder. Various Artists albums go inside their series folder instead.",
			Tokens:      []namingToken{tokAlbumTitle, tokAlbumYear},
			value:       func(c store.NamingConfig) string { return c.AlbumFolderFormat.String },
			set:         func(c *store.NamingConfig, v string) { c.AlbumFolderFormat = savedTemplate(v) },
		},
		{
			FormName: "va_series_folder_format", Label: "Various Artists series folder", Folder: true,
			Placeholder: "Various Artists/{Series Name}",
			Note:        `Only used for Various Artists albums that belong to a compilation series. Any other Various Artists album goes in a plain "Various Artists" folder.`,
			Tokens:      []namingToken{tokSeriesName},
			value:       func(c store.NamingConfig) string { return c.VASeriesFolderFormat.String },
			set:         func(c *store.NamingConfig, v string) { c.VASeriesFolderFormat = savedTemplate(v) },
		},
		{
			FormName: "track_file_format", Label: "Track file", Extension: ".flac",
			Placeholder: "{Artist Name} - {Album Title} - {track:00} - {Track Title}",
			Tokens:      append([]namingToken{tokArtistName, tokAlbumTitle, tokTrack, tokMedium, tokTrackTitle}, mediaInfoAudioTokens...),
			value:       func(c store.NamingConfig) string { return c.TrackFileFormat.String },
			set:         func(c *store.NamingConfig, v string) { c.TrackFileFormat = savedTemplate(v) },
		},
	}},
}

type namingTokenView struct {
	Code        string
	PaddedCode  string
	Description string
}

type namingFieldView struct {
	FormName    string
	Label       string
	Placeholder string
	Note        string
	Folder      bool
	Value       string
	Example     string
	Error       string
	Tokens      []namingTokenView
}

type namingGroupView struct {
	MediaType  string
	Label      string
	RenameName string
	RenameText string
	Rename     bool
	Fields     []namingFieldView
}

func (d settingsPageData) namingConfig(mediaType string) store.NamingConfig {
	return map[string]store.NamingConfig{"movie": d.MovieNaming, "series": d.SeriesNaming, "music": d.MusicNaming}[mediaType]
}

// NamingGuide builds the naming boxes and the guide describing them from
// namingGroups, so a box's label and the guide's entry for it can't drift
// apart. Examples use the saved (or just submitted) character options.
func (d settingsPageData) NamingGuide() []namingGroupView {
	opts := d.Media.PathOptions()
	groups := make([]namingGroupView, 0, len(namingGroups))
	for _, g := range namingGroups {
		config := d.namingConfig(g.MediaType)
		gv := namingGroupView{MediaType: g.MediaType, Label: g.Label, RenameName: g.RenameName, RenameText: g.RenameText, Rename: config.RenameFiles}
		for _, f := range g.Fields {
			value := f.value(config)
			fv := namingFieldView{
				FormName: f.FormName, Label: f.Label, Placeholder: f.Placeholder, Note: f.Note,
				Folder: f.Folder, Value: value, Example: f.example(value, opts), Error: d.Errors[f.FormName],
			}
			for _, t := range f.Tokens {
				tv := namingTokenView{Code: "{" + t.Name + "}", Description: t.Description}
				if t.Numeric {
					tv.PaddedCode = "{" + t.Name + ":00}"
				}
				fv.Tokens = append(fv.Tokens, tv)
			}
			gv.Fields = append(gv.Fields, fv)
		}
		groups = append(groups, gv)
	}
	return groups
}

// CharacterRule describes, for the guide, what happens to characters Windows
// can't use in a name under the current settings.
func (d settingsPageData) CharacterRule() string {
	if !d.Media.ReplaceIllegalCharacters {
		return `These characters are removed because Windows can't use them in names: < > : " / \ | ? *`
	}
	colon := map[string]string{
		string(pathbuilder.ColonDash):           `a colon becomes "-"`,
		string(pathbuilder.ColonSpaceDash):      `a colon becomes " -"`,
		string(pathbuilder.ColonSpaceDashSpace): `a colon becomes " - "`,
		string(pathbuilder.ColonSmart):          `": " becomes " - " and any other colon becomes "-"`,
	}[d.Media.ColonReplacement]
	if colon == "" {
		colon = "a colon is removed"
	}
	return `Characters Windows can't use in names are replaced: \ and / become +, ? becomes !, * becomes -, < > " | are removed, and ` + colon + "."
}

// example resolves template the same way internal/store does for this kind of
// box, using each token's sample value.
func (f namingField) example(template string, opts pathbuilder.Options) string {
	tokens := make(map[string]string, len(f.Tokens))
	for _, t := range f.Tokens {
		tokens[t.Name] = t.Sample
	}
	if f.Folder {
		return pathbuilder.JoinSegments(pathbuilder.ResolveTemplatePath(template, tokens, opts)...)
	}
	name := pathbuilder.SanitizeSegment(pathbuilder.ResolveTemplate(template, tokens), opts)
	if name == "" {
		return ""
	}
	return name + f.Extension
}

// The {MediaInfo ...} tokens are read from the file itself with FFprobe, so
// they're blank when Analyze Video Files or Analyze Audio Files is off.
var (
	mediaInfoVideoTokens = []namingToken{
		{Name: "MediaInfo Simple", Description: "Video and audio codec read from the file.", Sample: "x264 DTS"},
		{Name: "MediaInfo Full", Description: "Video and audio codec, plus audio languages when there's more than English.", Sample: "x264 DTS [EN+DE]"},
		{Name: "MediaInfo VideoCodec", Description: "Video codec read from the file.", Sample: "x265"},
		{Name: "MediaInfo VideoBitDepth", Description: "Video bit depth.", Sample: "10"},
		{Name: "MediaInfo VideoDynamicRange", Description: "HDR when the picture is HDR, otherwise blank.", Sample: "HDR"},
		{Name: "MediaInfo VideoDynamicRangeType", Description: "The kind of HDR: DV, HDR10, HLG or a combination.", Sample: "DV HDR10"},
		{Name: "MediaInfo AudioCodec", Description: "Codec of the main audio track.", Sample: "TrueHD Atmos"},
		{Name: "MediaInfo AudioChannels", Description: "Channels of the main audio track.", Sample: "7.1"},
		{Name: "MediaInfo AudioLanguages", Description: "Audio languages, blank when English is the only one.", Sample: "[EN+DE]"},
		{Name: "MediaInfo AudioLanguagesAll", Description: "Every audio language.", Sample: "[EN]"},
		{Name: "MediaInfo SubtitleLanguages", Description: "Subtitle languages.", Sample: "[EN+FR]"},
	}
	mediaInfoAudioTokens = []namingToken{
		{Name: "MediaInfo AudioCodec", Description: "Codec read from the file.", Sample: "FLAC"},
		{Name: "MediaInfo AudioBitRate", Description: "Bitrate.", Sample: "320 kbps"},
		{Name: "MediaInfo AudioBitsPerSample", Description: "Bit depth of lossless audio.", Sample: "24"},
		{Name: "MediaInfo AudioSampleRate", Description: "Sample rate in kHz.", Sample: "44.1"},
		{Name: "MediaInfo AudioChannels", Description: "Channels.", Sample: "2.0"},
	}
)
