package api

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Optimus-Perky/UMMarr/internal/decision"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/titleutil"
)

// The library grids offer Radarr's and Sonarr's Poster Options: each card
// carries every optional line and the browser shows the ones the user
// ticked (static/poster-options.js).

type movieCard struct {
	CutoffUnmet bool // has a file below the profile's cutoff
	store.MovieSummary
	InCinemas, DigitalRelease, PhysicalRelease string
	ReleaseDate                                string // from the minimum availability
	TMDbRating, IMDbRating, TomatoRating       string
	Progress                                   int    // percent of the bar filled
	ProgressClass                              string // done, missing or unreleased
	ProgressText                               string

	// Sort and filter keys for the library toolbar (static/library-grid.js).
	SortTitle                           string
	Letter                              string // A-Z jump bar: first letter of SortTitle, or #
	AddedUnix, CinemasUnix, DigitalUnix int64
	PhysicalUnix                        int64
	Available                           bool
	TMDbValue, IMDbValue, TomatoValue   float64

	// For the Overview and Table views.
	SizeHuman, AddedText, AvailabilityLabel string
}

type seriesCard struct {
	CutoffUnmet int // episode files below the profile's cutoff
	store.SeriesSummary
	Progress      int
	ProgressClass string // done, partial, missing or unreleased
	ProgressText  string
	SortTitle     string
	Letter        string
	AddedUnix     int64
	Missing       int // monitored aired episodes without a file
	SizeHuman     string
	AddedText     string
}

func cardDate(t sql.NullTime) string {
	if !t.Valid {
		return "-"
	}
	return t.Time.Format("2 Jan 2006")
}

func cardRating(ratings map[string]float64, provider string) string {
	v, ok := ratings[provider]
	if !ok || v <= 0 {
		return "-"
	}
	switch provider {
	case "imdb":
		return fmt.Sprintf("%.1f", v)
	case "tmdb":
		if v <= 10 {
			return fmt.Sprintf("%d%%", int(v*10+0.5))
		}
	}
	return fmt.Sprintf("%d%%", int(v+0.5))
}

func movieCards(movies []store.MovieSummary, now time.Time, profiles decision.Profiles) []movieCard {
	cards := make([]movieCard, 0, len(movies))
	for _, m := range movies {
		c := movieCard{
			MovieSummary: m, InCinemas: cardDate(m.InCinemas), DigitalRelease: cardDate(m.DigitalRelease), PhysicalRelease: cardDate(m.PhysicalRelease),
			TMDbRating: cardRating(m.Ratings, "tmdb"), IMDbRating: cardRating(m.Ratings, "imdb"), TomatoRating: cardRating(m.Ratings, "rotten_tomatoes"),
			ReleaseDate: "-",
		}
		c.SortTitle, c.Letter = sortKeys(m.Title)
		c.AddedUnix, c.CinemasUnix, c.DigitalUnix, c.PhysicalUnix = m.Added.Unix(), unixOr(m.InCinemas), unixOr(m.DigitalRelease), unixOr(m.PhysicalRelease)
		c.TMDbValue, c.IMDbValue, c.TomatoValue = m.Ratings["tmdb"], m.Ratings["imdb"], m.Ratings["rotten_tomatoes"]
		c.SizeHuman, c.AddedText, c.AvailabilityLabel = humanizeBytes(m.SizeOnDisk), m.Added.Format("2 Jan 2006"), availabilityLabel(m.MinimumAvailability)
		available := false
		if date, always, ok := decision.MovieAvailableFrom(store.WantedMovie{
			MinimumAvailability: m.MinimumAvailability, InCinemas: m.InCinemas, PhysicalRelease: m.PhysicalRelease, DigitalRelease: m.DigitalRelease,
		}); ok {
			if always {
				c.ReleaseDate = "Any time"
				available = true
			} else {
				c.ReleaseDate = date.Format("2 Jan 2006")
				available = !date.After(now)
			}
		}
		c.Available = available
		c.CutoffUnmet = m.HasFile && profiles.CutoffUnmet(m.QualityProfileID, m.FileQuality)
		switch {
		case m.HasFile:
			c.Progress, c.ProgressClass, c.ProgressText = 100, "done", "Downloaded"
		case available:
			c.ProgressClass, c.ProgressText = "missing", "Missing"
		default:
			c.ProgressClass, c.ProgressText = "unreleased", "Unreleased"
		}
		cards = append(cards, c)
	}
	return cards
}

func seriesCards(series []store.SeriesSummary, profiles decision.Profiles, qualities map[int64][]releaseparse.FileQuality) []seriesCard {
	cards := make([]seriesCard, 0, len(series))
	for _, s := range series {
		files := s.EpisodeFileCount
		if files > s.EpisodeCount {
			files = s.EpisodeCount
		}
		c := seriesCard{SeriesSummary: s, ProgressText: fmt.Sprintf("%d / %d", files, s.EpisodeCount), AddedUnix: s.Added.Unix(), Missing: s.EpisodeCount - files}
		c.SortTitle, c.Letter = sortKeys(s.Title)
		c.SizeHuman, c.AddedText = humanizeBytes(s.SizeOnDisk), s.Added.Format("2 Jan 2006")
		for _, q := range qualities[s.ID] {
			if profiles.CutoffUnmet(s.QualityProfileID, q) {
				c.CutoffUnmet++
			}
		}
		switch {
		case s.EpisodeCount == 0:
			c.ProgressClass = "unreleased"
		case files >= s.EpisodeCount:
			c.Progress, c.ProgressClass = 100, "done"
		case files > 0:
			c.Progress, c.ProgressClass = files*100/s.EpisodeCount, "partial"
		default:
			c.ProgressClass = "missing"
		}
		cards = append(cards, c)
	}
	return cards
}

func unixOr(t sql.NullTime) int64 {
	if !t.Valid {
		return 0
	}
	return t.Time.Unix()
}

// sortKeys is how a title sorts: a leading The, A or An ignored, as in
// Radarr, and the letter for the A-Z bar ("#" when it starts with a digit).
func sortKeys(title string) (sortTitle, letter string) {
	sortTitle = strings.ToLower(titleutil.SortTitle(title))
	letter = "#"
	if r, _ := utf8.DecodeRuneInString(sortTitle); unicode.IsLetter(r) {
		letter = strings.ToUpper(string(r))
	}
	return sortTitle, letter
}
