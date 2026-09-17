package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// The scan report: every folder a library scan couldn't match, kept after
// the scan so it can be matched by hand, ignored or dismissed.

type unmatchedRow struct {
	store.UnmatchedFolder
	LastSeenText string
	CanMatch     bool   // a folder that can be matched to a provider entry
	Provider     string // TMDB or MusicBrainz
	TypeLabel    string
}

type scanReportPageData struct {
	Active, PageTitle, Notice string
	Rows                      []unmatchedRow
	ShowIgnored               bool
	Ignored                   int
}

var scanReportTypes = map[string][2]string{
	"movie":  {"Movie", "TMDB"},
	"series": {"Series", "TMDB"},
	"music":  {"Artist", "MusicBrainz"},
}

// toUnmatchedRow decides what the report offers for a folder. An album
// folder ("Artist / Album") can't be matched on its own - its artist has to
// come in first - so it's listed for dismissing only.
func toUnmatchedRow(f store.UnmatchedFolder) unmatchedRow {
	labels := scanReportTypes[f.Kind]
	row := unmatchedRow{UnmatchedFolder: f, LastSeenText: f.LastSeen.Local().Format("2 Jan 15:04"), TypeLabel: labels[0], Provider: labels[1]}
	row.CanMatch = f.Reason == store.UnmatchedNoMatch && !strings.Contains(f.Name, " / ")
	return row
}

// ScanReport lists what the last scans couldn't take.
func (h *handler) ScanReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	showIgnored := r.URL.Query().Get("ignored") == "1"
	list, err := store.ListUnmatchedFolders(ctx, h.deps.DB, showIgnored)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := make([]unmatchedRow, 0, len(list))
	for _, f := range list {
		rows = append(rows, toUnmatchedRow(f))
	}
	all, _ := store.ListUnmatchedFolders(ctx, h.deps.DB, true)
	notice := ""
	switch {
	case r.URL.Query().Get("matched") != "":
		notice = fmt.Sprintf("%s is in the library now, pointed at its folder.", r.URL.Query().Get("matched"))
	case r.URL.Query().Get("dismissed") != "":
		notice = "Dismissed. It comes back if the next scan still can't match it."
	}
	h.renderPage(w, "scan_report", scanReportPageData{
		Active: "settings", PageTitle: "Scan report", Notice: notice,
		Rows: rows, ShowIgnored: showIgnored, Ignored: len(all) - len(rows),
	})
}

type scanMatchData struct {
	Row                 unmatchedRow
	Query               string
	Results             fixMatchResults
	Notice              string
	SearchURL, ApplyURL string
}

// ScanReportMatch is the Match dialog: search the provider for what this
// folder really is.
func (h *handler) ScanReportMatch(w http.ResponseWriter, r *http.Request) {
	row, ok := h.unmatchedFromPath(w, r)
	if !ok {
		return
	}
	data := scanMatchData{
		Row: row, Query: strings.TrimSpace(r.URL.Query().Get("q")),
		SearchURL: fmt.Sprintf("/library/scan-report/%d/match", row.ID),
		ApplyURL:  fmt.Sprintf("/library/scan-report/%d/match", row.ID),
	}
	if data.Query == "" {
		data.Query = searchTermForFolder(row)
	}
	results, err := h.searchProviderFor(r, row, data.Query)
	switch {
	case err != nil:
		data.Notice = err.Error()
	case len(results) == 0:
		data.Notice = "Nothing found on " + row.Provider + " for that search."
	}
	data.Results = fixMatchResults{Results: results, Query: data.Query, ApplyURL: data.ApplyURL}
	if err != nil {
		data.Results.Err = err.Error()
	}
	if r.Header.Get("HX-Request") == "true" && r.URL.Query().Get("q") != "" {
		h.renderPartial(w, "fix_match_results", data.Results)
		return
	}
	h.renderPartial(w, "scan_match", data)
}

// searchTermForFolder is the folder name with the year and junk trimmed, as
// a first search.
func searchTermForFolder(row unmatchedRow) string {
	name := row.Name
	if i := strings.Index(name, " ("); i > 0 {
		name = name[:i]
	}
	return strings.TrimSpace(name)
}

func (h *handler) searchProviderFor(r *http.Request, row unmatchedRow, query string) ([]fixMatchResult, error) {
	ctx := r.Context()
	switch row.Kind {
	case "movie":
		if h.deps.TMDB == nil {
			return nil, fmt.Errorf("TMDB isn't configured, so folders can't be matched")
		}
		results, err := h.deps.TMDB.SearchMovies(ctx, query, 0)
		if err != nil {
			return nil, err
		}
		out := make([]fixMatchResult, 0, len(results))
		for _, m := range results {
			out = append(out, fixMatchResult{ID: strconv.Itoa(m.ID), Title: m.Title, Sub: releaseYear(m.ReleaseDate), Initial: initialOf(m.Title)})
		}
		return out, nil
	case "series":
		if h.deps.TMDB == nil {
			return nil, fmt.Errorf("TMDB isn't configured, so folders can't be matched")
		}
		results, err := h.deps.TMDB.SearchSeries(ctx, query)
		if err != nil {
			return nil, err
		}
		out := make([]fixMatchResult, 0, len(results))
		for _, s := range results {
			out = append(out, fixMatchResult{ID: strconv.Itoa(s.ID), Title: s.Name, Sub: releaseYear(s.FirstAirDate), Initial: initialOf(s.Name)})
		}
		return out, nil
	case "music":
		if h.deps.MusicBrainz == nil {
			return nil, fmt.Errorf("MusicBrainz isn't configured, so folders can't be matched")
		}
		results, err := h.deps.MusicBrainz.SearchArtist(ctx, query)
		if err != nil {
			return nil, err
		}
		out := make([]fixMatchResult, 0, len(results))
		for _, a := range results {
			out = append(out, fixMatchResult{ID: a.ID, Title: a.Name, Initial: initialOf(a.Name)})
		}
		return out, nil
	}
	return nil, fmt.Errorf("a %s folder can't be matched here", row.Kind)
}

// ScanReportMatchApply adds the chosen provider entry, points it at the
// folder the scan found and scans that folder's files in.
func (h *handler) ScanReportMatchApply(w http.ResponseWriter, r *http.Request) {
	row, ok := h.unmatchedFromPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	providerID := strings.TrimSpace(r.FormValue("id"))
	if providerID == "" {
		renderInlineError(w, "Pick an entry first.")
		return
	}
	ctx := r.Context()
	profileID, err := defaultQualityProfileID(ctx, h.deps.DB)
	if err != nil {
		renderInlineError(w, err.Error())
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = row.Name
	}
	switch row.Kind {
	case "movie":
		tmdbID, err := strconv.Atoi(providerID)
		if err != nil || h.deps.Movies == nil {
			renderInlineError(w, "That movie can't be added here.")
			return
		}
		movieID, err := h.deps.Movies.AddByTMDBID(ctx, tmdbID, row.RootFolderID, profileID)
		if err != nil {
			renderInlineError(w, err.Error())
			return
		}
		if err := store.SetMoviePath(ctx, h.deps.DB, movieID, row.Path); err != nil {
			renderInlineError(w, err.Error())
			return
		}
		if h.deps.Import != nil {
			h.deps.Import.ScanMovie(ctx, movieID)
		}
	case "series":
		tmdbID, err := strconv.Atoi(providerID)
		if err != nil || h.deps.Series == nil {
			renderInlineError(w, "That series can't be added here.")
			return
		}
		seriesID, err := h.deps.Series.AddByTMDBID(ctx, tmdbID, row.RootFolderID, profileID)
		if err != nil {
			renderInlineError(w, err.Error())
			return
		}
		if err := store.SetSeriesPath(ctx, h.deps.DB, seriesID, row.Path); err != nil {
			renderInlineError(w, err.Error())
			return
		}
		if h.deps.Import != nil {
			h.deps.Import.ScanSeries(ctx, seriesID)
		}
	case "music":
		if h.deps.Music == nil {
			renderInlineError(w, "That artist can't be added here.")
			return
		}
		artistID, err := h.deps.Music.AddArtistByMBID(ctx, providerID, row.RootFolderID, profileID)
		if err != nil {
			renderInlineError(w, err.Error())
			return
		}
		if err := store.SetArtistPath(ctx, h.deps.DB, artistID, row.Path); err != nil {
			renderInlineError(w, err.Error())
			return
		}
	default:
		renderInlineError(w, "That folder can't be matched here.")
		return
	}
	if err := store.ResolveUnmatchedFolder(ctx, h.deps.DB, row.Path); err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", "/library/scan-report?matched="+urlValue(title))
	w.WriteHeader(http.StatusOK)
}

// ScanReportIgnore hides a folder from the report, or shows it again.
func (h *handler) ScanReportIgnore(w http.ResponseWriter, r *http.Request) {
	row, ok := h.unmatchedFromPath(w, r)
	if !ok {
		return
	}
	if err := store.IgnoreUnmatchedFolder(r.Context(), h.deps.DB, row.ID, !row.Ignored); err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", "/library/scan-report"+ignoredQuery(r))
	w.WriteHeader(http.StatusOK)
}

// ScanReportDismiss takes a folder off the report until a scan finds it
// unmatched again.
func (h *handler) ScanReportDismiss(w http.ResponseWriter, r *http.Request) {
	row, ok := h.unmatchedFromPath(w, r)
	if !ok {
		return
	}
	if err := store.ResolveUnmatchedFolder(r.Context(), h.deps.DB, row.Path); err != nil {
		renderInlineError(w, err.Error())
		return
	}
	w.Header().Set("HX-Redirect", "/library/scan-report?dismissed=1")
	w.WriteHeader(http.StatusOK)
}

func ignoredQuery(r *http.Request) string {
	if r.URL.Query().Get("ignored") == "1" {
		return "?ignored=1"
	}
	return ""
}

func (h *handler) unmatchedFromPath(w http.ResponseWriter, r *http.Request) (unmatchedRow, bool) {
	id, ok := pathID(w, r, "id", "folder")
	if !ok {
		return unmatchedRow{}, false
	}
	f, found, err := store.GetUnmatchedFolder(r.Context(), h.deps.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return unmatchedRow{}, false
	}
	if !found {
		http.NotFound(w, r)
		return unmatchedRow{}, false
	}
	return toUnmatchedRow(f), true
}

// defaultQualityProfileID is the profile a matched folder is added with:
// the one the Add forms preselect, or the first there is.
func defaultQualityProfileID(ctx context.Context, db store.Queryer) (int64, error) {
	profiles, err := store.ListQualityProfiles(ctx, db)
	if err != nil {
		return 0, err
	}
	if len(profiles) == 0 {
		return 0, errors.New("add a quality profile under Settings → Profiles first")
	}
	for _, p := range profiles {
		if p.IsDefault {
			return p.ID, nil
		}
	}
	return profiles[0].ID, nil
}

func initialOf(title string) string {
	for _, r := range title {
		return strings.ToUpper(string(r))
	}
	return "?"
}

func releaseYear(date string) string {
	if len(date) >= 4 {
		return date[:4]
	}
	return ""
}

func urlValue(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "&", "%26"), " ", "%20")
}
