package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// The interactive search opens in a dialog like Sonarr's: a heading naming
// what's being searched for, then Details, History and Search tabs.

type detailField struct {
	Label, Value string
}

type historyView struct {
	When      string
	Release   string
	Indexer   string
	Status    string
	GrabbedBy string
}

type fileView struct {
	Path, SizeHuman, Languages, Quality, Codec, Group, Added string
	MediaInfo                                                mediaInfoView
}

type dialogInfo struct {
	ActiveTab string // "details", "history" or "search" (the default)
	Overview  string
	Files     []fileView
	// SearchURL, when set, means the Search tab holds a button that loads the
	// search into the dialog instead of results already fetched.
	SearchURL string
	Heading   string
	Details   []detailField
	History   []historyView
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

func dateOr(t interface{ IsZero() bool }, valid bool, format string) string {
	if !valid {
		return "-"
	}
	if tt, ok := t.(time.Time); ok {
		return tt.Format(format)
	}
	return "-"
}

func historyViews(grabs []store.Grab) []historyView {
	out := make([]historyView, 0, len(grabs))
	for _, g := range grabs {
		out = append(out, historyView{
			When: g.Added.Local().Format("2 Jan 2006 15:04"), Release: g.ReleaseTitle, Indexer: g.Indexer,
			Status: strings.ReplaceAll(g.Status, "_", " "), GrabbedBy: g.GrabbedBy,
		})
	}
	return out
}

func (h *handler) movieDialog(ctx context.Context, m store.WantedMovie) dialogInfo {
	heading := m.Title
	if m.Year > 0 {
		heading = fmt.Sprintf("%s (%d)", m.Title, m.Year)
	}
	status := "Missing"
	if m.HasFile {
		status = "Downloaded"
	} else if m.Queued {
		status = "Downloading"
	}
	grabs, _ := store.ListGrabsForMovie(ctx, h.deps.DB, m.ID)
	return dialogInfo{
		Heading: heading,
		Details: []detailField{
			{"Status", status}, {"Monitored", yesNo(m.Monitored)},
			{"Minimum availability", m.MinimumAvailability},
			{"In cinemas", dateOr(m.InCinemas.Time, m.InCinemas.Valid, "2 Jan 2006")},
			{"Physical release", dateOr(m.PhysicalRelease.Time, m.PhysicalRelease.Valid, "2 Jan 2006")},
			{"Digital release", dateOr(m.DigitalRelease.Time, m.DigitalRelease.Valid, "2 Jan 2006")},
		},
		History: historyViews(grabs),
	}
}

func seriesCounts(eps []store.WantedEpisode) (total, withFile, monitored int) {
	for _, ep := range eps {
		total++
		if ep.HasFile {
			withFile++
		}
		if ep.Monitored {
			monitored++
		}
	}
	return
}

func (h *handler) seriesDialog(ctx context.Context, s store.WantedSeries, season *int) dialogInfo {
	eps := s.Episodes
	heading := s.Title
	if season != nil {
		eps = s.Season(*season)
		if *season == 0 {
			heading += " - Specials"
		} else {
			heading += fmt.Sprintf(" - Season %d", *season)
		}
	}
	total, withFile, monitored := seriesCounts(eps)
	grabs, _ := store.ListGrabsForSeries(ctx, h.deps.DB, s.ID, season, nil)
	return dialogInfo{
		Heading: heading,
		Details: []detailField{
			{"Monitored", yesNo(s.Monitored)}, {"Episodes", fmt.Sprint(total)},
			{"With files", fmt.Sprint(withFile)}, {"Monitored episodes", fmt.Sprint(monitored)},
		},
		History: historyViews(grabs),
	}
}

func (h *handler) albumDialog(ctx context.Context, a store.WantedAlbum) dialogInfo {
	grabs, _ := store.ListGrabsForAlbum(ctx, h.deps.DB, a.ID, nil)
	return dialogInfo{
		Heading: a.Artist + " - " + a.Title,
		Details: []detailField{
			{"Artist", a.Artist}, {"Release date", dateOr(a.ReleaseDate.Time, a.ReleaseDate.Valid, "2 Jan 2006")},
			{"Monitored", yesNo(a.Monitored)}, {"Tracks", fmt.Sprint(len(a.Tracks))}, {"With files", fmt.Sprint(a.FileCount())},
		},
		History: historyViews(grabs),
	}
}

func (h *handler) trackDialog(ctx context.Context, a store.WantedAlbum, t store.WantedTrack) dialogInfo {
	grabs, _ := store.ListGrabsForAlbum(ctx, h.deps.DB, a.ID, &t.ID)
	return dialogInfo{
		Heading: a.Artist + " - " + t.Title,
		Details: []detailField{
			{"Album", a.Title}, {"Track", t.Title}, {"Has file", yesNo(t.HasFile)},
		},
		History: historyViews(grabs),
	}
}

// flagNames lists a release's indexer flags by Radarr's names.
func flagNames(f newznab.Flags) []string {
	var out []string
	for _, n := range newznab.FlagNames {
		if f&n.Flag == n.Flag {
			out = append(out, n.Name)
		}
	}
	return out
}

// peersClass colours the Peers badge like Sonarr: none is red, a handful
// is orange, plenty is blue.
func peersClass(protocol string, seeders int) string {
	if protocol != newznab.ProtocolTorrent {
		return ""
	}
	switch {
	case seeders <= 0:
		return "peers-none"
	case seeders < 5:
		return "peers-few"
	}
	return "peers-ok"
}

// airsText is Sonarr's "Airs" line: "14 Jan 2008 at 21:00 on FOX".
func airsText(e store.EpisodeInfo) string {
	if !e.AirDate.Valid {
		return "-"
	}
	s := e.AirDate.Time.Format("2 Jan 2006")
	if e.AirTime.Valid && e.AirTime.String != "" {
		s += " at " + e.AirTime.String
	}
	if e.Network.Valid && e.Network.String != "" {
		s += " on " + e.Network.String
	}
	return s
}

// episodeInfoDialog builds the episode dialog from GetEpisodeInfo: heading,
// airing details, overview, file and history. queued marks a grab in progress.
func (h *handler) episodeInfoDialog(ctx context.Context, e store.EpisodeInfo, queued bool) dialogInfo {
	heading := fmt.Sprintf("%s - %dx%02d", e.SeriesTitle, e.SeasonNumber, e.EpisodeNumber)
	if e.Title.Valid && e.Title.String != "" {
		heading += " - " + e.Title.String
	}
	status := episodeStatus(e.File != nil, "", e.AirDate, time.Now())
	if queued && e.File == nil {
		status = "Downloading"
	}
	details := []detailField{
		{"Airs", airsText(e)},
		{"Episode", fmt.Sprintf("S%02dE%02d", e.SeasonNumber, e.EpisodeNumber)},
		{"Status", status}, {"Monitored", yesNo(e.Monitored)},
	}
	if e.QualityProfileName.Valid && e.QualityProfileName.String != "" {
		details = append(details, detailField{"Quality profile", e.QualityProfileName.String})
	}
	if e.Runtime.Valid && e.Runtime.Int64 > 0 {
		details = append(details, detailField{"Runtime", fmt.Sprintf("%d minutes", e.Runtime.Int64)})
	}
	var files []fileView
	if e.File != nil {
		path := e.File.RelativePath
		if e.SeriesPath.Valid {
			path = e.SeriesPath.String + "/" + path
		}
		files = append(files, fileView{
			Path: path, SizeHuman: humanizeBytes(e.File.Size.Int64), Languages: fileLanguages(e.File.MediaInfo, e.File.RelativePath),
			Quality: e.File.Quality.String(), Codec: e.File.Quality.Codec, Group: e.File.Quality.ReleaseGroup, Added: e.File.DateAdded.Local().Format("2 Jan 2006"), MediaInfo: newMediaInfoView(e.File.MediaInfo),
		})
	}
	grabs, _ := store.ListGrabsForSeries(ctx, h.deps.DB, e.SeriesID, &e.SeasonNumber, &e.EpisodeNumber)
	return dialogInfo{Heading: heading, Details: details, Overview: e.Overview.String, Files: files, History: historyViews(grabs)}
}

// episodeDialog is the search dialog's episode heading and details.
func (h *handler) episodeDialog(ctx context.Context, ep store.WantedEpisode) dialogInfo {
	info, found, err := store.GetEpisodeInfo(ctx, h.deps.DB, ep.ID)
	if err != nil || !found {
		return dialogInfo{Heading: fmt.Sprintf("S%02dE%02d", ep.SeasonNumber, ep.EpisodeNumber)}
	}
	return h.episodeInfoDialog(ctx, info, ep.Queued)
}
