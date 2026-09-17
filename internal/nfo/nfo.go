// Package nfo writes Kodi (XBMC) / Emby metadata beside library files:
// movie and tvshow .nfo files, episode .nfo files, poster and fanart images
// - Radarr's and Sonarr's Metadata providers.
package nfo

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// Movie is Kodi's movie.nfo.
type Movie struct {
	XMLName       xml.Name   `xml:"movie"`
	Title         string     `xml:"title"`
	OriginalTitle string     `xml:"originaltitle,omitempty"`
	SortTitle     string     `xml:"sorttitle,omitempty"`
	Year          int64      `xml:"year,omitempty"`
	Plot          string     `xml:"plot,omitempty"`
	Runtime       int64      `xml:"runtime,omitempty"`
	MPAA          string     `xml:"mpaa,omitempty"`
	Studio        string     `xml:"studio,omitempty"`
	Genres        []string   `xml:"genre"`
	Premiered     string     `xml:"premiered,omitempty"`
	Set           *Set       `xml:"set,omitempty"`
	Ratings       *Ratings   `xml:"ratings,omitempty"`
	UniqueIDs     []UniqueID `xml:"uniqueid"`
	Thumb         string     `xml:"thumb,omitempty"`
	Fanart        *Fanart    `xml:"fanart,omitempty"`
}

// Set is a movie collection.
type Set struct {
	Name string `xml:"name"`
}

// Ratings holds each provider's rating.
type Ratings struct {
	Rating []Rating `xml:"rating"`
}

// Rating is one provider's rating out of 10.
type Rating struct {
	Name    string  `xml:"name,attr"`
	Max     int     `xml:"max,attr"`
	Default bool    `xml:"default,attr"`
	Value   float64 `xml:"value"`
}

// UniqueID is a provider id.
type UniqueID struct {
	Type    string `xml:"type,attr"`
	Default bool   `xml:"default,attr,omitempty"`
	Value   string `xml:",chardata"`
}

// Fanart wraps backdrop thumbs.
type Fanart struct {
	Thumb []string `xml:"thumb"`
}

// TVShow is Kodi's tvshow.nfo.
type TVShow struct {
	XMLName   xml.Name   `xml:"tvshow"`
	Title     string     `xml:"title"`
	SortTitle string     `xml:"sorttitle,omitempty"`
	Plot      string     `xml:"plot,omitempty"`
	Premiered string     `xml:"premiered,omitempty"`
	Status    string     `xml:"status,omitempty"`
	Studio    string     `xml:"studio,omitempty"`
	MPAA      string     `xml:"mpaa,omitempty"`
	Runtime   int64      `xml:"runtime,omitempty"`
	Genres    []string   `xml:"genre"`
	Ratings   *Ratings   `xml:"ratings,omitempty"`
	UniqueIDs []UniqueID `xml:"uniqueid"`
	Thumb     string     `xml:"thumb,omitempty"`
}

// Episode is Kodi's episodedetails .nfo beside an episode file.
type Episode struct {
	XMLName   xml.Name   `xml:"episodedetails"`
	Title     string     `xml:"title"`
	ShowTitle string     `xml:"showtitle,omitempty"`
	Season    int        `xml:"season"`
	Episode   int        `xml:"episode"`
	Aired     string     `xml:"aired,omitempty"`
	Plot      string     `xml:"plot,omitempty"`
	Runtime   int64      `xml:"runtime,omitempty"`
	UniqueIDs []UniqueID `xml:"uniqueid"`
}

// Album is Kodi's album.nfo.
type Album struct {
	XMLName   xml.Name   `xml:"album"`
	Title     string     `xml:"title"`
	Artist    string     `xml:"artist"`
	Year      int64      `xml:"year,omitempty"`
	Review    string     `xml:"review,omitempty"`
	Type      string     `xml:"type,omitempty"`
	Genres    []string   `xml:"genre"`
	UniqueIDs []UniqueID `xml:"uniqueid"`
	Thumb     string     `xml:"thumb,omitempty"`
	ReleaseMB string     `xml:"musicbrainzreleasegroupid,omitempty"`
}

// Marshal renders a document with the XML header Kodi expects.
func Marshal(v any) ([]byte, error) {
	body, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}

func ratings(m map[string]float64) *Ratings {
	if len(m) == 0 {
		return nil
	}
	var out Ratings
	for _, provider := range []string{"tmdb", "imdb", "rotten_tomatoes", "tvmaze"} {
		v, ok := m[provider]
		if !ok || v <= 0 {
			continue
		}
		max := 10
		if provider == "rotten_tomatoes" {
			max = 100
		}
		out.Rating = append(out.Rating, Rating{Name: provider, Max: max, Default: len(out.Rating) == 0, Value: v})
	}
	if len(out.Rating) == 0 {
		return nil
	}
	return &out
}

// Writer writes metadata for library items according to the settings.
type Writer struct {
	DB   *sql.DB
	HTTP *http.Client
	// Permissions applies the Media management chmod/chown to what's written.
	Permissions func(ctx context.Context) importer.Permissions
}

func (w *Writer) settings(ctx context.Context) (store.MetadataSettings, bool) {
	if w == nil || w.DB == nil {
		return store.MetadataSettings{}, false
	}
	s, err := store.GetMetadataSettings(ctx, w.DB)
	if err != nil || !s.Any() {
		return s, false
	}
	return s, true
}

func (w *Writer) client() *http.Client {
	if w.HTTP != nil {
		return w.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (w *Writer) perms(ctx context.Context) importer.Permissions {
	if w.Permissions == nil {
		return importer.Permissions{}
	}
	return w.Permissions(ctx)
}

// writeFile writes data to path (only when the folder exists) with the
// configured permissions. An identical file is left alone.
func (w *Writer) writeFile(ctx context.Context, path string, data []byte) error {
	dir := filepath.Dir(path)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil // the item's folder doesn't exist yet; nothing to write beside
	}
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	p := w.perms(ctx)
	if p.Explicit {
		os.Chmod(path, p.FolderMode.Perm()&^0o111)
	}
	if p.SetGroup {
		os.Lchown(path, -1, p.Group)
	}
	return nil
}

// fetchImage downloads url to path unless it's already there.
func (w *Writer) fetchImage(ctx context.Context, url, path string) error {
	if url == "" {
		return nil
	}
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return nil
	}
	dir := filepath.Dir(path)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := w.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("image %s: HTTP %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return err
	}
	return w.writeFile(ctx, path, data)
}

func imageAt(images []string, i int) string {
	if i < len(images) {
		return images[i]
	}
	return ""
}

func nfoName(relativePath string) string {
	return strings.TrimSuffix(relativePath, filepath.Ext(relativePath)) + ".nfo"
}

// WriteMovie writes movieID's .nfo (beside its file, else movie.nfo) and
// images into its folder.
func (w *Writer) WriteMovie(ctx context.Context, movieID int64) error {
	s, on := w.settings(ctx)
	if !on || (!(s.MovieNFO && s.NFO()) && !s.MovieImages) {
		return nil
	}
	d, found, err := store.GetMovieDetail(ctx, w.DB, movieID)
	if err != nil || !found || !d.Path.Valid {
		return err
	}
	images := store.ItemImages(ctx, w.DB, "movie", movieID)
	if s.MovieNFO && s.NFO() {
		m := Movie{Title: d.Title, Year: d.Year.Int64, Plot: d.Overview.String, Runtime: d.Runtime.Int64, MPAA: d.Certification.String, Studio: d.Studio.String, Genres: d.Genres, Ratings: ratings(d.Ratings)}
		if d.CollectionTitle.Valid && d.CollectionTitle.String != "" {
			m.Set = &Set{Name: d.CollectionTitle.String}
		}
		if d.InCinemas.Valid {
			m.Premiered = d.InCinemas.Time.Format("2006-01-02")
		}
		var metadataID int64
		w.DB.QueryRowContext(ctx, `SELECT movie_metadata_id FROM movies WHERE id = ?`, movieID).Scan(&metadataID)
		for i, provider := range []string{"tmdb", "imdb"} {
			if v, ok, _ := store.GetExternalID(ctx, w.DB, "movie", metadataID, provider); ok {
				m.UniqueIDs = append(m.UniqueIDs, UniqueID{Type: provider, Default: i == 0, Value: v})
			}
		}
		m.Thumb = imageAt(images, 0)
		if fanart := imageAt(images, 1); fanart != "" {
			m.Fanart = &Fanart{Thumb: []string{fanart}}
		}
		data, err := Marshal(m)
		if err != nil {
			return err
		}
		name := "movie.nfo"
		if d.File != nil {
			name = nfoName(d.File.RelativePath)
		}
		if err := w.writeFile(ctx, filepath.Join(d.Path.String, name), data); err != nil {
			return err
		}
	}
	if s.MovieImages {
		if err := w.fetchImage(ctx, imageAt(images, 0), filepath.Join(d.Path.String, "poster.jpg")); err != nil {
			return err
		}
		if err := w.fetchImage(ctx, imageAt(images, 1), filepath.Join(d.Path.String, "fanart.jpg")); err != nil {
			return err
		}
	}
	return nil
}

// WriteSeries writes tvshow.nfo and images into the series folder, and an
// .nfo beside every episode file.
func (w *Writer) WriteSeries(ctx context.Context, seriesID int64) error {
	s, on := w.settings(ctx)
	if !on {
		return nil
	}
	d, found, err := store.GetSeriesDetail(ctx, w.DB, seriesID)
	if err != nil || !found || !d.Path.Valid {
		return err
	}
	images := store.ItemImages(ctx, w.DB, "series", seriesID)
	if s.SeriesNFO && s.NFO() {
		show := TVShow{Title: d.Title, Plot: d.Overview.String, Status: d.Status.String, Studio: d.Network.String, MPAA: d.Certification.String, Runtime: d.Runtime.Int64, Genres: d.Genres, Ratings: ratings(d.Ratings), Thumb: imageAt(images, 0)}
		if d.FirstAired.Valid {
			show.Premiered = d.FirstAired.Time.Format("2006-01-02")
		}
		for i, provider := range []string{"tmdb", "tvdb", "imdb"} {
			if v, ok, _ := store.GetExternalID(ctx, w.DB, "series", d.MetadataID, provider); ok {
				show.UniqueIDs = append(show.UniqueIDs, UniqueID{Type: provider, Default: i == 0, Value: v})
			}
		}
		data, err := Marshal(show)
		if err != nil {
			return err
		}
		if err := w.writeFile(ctx, filepath.Join(d.Path.String, "tvshow.nfo"), data); err != nil {
			return err
		}
	}
	if s.SeriesImages {
		if err := w.fetchImage(ctx, imageAt(images, 0), filepath.Join(d.Path.String, "poster.jpg")); err != nil {
			return err
		}
	}
	if s.SeriesImages {
		if err := w.writeSeasons(ctx, s, seriesID, d.Path.String); err != nil {
			return err
		}
	}
	if s.EpisodeNFO && s.NFO() {
		refs, err := store.ListEpisodeFileRefs(ctx, w.DB, seriesID)
		if err != nil {
			return err
		}
		done := map[string]bool{}
		for _, ref := range refs {
			if done[ref.RelativePath] {
				continue
			}
			done[ref.RelativePath] = true
			if err := w.WriteEpisode(ctx, ref.EpisodeID); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteEpisode writes the .nfo beside an episode's file.
func (w *Writer) WriteEpisode(ctx context.Context, episodeID int64) error {
	s, on := w.settings(ctx)
	if !on || !s.EpisodeNFO || !s.NFO() {
		return nil
	}
	info, found, err := store.GetEpisodeInfo(ctx, w.DB, episodeID)
	if err != nil || !found || info.File == nil || !info.SeriesPath.Valid {
		return err
	}
	e := Episode{Title: info.Title.String, ShowTitle: info.SeriesTitle, Season: info.SeasonNumber, Episode: info.EpisodeNumber, Plot: info.Overview.String, Runtime: info.Runtime.Int64}
	if info.AirDate.Valid {
		e.Aired = info.AirDate.Time.Format("2006-01-02")
	}
	for _, provider := range []string{"tmdb", "tvdb", "tvmaze"} {
		if v, ok, _ := store.GetExternalID(ctx, w.DB, "episode", episodeID, provider); ok {
			e.UniqueIDs = append(e.UniqueIDs, UniqueID{Type: provider, Default: len(e.UniqueIDs) == 0, Value: v})
		}
	}
	data, err := Marshal(e)
	if err != nil {
		return err
	}
	return w.writeFile(ctx, filepath.Join(info.SeriesPath.String, nfoName(info.File.RelativePath)), data)
}

// WriteAlbum writes album.nfo and the cover into the album folder.
func (w *Writer) WriteAlbum(ctx context.Context, albumID int64) error {
	s, on := w.settings(ctx)
	if !on || (!(s.AlbumNFO && s.NFO()) && !s.AlbumImages) {
		return nil
	}
	d, found, err := store.GetAlbumDetail(ctx, w.DB, albumID)
	if err != nil || !found || !d.Path.Valid {
		return err
	}
	images := store.ItemImages(ctx, w.DB, "album", albumID)
	if s.AlbumNFO && s.NFO() {
		a := Album{Title: d.Title, Artist: d.ArtistName, Year: d.Year.Int64, Review: d.Overview.String, Type: d.AlbumType, Genres: d.Genres, Thumb: imageAt(images, 0)}
		if v, ok, _ := store.GetExternalID(ctx, w.DB, "album", albumID, "musicbrainz"); ok {
			a.ReleaseMB = v
			a.UniqueIDs = append(a.UniqueIDs, UniqueID{Type: "musicbrainz", Default: true, Value: v})
		}
		data, err := Marshal(a)
		if err != nil {
			return err
		}
		if err := w.writeFile(ctx, filepath.Join(d.Path.String, "album.nfo"), data); err != nil {
			return err
		}
	}
	if d.ArtistID.Valid {
		if err := w.WriteArtist(ctx, d.ArtistID.Int64); err != nil {
			return err
		}
	}
	if s.AlbumImages {
		if s.Enabled {
			if err := w.fetchImage(ctx, imageAt(images, 0), filepath.Join(d.Path.String, "cover.jpg")); err != nil {
				return err
			}
		}
		if s.Jellyfin || s.Plex {
			if err := w.fetchImage(ctx, imageAt(images, 0), filepath.Join(d.Path.String, "folder.jpg")); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteAll writes metadata for the whole library; the first error of each
// kind is reported, the rest carry on.
func (w *Writer) WriteAll(ctx context.Context) error {
	if _, on := w.settings(ctx); !on {
		return nil
	}
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if movies, err := store.ListMovies(ctx, w.DB); err == nil {
		for _, m := range movies {
			note(w.WriteMovie(ctx, m.ID))
		}
	}
	if series, err := store.ListSeries(ctx, w.DB); err == nil {
		for _, s := range series {
			note(w.WriteSeries(ctx, s.ID))
		}
	}
	if albums, err := store.ListAlbums(ctx, w.DB); err == nil {
		for _, a := range albums {
			note(w.WriteAlbum(ctx, a.ID))
		}
	}
	return firstErr
}

// RemoveEpisodeNFO deletes the .nfo that belonged to a renamed episode file.
func RemoveEpisodeNFO(seriesPath, oldRelativePath string) {
	os.Remove(filepath.Join(seriesPath, nfoName(oldRelativePath)))
}

var _ = strconv.Itoa

// Season is Kodi/Jellyfin's season.nfo.
type Season struct {
	XMLName      xml.Name `xml:"season"`
	SeasonNumber int      `xml:"seasonnumber"`
	Title        string   `xml:"title,omitempty"`
}

// Artist is Kodi/Jellyfin's artist.nfo.
type Artist struct {
	XMLName   xml.Name   `xml:"artist"`
	Name      string     `xml:"name"`
	Biography string     `xml:"biography,omitempty"`
	Genres    []string   `xml:"genre"`
	UniqueIDs []UniqueID `xml:"uniqueid"`
	Thumb     string     `xml:"thumb,omitempty"`
}

// writeSeasons writes each season's poster: seasonNN-poster.jpg in the
// series folder (Kodi, Jellyfin), poster.jpg in the season's own folder
// (Plex), and Jellyfin's season.nfo there too.
func (w *Writer) writeSeasons(ctx context.Context, s store.MetadataSettings, seriesID int64, seriesPath string) error {
	seasons, err := store.ListSeasonsForSeries(ctx, w.DB, seriesID)
	if err != nil {
		return err
	}
	refs, _ := store.ListEpisodeFileRefs(ctx, w.DB, seriesID)
	seasonDirs := map[int]string{}
	for _, ref := range refs {
		if dir := filepath.Dir(filepath.Join(seriesPath, ref.RelativePath)); dir != filepath.Clean(seriesPath) {
			seasonDirs[ref.SeasonNumber] = dir
		}
	}
	for _, se := range seasons {
		if se.Poster != "" && (s.Enabled || s.Jellyfin) {
			name := fmt.Sprintf("season%02d-poster.jpg", se.SeasonNumber)
			if se.SeasonNumber == 0 {
				name = "season-specials-poster.jpg"
			}
			if err := w.fetchImage(ctx, se.Poster, filepath.Join(seriesPath, name)); err != nil {
				return err
			}
		}
		dir, ok := seasonDirs[se.SeasonNumber]
		if !ok {
			continue
		}
		if se.Poster != "" && s.Plex {
			if err := w.fetchImage(ctx, se.Poster, filepath.Join(dir, "poster.jpg")); err != nil {
				return err
			}
		}
		if s.Jellyfin && s.SeriesNFO {
			data, err := Marshal(Season{SeasonNumber: se.SeasonNumber})
			if err != nil {
				return err
			}
			if err := w.writeFile(ctx, filepath.Join(dir, "season.nfo"), data); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteArtist writes artist.nfo (Kodi, Jellyfin) and folder.jpg (Jellyfin,
// Plex) into the artist's folder.
func (w *Writer) WriteArtist(ctx context.Context, artistID int64) error {
	s, on := w.settings(ctx)
	if !on || (!(s.AlbumNFO && s.NFO()) && !s.AlbumImages) {
		return nil
	}
	var name, path, overview string
	if err := w.DB.QueryRowContext(ctx, `SELECT am.name, COALESCE(a.path, ''), COALESCE(am.overview, '') FROM artists a JOIN artist_metadata am ON am.id = a.artist_metadata_id WHERE a.id = ?`, artistID).Scan(&name, &path, &overview); err != nil || path == "" {
		return nil
	}
	images := store.ItemImages(ctx, w.DB, "artist", artistID)
	if s.AlbumNFO && s.NFO() {
		a := Artist{Name: name, Biography: overview, Thumb: imageAt(images, 0)}
		var metadataID int64
		w.DB.QueryRowContext(ctx, `SELECT artist_metadata_id FROM artists WHERE id = ?`, artistID).Scan(&metadataID)
		if v, ok, _ := store.GetExternalID(ctx, w.DB, "artist", metadataID, "musicbrainz"); ok {
			a.UniqueIDs = append(a.UniqueIDs, UniqueID{Type: "musicbrainz", Default: true, Value: v})
		}
		data, err := Marshal(a)
		if err != nil {
			return err
		}
		if err := w.writeFile(ctx, filepath.Join(path, "artist.nfo"), data); err != nil {
			return err
		}
	}
	if s.AlbumImages && (s.Jellyfin || s.Plex) {
		if err := w.fetchImage(ctx, imageAt(images, 0), filepath.Join(path, "folder.jpg")); err != nil {
			return err
		}
	}
	return nil
}
