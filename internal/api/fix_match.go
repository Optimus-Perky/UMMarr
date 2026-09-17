package api

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Fix match, as in Radarr and Sonarr: search the metadata provider from a
// dialog and point an item at the right title, keeping its folder, files,
// monitoring and profile.

type fixMatchData struct {
	Title, Noun, Provider string
	Query                 string
	SearchURL, ApplyURL   string
}

type fixMatchResult struct {
	ID, Title, Sub, Initial string
}

type fixMatchResults struct {
	Results  []fixMatchResult
	Err      string
	Query    string
	ApplyURL string
	Noun     string
}

func initial(s string) string {
	for _, r := range s {
		return string(r)
	}
	return "?"
}

func year(date string) string {
	if len(date) >= 4 {
		return date[:4]
	}
	return ""
}

// fixMatch serves both the dialog (no q) and its results (q present).
func (h *handler) fixMatch(w http.ResponseWriter, r *http.Request, id int64, data fixMatchData, search func(q string) ([]fixMatchResult, error)) {
	q := r.URL.Query()
	if !q.Has("q") {
		h.renderPartial(w, "fix_match", data)
		return
	}
	results := fixMatchResults{Query: q.Get("q"), ApplyURL: data.ApplyURL, Noun: data.Noun}
	if results.Query != "" {
		var err error
		if results.Results, err = search(results.Query); err != nil {
			results.Err = err.Error()
		}
	}
	h.renderPartial(w, "fix_match_results", results)
}

// applyFixMatch reports the outcome: a redirect to the corrected item, or
// the reason it couldn't be corrected in the dialog.
func applyFixMatch(w http.ResponseWriter, err error, redirect string) {
	if err != nil {
		msg := err.Error()
		if errors.Is(err, store.ErrAlreadyTracked) {
			msg = store.ErrAlreadyTracked.Error()
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<p class="notice" style="color:var(--warn)">Couldn't fix the match: %s</p>`, html.EscapeString(msg))
		return
	}
	w.Header().Set("HX-Redirect", redirect)
	w.WriteHeader(http.StatusOK)
}

func (h *handler) MovieFixMatch(w http.ResponseWriter, r *http.Request) {
	movieID, ok := pathID(w, r, "id", "movie")
	if !ok {
		return
	}
	detail, found, err := store.GetMovieDetail(r.Context(), h.deps.DB, movieID)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	base := fmt.Sprintf("/movies/%d/fix-match", movieID)
	h.fixMatch(w, r, movieID, fixMatchData{Title: detail.Title, Noun: "movie", Provider: "TMDB", Query: detail.Title, SearchURL: base, ApplyURL: base}, func(q string) ([]fixMatchResult, error) {
		if h.deps.TMDB == nil {
			return nil, errors.New("TMDB isn't configured")
		}
		found, err := h.deps.TMDB.SearchMovies(r.Context(), q, 0)
		if err != nil {
			return nil, err
		}
		out := make([]fixMatchResult, 0, len(found))
		for _, m := range found {
			out = append(out, fixMatchResult{ID: strconv.Itoa(m.ID), Title: m.Title, Sub: year(m.ReleaseDate), Initial: initial(m.Title)})
		}
		return out, nil
	})
}

func (h *handler) MovieFixMatchApply(w http.ResponseWriter, r *http.Request) {
	movieID, ok := pathID(w, r, "id", "movie")
	if !ok {
		return
	}
	tmdbID, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		http.Error(w, "invalid tmdb id", http.StatusBadRequest)
		return
	}
	if h.deps.Movies == nil {
		applyFixMatch(w, errors.New("TMDB isn't configured"), "")
		return
	}
	err = h.deps.Movies.FixMatch(r.Context(), movieID, tmdbID)
	if err == nil {
		h.recordItemEvent(r, store.EventMatched, "movie", movieID, 0, 0, fmt.Sprintf("Matched to TMDB %d", tmdbID), "interactive")
	}
	applyFixMatch(w, err, fmt.Sprintf("/movies/%d", movieID))
}

func (h *handler) SeriesFixMatch(w http.ResponseWriter, r *http.Request) {
	seriesID, ok := pathID(w, r, "id", "series")
	if !ok {
		return
	}
	detail, found, err := store.GetSeriesDetail(r.Context(), h.deps.DB, seriesID)
	if err != nil || !found {
		http.NotFound(w, r)
		return
	}
	base := fmt.Sprintf("/tv/%d/fix-match", seriesID)
	h.fixMatch(w, r, seriesID, fixMatchData{Title: detail.Title, Noun: "series", Provider: "TMDB", Query: detail.Title, SearchURL: base, ApplyURL: base}, func(q string) ([]fixMatchResult, error) {
		if h.deps.TMDB == nil {
			return nil, errors.New("TMDB isn't configured")
		}
		found, err := h.deps.TMDB.SearchSeries(r.Context(), q)
		if err != nil {
			return nil, err
		}
		out := make([]fixMatchResult, 0, len(found))
		for _, s := range found {
			out = append(out, fixMatchResult{ID: strconv.Itoa(s.ID), Title: s.Name, Sub: year(s.FirstAirDate), Initial: initial(s.Name)})
		}
		return out, nil
	})
}

func (h *handler) SeriesFixMatchApply(w http.ResponseWriter, r *http.Request) {
	seriesID, ok := pathID(w, r, "id", "series")
	if !ok {
		return
	}
	tmdbID, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		http.Error(w, "invalid tmdb id", http.StatusBadRequest)
		return
	}
	if h.deps.Series == nil {
		applyFixMatch(w, errors.New("TMDB isn't configured"), "")
		return
	}
	err = h.deps.Series.FixMatch(r.Context(), seriesID, tmdbID)
	if err == nil {
		h.recordItemEvent(r, store.EventMatched, "series", 0, seriesID, 0, fmt.Sprintf("Matched to TMDB %d", tmdbID), "interactive")
	}
	applyFixMatch(w, err, fmt.Sprintf("/tv/%d", seriesID))
}

func (h *handler) ArtistFixMatch(w http.ResponseWriter, r *http.Request) {
	artistID, ok := pathID(w, r, "id", "artist")
	if !ok {
		return
	}
	var name string
	if err := h.deps.DB.QueryRowContext(r.Context(), `SELECT am.name FROM artists a JOIN artist_metadata am ON am.id = a.artist_metadata_id WHERE a.id = ?`, artistID).Scan(&name); err != nil {
		http.NotFound(w, r)
		return
	}
	base := fmt.Sprintf("/music/artists/%d/fix-match", artistID)
	h.fixMatch(w, r, artistID, fixMatchData{Title: name, Noun: "artist", Provider: "MusicBrainz", Query: name, SearchURL: base, ApplyURL: base}, func(q string) ([]fixMatchResult, error) {
		if h.deps.MusicBrainz == nil {
			return nil, errors.New("MusicBrainz isn't configured")
		}
		found, err := h.deps.MusicBrainz.SearchArtist(r.Context(), q)
		if err != nil {
			return nil, err
		}
		out := make([]fixMatchResult, 0, len(found))
		for _, a := range found {
			sub := a.Type
			if a.Disambiguation != "" {
				sub = a.Disambiguation
			}
			out = append(out, fixMatchResult{ID: a.ID, Title: a.Name, Sub: sub, Initial: initial(a.Name)})
		}
		return out, nil
	})
}

func (h *handler) ArtistFixMatchApply(w http.ResponseWriter, r *http.Request) {
	artistID, ok := pathID(w, r, "id", "artist")
	if !ok {
		return
	}
	mbid := r.FormValue("id")
	if mbid == "" || h.deps.Music == nil {
		applyFixMatch(w, errors.New("MusicBrainz isn't configured"), "")
		return
	}
	applyFixMatch(w, h.deps.Music.FixMatchArtist(r.Context(), artistID, mbid), "/music")
}

func (h *handler) AlbumFixMatch(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	var title, artist string
	if err := h.deps.DB.QueryRowContext(r.Context(), `SELECT al.title, am.name FROM albums al JOIN artist_metadata am ON am.id = al.artist_metadata_id WHERE al.id = ?`, albumID).Scan(&title, &artist); err != nil {
		http.NotFound(w, r)
		return
	}
	base := fmt.Sprintf("/music/albums/%d/fix-match", albumID)
	h.fixMatch(w, r, albumID, fixMatchData{Title: artist + " - " + title, Noun: "album", Provider: "MusicBrainz", Query: artist + " " + title, SearchURL: base, ApplyURL: base}, func(q string) ([]fixMatchResult, error) {
		if h.deps.MusicBrainz == nil {
			return nil, errors.New("MusicBrainz isn't configured")
		}
		found, err := h.deps.MusicBrainz.SearchReleaseGroup(r.Context(), q)
		if err != nil {
			return nil, err
		}
		out := make([]fixMatchResult, 0, len(found))
		for _, rg := range found {
			sub := rg.PrimaryType
			if y := year(rg.FirstReleaseDate); y != "" {
				sub = y + " · " + sub
			}
			if rg.Disambiguation != "" {
				sub += " · " + rg.Disambiguation
			}
			out = append(out, fixMatchResult{ID: rg.ID, Title: rg.Title, Sub: sub, Initial: initial(rg.Title)})
		}
		return out, nil
	})
}

func (h *handler) AlbumFixMatchApply(w http.ResponseWriter, r *http.Request) {
	albumID, ok := pathID(w, r, "id", "album")
	if !ok {
		return
	}
	mbid := r.FormValue("id")
	if mbid == "" || h.deps.Music == nil {
		applyFixMatch(w, errors.New("MusicBrainz isn't configured"), "")
		return
	}
	err := h.deps.Music.FixMatchAlbum(r.Context(), albumID, mbid)
	if err == nil {
		h.recordItemEvent(r, store.EventMatched, "music", 0, 0, albumID, "Matched to MusicBrainz "+mbid, "interactive")
	}
	applyFixMatch(w, err, fmt.Sprintf("/music/albums/%d", albumID))
}
