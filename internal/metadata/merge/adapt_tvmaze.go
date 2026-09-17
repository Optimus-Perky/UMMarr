package merge

import (
	"strconv"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tvmaze"
)

func adaptTVMazeSeries(s *tvmaze.Show, episodes []tvmaze.Episode) (seriesSource, []seasonSource) {
	src := seriesSource{
		Provider:    "tvmaze",
		Title:       s.Name,
		Status:      s.Status,
		Overview:    stripHTML(s.Summary),
		Genres:      s.Genres,
		Images:      tvmazeImages(s.Image),
		FirstAired:  tvmaze.ParseAirdate(s.Premiered),
		LastAired:   tvmaze.ParseAirdate(s.Ended),
		ExternalIDs: map[string]string{"tvmaze": strconv.Itoa(s.ID)},
	}
	if year, err := strconv.Atoi(firstFour(s.Premiered)); err == nil {
		src.Year = year
	}
	if s.Externals.IMDb != nil && *s.Externals.IMDb != "" {
		src.ExternalIDs["imdb"] = *s.Externals.IMDb
	}
	if s.Externals.TheTVDB != nil {
		src.ExternalIDs["tvdb"] = strconv.Itoa(*s.Externals.TheTVDB)
	}

	seasons := groupTVMazeEpisodesBySeason(episodes)
	for _, season := range seasons {
		src.SeasonNumbers = append(src.SeasonNumbers, season.SeasonNumber)
	}
	return src, seasons
}

func groupTVMazeEpisodesBySeason(episodes []tvmaze.Episode) []seasonSource {
	bySeasonNumber := map[int]*seasonSource{}
	for _, ep := range episodes {
		season, ok := bySeasonNumber[ep.Season]
		if !ok {
			season = &seasonSource{Provider: "tvmaze", SeasonNumber: ep.Season}
			bySeasonNumber[ep.Season] = season
		}
		season.Episodes = append(season.Episodes, episodeSource{
			Provider:      "tvmaze",
			EpisodeNumber: ep.Number,
			Title:         ep.Name,
			Overview:      stripHTML(ep.Summary),
			AirDate:       tvmaze.ParseAirdate(ep.Airdate),
			Runtime:       ep.Runtime,
		})
	}
	seasons := make([]seasonSource, 0, len(bySeasonNumber))
	for _, sn := range sortedIntKeys(bySeasonNumber) {
		seasons = append(seasons, *bySeasonNumber[sn])
	}
	return seasons
}

func tvmazeImages(img *tvmaze.Image) []string {
	if img == nil {
		return nil
	}
	return nonEmptyStrings(img.Original, img.Medium)
}

// stripHTML does the bare minimum to make TVMaze's HTML-formatted summary
// fields (e.g. "<p>Some text.</p>") usable as plain overview text. A real
// HTML-to-text pass can replace this later if formatting fidelity ever
// matters; for metadata display, stripping tags is enough.
func stripHTML(s string) string {
	out := make([]byte, 0, len(s))
	inTag := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				out = append(out, s[i])
			}
		}
	}
	return string(out)
}
