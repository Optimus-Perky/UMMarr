package merge

import (
	"strconv"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
)

func adaptTMDBMovie(m *tmdb.Movie) movieSource {
	src := movieSource{
		Provider:      "tmdb",
		Title:         m.Title,
		OriginalTitle: m.OriginalTitle,
		Status:        m.Status,
		Overview:      m.Overview,
		Runtime:       m.Runtime,
		ReleaseDate:   tmdb.ParseReleaseDate(m.ReleaseDate),
		Genres:        genreNames(m.Genres),
		Images:        posterImages(m.PosterPath, m.BackdropPath),
		Ratings:       map[string]float64{"tmdb": m.VoteAverage},
		ExternalIDs:   map[string]string{"tmdb": strconv.Itoa(m.ID)},
	}
	if year, err := strconv.Atoi(firstFour(m.ReleaseDate)); err == nil {
		src.Year = year
	}
	if m.ExternalIDs != nil && m.ExternalIDs.IMDbID != "" {
		src.ExternalIDs["imdb"] = m.ExternalIDs.IMDbID
	}
	if len(m.ProductionCompanies) > 0 {
		src.Studio = m.ProductionCompanies[0].Name
	}
	if m.BelongsToCollection != nil {
		src.CollectionTitle = m.BelongsToCollection.Name
	}
	// Not populated: PhysicalRelease/DigitalRelease/Certification - TMDB's
	// basic /movie/{id} response has none of these; they require the
	// separate /movie/{id}/release_dates endpoint (per-country release
	// type/certification data), not called here.
	return src
}

func adaptTMDBSeries(s *tmdb.Series) seriesSource {
	src := seriesSource{
		Provider:         "tmdb",
		Title:            s.Name,
		Status:           s.Status,
		Overview:         s.Overview,
		OriginalLanguage: s.OriginalLanguage,
		FirstAired:       tmdb.ParseReleaseDate(s.FirstAirDate),
		LastAired:        tmdb.ParseReleaseDate(s.LastAirDate),
		Genres:           genreNames(s.Genres),
		Images:           posterImages(s.PosterPath, ""),
		Ratings:          map[string]float64{"tmdb": s.VoteAverage},
		ExternalIDs:      map[string]string{"tmdb": strconv.Itoa(s.ID)},
	}
	if year, err := strconv.Atoi(firstFour(s.FirstAirDate)); err == nil {
		src.Year = year
	}
	if s.ExternalIDs != nil && s.ExternalIDs.IMDbID != "" {
		src.ExternalIDs["imdb"] = s.ExternalIDs.IMDbID
	}
	if len(s.Networks) > 0 {
		src.Network = s.Networks[0].Name
	}
	if len(s.EpisodeRunTime) > 0 {
		src.Runtime = s.EpisodeRunTime[0]
	}
	for _, season := range s.Seasons {
		src.SeasonNumbers = append(src.SeasonNumbers, season.SeasonNumber)
	}
	// Not populated: AirTime/SeriesType/Certification - none of these are
	// in TMDB's basic /tv/{id} response.
	return src
}

func adaptTMDBSeason(seasonNumber int, season *tmdb.Season) seasonSource {
	src := seasonSource{Provider: "tmdb", SeasonNumber: seasonNumber}
	if images := posterImages(season.PosterPath); len(images) > 0 {
		src.Poster = images[0]
	}
	for _, ep := range season.Episodes {
		src.Episodes = append(src.Episodes, episodeSource{
			Provider:      "tmdb",
			EpisodeNumber: ep.EpisodeNumber,
			Title:         ep.Name,
			Overview:      ep.Overview,
			AirDate:       tmdb.ParseReleaseDate(ep.AirDate),
			Runtime:       ep.Runtime,
		})
	}
	return src
}

func genreNames(genres []tmdb.Genre) []string {
	names := make([]string, 0, len(genres))
	for _, g := range genres {
		names = append(names, g.Name)
	}
	return names
}

func posterImages(paths ...string) []string {
	const imageBase = "https://image.tmdb.org/t/p/original"
	images := make([]string, 0, len(paths))
	for _, p := range paths {
		if p != "" {
			images = append(images, imageBase+p)
		}
	}
	return images
}

func firstFour(s string) string {
	if len(s) < 4 {
		return ""
	}
	return s[:4]
}
