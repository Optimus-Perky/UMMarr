package merge

import (
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/omdb"
)

func adaptOMDbMovie(r *omdb.Response) movieSource {
	src := movieSource{
		Provider:    "omdb",
		Title:       r.Title,
		Overview:    r.Plot,
		Genres:      splitCommaList(r.Genre),
		Images:      nonEmptyStrings(r.Poster),
		Ratings:     omdbRatings(r.Ratings),
		ExternalIDs: map[string]string{"omdb": r.ImdbID},
	}
	if r.ImdbID != "" {
		src.ExternalIDs["imdb"] = r.ImdbID
	}
	if year, err := strconv.Atoi(firstFour(r.Year)); err == nil {
		src.Year = year
	}
	src.Runtime = parseRuntimeMinutes(r.Runtime)
	return src
}

// omdbRatings maps OMDb's named-source ratings array into a flat
// key->value map, converting each source's own scale (percent, out of 10,
// out of 100) into a plain float so it sits alongside TMDB's vote_average
// without implying a shared scale - callers/UI decide how to display each.
func omdbRatings(ratings []omdb.Rating) map[string]float64 {
	out := map[string]float64{}
	for _, r := range ratings {
		key := ""
		switch r.Source {
		case "Internet Movie Database":
			key = "imdb"
		case "Rotten Tomatoes":
			key = "rotten_tomatoes"
		case "Metacritic":
			key = "metacritic"
		default:
			continue
		}
		if v, ok := parseRatingValue(r.Value); ok {
			out[key] = v
		}
	}
	return out
}

// parseRatingValue handles OMDb's three value shapes: "87%", "7.4/10",
// "74/100".
func parseRatingValue(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimSuffix(v, "%")
	if idx := strings.Index(v, "/"); idx != -1 {
		v = v[:idx]
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func parseRuntimeMinutes(s string) int {
	s = strings.TrimSuffix(strings.TrimSpace(s), " min")
	minutes, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return minutes
}

func splitCommaList(s string) []string {
	if s == "" || s == "N/A" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func nonEmptyStrings(vals ...string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if v != "" && v != "N/A" {
			out = append(out, v)
		}
	}
	return out
}
