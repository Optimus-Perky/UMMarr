package api

import (
	"net/http"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

type homeData struct {
	Active       string
	PageTitle    string
	Stats        homeStats
	RecentMovies []store.MovieSummary
	RecentSeries []store.SeriesSummary
	RecentAlbums []store.AlbumSummary
}

type homeStats struct {
	Movies, Series, Artists, Albums int
}

const recentLimit = 4

func (h *handler) Home(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	allMovies, err := store.ListMovies(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	allSeries, err := store.ListSeries(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	allArtists, err := store.ListArtists(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	allAlbums, err := store.ListAlbums(ctx, h.deps.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	recentMovies, err := store.ListRecentMovies(ctx, h.deps.DB, recentLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	recentSeries, err := store.ListRecentSeries(ctx, h.deps.DB, recentLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	recentAlbums, err := store.ListRecentAlbums(ctx, h.deps.DB, recentLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.renderPage(w, "home", homeData{
		Active:    "home",
		PageTitle: "Home",
		Stats: homeStats{
			Movies:  len(allMovies),
			Series:  len(allSeries),
			Artists: len(allArtists),
			Albums:  len(allAlbums),
		},
		RecentMovies: recentMovies,
		RecentSeries: recentSeries,
		RecentAlbums: recentAlbums,
	})
}
