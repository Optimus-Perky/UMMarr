package api

import (
	"net/http"
	"strconv"

	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/titleutil"
)

// Detail pages live at title addresses like /movies/the-fast-and-the-
// furious-2001 and /tv/breaking-bad, as in Radarr and Sonarr. The numeric
// addresses everything else uses (/tv/3/refresh and so on) still work, and a
// numeric detail address redirects to the title one.

func redirectKeepingQuery(w http.ResponseWriter, r *http.Request, path string) {
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, path, http.StatusMovedPermanently)
}

// movieFromRef resolves the {id} segment of a movie page, a slug or a
// number. ok is false when the response has already been written (a
// redirect, 404 or error).
func (h *handler) movieFromRef(w http.ResponseWriter, r *http.Request) (store.MovieDetail, bool) {
	ref := r.PathValue("id")
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		var found bool
		if id, found, err = store.FindMovieIDBySlug(r.Context(), h.deps.DB, ref); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return store.MovieDetail{}, false
		} else if !found {
			http.NotFound(w, r)
			return store.MovieDetail{}, false
		}
	}
	movie, found, err := store.GetMovieDetail(r.Context(), h.deps.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return store.MovieDetail{}, false
	}
	if !found {
		http.NotFound(w, r)
		return store.MovieDetail{}, false
	}
	if slug := titleutil.SlugWithYear(movie.Title, movie.Year.Int64); ref != slug {
		redirectKeepingQuery(w, r, "/movies/"+slug)
		return store.MovieDetail{}, false
	}
	return movie, true
}

// seriesFromRef is movieFromRef for a series page.
func (h *handler) seriesFromRef(w http.ResponseWriter, r *http.Request) (store.SeriesDetail, bool) {
	ref := r.PathValue("id")
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil {
		var found bool
		if id, found, err = store.FindSeriesIDBySlug(r.Context(), h.deps.DB, ref); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return store.SeriesDetail{}, false
		} else if !found {
			http.NotFound(w, r)
			return store.SeriesDetail{}, false
		}
	}
	series, found, err := store.GetSeriesDetail(r.Context(), h.deps.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return store.SeriesDetail{}, false
	}
	if !found {
		http.NotFound(w, r)
		return store.SeriesDetail{}, false
	}
	if slug := titleutil.Slug(series.Title); ref != slug {
		redirectKeepingQuery(w, r, "/tv/"+slug)
		return store.SeriesDetail{}, false
	}
	return series, true
}

// albumFromRef resolves an album page's address: /music/albums/{artist}/{album}
// by slugs, or the numeric /music/albums/{id}, which redirects to the slugs.
func (h *handler) albumFromRef(w http.ResponseWriter, r *http.Request) (store.AlbumDetail, bool) {
	var id int64
	if albumSlug := r.PathValue("album"); albumSlug != "" {
		var found bool
		var err error
		if id, found, err = store.FindAlbumIDBySlugs(r.Context(), h.deps.DB, r.PathValue("artist"), albumSlug); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return store.AlbumDetail{}, false
		} else if !found {
			http.NotFound(w, r)
			return store.AlbumDetail{}, false
		}
	} else {
		var err error
		if id, err = strconv.ParseInt(r.PathValue("id"), 10, 64); err != nil {
			http.NotFound(w, r)
			return store.AlbumDetail{}, false
		}
	}
	album, found, err := store.GetAlbumDetail(r.Context(), h.deps.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return store.AlbumDetail{}, false
	}
	if !found {
		http.NotFound(w, r)
		return store.AlbumDetail{}, false
	}
	artistSlug, albumSlug := titleutil.Slug(album.ArtistName), titleutil.Slug(album.Title)
	if r.PathValue("artist") != artistSlug || r.PathValue("album") != albumSlug {
		redirectKeepingQuery(w, r, "/music/albums/"+artistSlug+"/"+albumSlug)
		return store.AlbumDetail{}, false
	}
	return album, true
}
