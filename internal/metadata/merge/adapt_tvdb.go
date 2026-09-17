package merge

import (
	"strconv"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tvdb"
)

func parseTVDBDate(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &t
}

func adaptTVDBSeries(s *tvdb.Series, episodes []tvdb.Episode) (seriesSource, []seasonSource) {
	src := seriesSource{
		Provider:    "tvdb",
		Title:       s.Name,
		Status:      s.Status.Name,
		Overview:    s.Overview,
		Network:     s.OriginalNetwork.Name,
		Runtime:     s.AverageRuntime,
		FirstAired:  parseTVDBDate(s.FirstAired),
		LastAired:   parseTVDBDate(s.LastAired),
		Images:      nonEmptyStrings(s.Image),
		ExternalIDs: map[string]string{"tvdb": strconv.Itoa(s.ID)},
	}
	for _, g := range s.Genres {
		if g.Name != "" {
			src.Genres = append(src.Genres, g.Name)
		}
	}
	if year, err := strconv.Atoi(firstFour(s.FirstAired)); err == nil {
		src.Year = year
	}
	for _, id := range s.RemoteIDs {
		switch strings.ToLower(id.SourceName) {
		case "imdb":
			src.ExternalIDs["imdb"] = id.ID
		case "themoviedb.com", "themoviedb", "tmdb":
			src.ExternalIDs["tmdb"] = id.ID
		}
	}
	seasons := groupTVDBEpisodesBySeason(episodes)
	for _, season := range seasons {
		src.SeasonNumbers = append(src.SeasonNumbers, season.SeasonNumber)
	}
	return src, seasons
}

func groupTVDBEpisodesBySeason(episodes []tvdb.Episode) []seasonSource {
	bySeasonNumber := map[int]*seasonSource{}
	for _, ep := range episodes {
		season, ok := bySeasonNumber[ep.SeasonNumber]
		if !ok {
			season = &seasonSource{Provider: "tvdb", SeasonNumber: ep.SeasonNumber}
			bySeasonNumber[ep.SeasonNumber] = season
		}
		season.Episodes = append(season.Episodes, episodeSource{
			Provider:      "tvdb",
			EpisodeNumber: ep.Number,
			Title:         ep.Name,
			Overview:      ep.Overview,
			AirDate:       parseTVDBDate(ep.Aired),
			Runtime:       ep.Runtime,
		})
	}
	seasons := make([]seasonSource, 0, len(bySeasonNumber))
	for _, sn := range sortedIntKeys(bySeasonNumber) {
		seasons = append(seasons, *bySeasonNumber[sn])
	}
	return seasons
}
