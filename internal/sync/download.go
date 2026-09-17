package sync

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	gosync "sync"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/downloadclient"
	"github.com/Optimus-Perky/UMMarr/internal/downloadclient/deluge"
	"github.com/Optimus-Perky/UMMarr/internal/downloadclient/nzbget"
	"github.com/Optimus-Perky/UMMarr/internal/downloadclient/qbittorrent"
	"github.com/Optimus-Perky/UMMarr/internal/downloadclient/sabnzbd"
	"github.com/Optimus-Perky/UMMarr/internal/downloadclient/transmission"
	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// DownloadService grabs a chosen release to a download client and tracks it
// in the grabs table. Not the Add/Refresh shape - a grab is a one-shot
// action, not something to upsert idempotently by external id.
//
// Clients are built fresh from the saved download_clients rows on every
// call, and releases are fetched through the indexer they came from
// (Indexers), so saving Settings takes effect on the very next grab or
// poll, no restart needed. A release goes to the enabled client for its
// protocol with the lowest priority number, as in Sonarr.
type DownloadService struct {
	DB       *sql.DB
	Import   *ImportService  // nilable
	Indexers *IndexerService // fetches each release through the indexer it came from

	UserAgent string

	// Events records grabs and their outcomes (nil records nothing).
	Events *Events

	// Redownload searches for a replacement after a failed download
	// (SearchService.Redownload); nil never searches.
	Redownload func(ctx context.Context, g store.Grab)

	// Bootstrap* are the .env-var fallback values - a Deluge used only when
	// no download client has been saved via Settings.
	BootstrapDelugeBaseURL  string
	BootstrapDelugePassword string

	// PollGracePeriod is how long a grab can go without "reporting in"
	// before RefreshQueue actively re-asks its client about it.
	// Zero/negative falls back to defaultPollGracePeriod.
	PollGracePeriod time.Duration

	// PerGrabCheckTimeout bounds how long RefreshQueue waits on any single
	// grab's check (see checkOneGrab) before moving on. Zero/negative
	// falls back to defaultPerGrabCheckTimeout. Tests set this short to
	// exercise the timeout path without a real 60s wait.
	PerGrabCheckTimeout time.Duration

	// inFlight is the set of grabs whose check is still running after
	// RefreshQueue stopped waiting for it - see checkOneGrab.
	inFlightMu gosync.Mutex
	inFlight   map[int64]bool
}

// beginCheck claims a grab for checking, or reports that a check started on
// an earlier tick is still running.
//
// A check that outlives PerGrabCheckTimeout keeps going detached, and only
// writes last_checked when it finally finishes - so until then the grab
// still looks overdue and the next tick would start a second copy of the
// same import. Found live 2026-09-16: an eight-file season pack was
// re-imported every few minutes, each run re-copying files the last run had
// already copied and attaching a second and third episode_files row to the
// same episode.
func (s *DownloadService) beginCheck(grabID int64) bool {
	s.inFlightMu.Lock()
	defer s.inFlightMu.Unlock()
	if s.inFlight[grabID] {
		return false
	}
	if s.inFlight == nil {
		s.inFlight = map[int64]bool{}
	}
	s.inFlight[grabID] = true
	return true
}

func (s *DownloadService) endCheck(grabID int64) {
	s.inFlightMu.Lock()
	defer s.inFlightMu.Unlock()
	delete(s.inFlight, grabID)
}

// ErrNoDownloadClient means no enabled client takes the release's protocol.
type ErrNoDownloadClient struct{ Protocol string }

func (e ErrNoDownloadClient) Error() string {
	return fmt.Sprintf("no enabled %s download client - add one under Settings → Download Clients", e.Protocol)
}

// clientRows lists the saved clients, or the .env Deluge when none are saved.
func (s *DownloadService) clientRows(ctx context.Context) ([]store.DownloadClient, error) {
	if s.DB != nil {
		rows, err := store.ListDownloadClients(ctx, s.DB)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			return rows, nil
		}
	}
	baseURL, password := effectiveDelugeConfig(ctx, s.DB, s.BootstrapDelugeBaseURL, s.BootstrapDelugePassword)
	if baseURL == "" {
		return nil, nil
	}
	return []store.DownloadClient{{Name: "Deluge", Implementation: store.ClientDeluge, Enabled: true, Priority: 1, BaseURL: baseURL, Password: password}}, nil
}

// Build connects a client row to its program.
func (s *DownloadService) Build(dc store.DownloadClient) (downloadclient.Client, error) {
	mapping := downloadclient.PathMapping{Remote: dc.RemotePath, Local: dc.LocalPath}
	switch dc.Implementation {
	case store.ClientDeluge:
		if dc.BaseURL == "" {
			return nil, deluge.ErrMissingCredential
		}
		c, err := deluge.New(deluge.Options{BaseURL: dc.BaseURL, Password: dc.Password})
		if err != nil {
			return nil, err
		}
		return &downloadclient.Deluge{Client: c, Mapping: mapping}, nil
	case store.ClientQBittorrent:
		return qbittorrent.New(qbittorrent.Options{BaseURL: dc.BaseURL, Username: dc.Username, Password: dc.Password, Category: dc.Category, Mapping: mapping})
	case store.ClientSABnzbd:
		return sabnzbd.New(sabnzbd.Options{BaseURL: dc.BaseURL, APIKey: dc.APIKey, Category: dc.Category, Mapping: mapping})
	case store.ClientTransmission:
		return transmission.New(transmission.Options{BaseURL: dc.BaseURL, Username: dc.Username, Password: dc.Password, Label: dc.Category, Mapping: mapping})
	case store.ClientNZBGet:
		return nzbget.New(nzbget.Options{BaseURL: dc.BaseURL, Username: dc.Username, Password: dc.Password, Category: dc.Category, Mapping: mapping})
	}
	return nil, fmt.Errorf("unknown download client %q", dc.Implementation)
}

// clientFor picks the enabled client for protocol with the lowest priority.
func (s *DownloadService) clientFor(ctx context.Context, protocol string) (downloadclient.Client, store.DownloadClient, error) {
	rows, err := s.clientRows(ctx)
	if err != nil {
		return nil, store.DownloadClient{}, err
	}
	for _, dc := range rows {
		if dc.Enabled && dc.Protocol() == protocol {
			c, err := s.Build(dc)
			return c, dc, err
		}
	}
	return nil, store.DownloadClient{}, ErrNoDownloadClient{Protocol: protocol}
}

// clientForGrab finds the client a grab went to: its row when it recorded
// one, otherwise a client running the same program, or any enabled client
// for its protocol.
func (s *DownloadService) clientForGrab(ctx context.Context, g store.Grab) (downloadclient.Client, error) {
	rows, err := s.clientRows(ctx)
	if err != nil {
		return nil, err
	}
	var fallback *store.DownloadClient
	for i, dc := range rows {
		switch {
		case g.DownloadClientRef.Valid && dc.ID == g.DownloadClientRef.Int64:
			return s.Build(dc)
		case fallback == nil && dc.Enabled && dc.Implementation == g.DownloadClient:
			fallback = &rows[i]
		}
	}
	if fallback == nil {
		for i, dc := range rows {
			if dc.Enabled && dc.Protocol() == g.Protocol {
				fallback = &rows[i]
				break
			}
		}
	}
	if fallback == nil {
		return nil, ErrNoDownloadClient{Protocol: g.Protocol}
	}
	return s.Build(*fallback)
}

// EnabledProtocols says which protocols have an enabled client.
func (s *DownloadService) EnabledProtocols(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	if s == nil {
		return out
	}
	rows, err := s.clientRows(ctx)
	if err != nil {
		return out
	}
	for _, dc := range rows {
		if dc.Enabled {
			out[dc.Protocol()] = true
		}
	}
	return out
}

func clientRef(dc store.DownloadClient) sql.NullInt64 {
	return sql.NullInt64{Int64: dc.ID, Valid: dc.ID > 0}
}

func (s *DownloadService) grab(ctx context.Context, g store.Grab, release newznab.Release, by string) (int64, error) {
	if err := s.checkGrabLimit(ctx, release); err != nil {
		return 0, err
	}
	clientID, dc, hash, err := s.send(ctx, g, release)
	if err != nil {
		return 0, err
	}
	g.ReleaseTitle, g.Indexer, g.Protocol = release.Title, release.Indexer, release.Protocol
	g.Published = nullTime(release.PublishDate)
	g.InfoHash = sql.NullString{String: hash, Valid: hash != ""}
	g.IndexerID = sql.NullInt64{Int64: release.IndexerID, Valid: release.IndexerID > 0}
	g.GrabbedBy = by
	g.Size = sql.NullInt64{Int64: release.Size, Valid: release.Size > 0}
	g.DownloadClient, g.DownloadClientID, g.DownloadClientRef = dc.Implementation, sql.NullString{String: clientID, Valid: true}, clientRef(dc)
	g.Status = "grabbed"
	id, err := store.InsertGrab(ctx, s.DB, g)
	if err == nil {
		g.ID = id
		s.Events.Record(ctx, grabEvent(ctx, s.DB, g, store.EventGrabbed))
	}
	return id, err
}

// GrabMovie fetches release and sends it to a client, recording a grabs row
// on success.
func (s *DownloadService) GrabMovie(ctx context.Context, movieID int64, release newznab.Release, by string) (int64, error) {
	return s.grab(ctx, store.Grab{MovieID: sql.NullInt64{Int64: movieID, Valid: true}}, release, by)
}

// GrabSeries fetches release and sends it to a client. seasonNumber is nil
// for a whole-series grab, or set for a season-pack grab.
func (s *DownloadService) GrabSeries(ctx context.Context, seriesID int64, seasonNumber *int, release newznab.Release, by string) (int64, error) {
	g := store.Grab{SeriesID: sql.NullInt64{Int64: seriesID, Valid: true}}
	if seasonNumber != nil {
		g.SeasonNumber = sql.NullInt64{Int64: int64(*seasonNumber), Valid: true}
	} else {
		g.SeasonNumber, g.EpisodeNumber = s.grabCoverage(ctx, seriesID, release.Title)
	}
	return s.grab(ctx, g, release, by)
}

// grabCoverage works out which episodes a release covers when the caller
// did not say - a search run from the series page has no season to pass on,
// so the release title is all there is to go on.
//
// This matters because a grab with no season recorded means "the whole
// series": leave it empty for a season pack and every episode of every
// season shows as Downloading, including ones that aired ten years ago and
// ones that have not aired at all.
//
// A release naming several episodes only claims the first of them. Claiming
// the whole season instead would put the same false Downloading on the rest
// of it, and the others correct themselves the moment the files import.
func (s *DownloadService) grabCoverage(ctx context.Context, seriesID int64, title string) (season, episode sql.NullInt64) {
	info, ok := releaseparse.ParseEpisode(title)
	if !ok || info.MultiSeason {
		return sql.NullInt64{}, sql.NullInt64{} // genuinely the whole series, or unreadable
	}
	if !info.AirDate.IsZero() {
		// A daily release names a date, not a season - ask the library
		// which episode that was.
		sn, en, found := store.EpisodeAiredOn(ctx, s.DB, seriesID, info.AirDate)
		if !found {
			return sql.NullInt64{}, sql.NullInt64{}
		}
		return sql.NullInt64{Int64: int64(sn), Valid: true}, sql.NullInt64{Int64: int64(en), Valid: true}
	}
	season = sql.NullInt64{Int64: int64(info.Season), Valid: true}
	if len(info.Episodes) > 0 {
		episode = sql.NullInt64{Int64: int64(info.Episodes[0]), Valid: true}
	}
	return season, episode
}

// GrabEpisode fetches release and sends it to a client for one specific
// episode - unlike GrabSeries, the resulting grab records EpisodeNumber
// too, so ImportService.Import still routes through the normal
// importSeries per-file S/E matcher while Activity/bookkeeping can tell
// this apart from a season-pack grab.
func (s *DownloadService) GrabEpisode(ctx context.Context, seriesID int64, seasonNumber, episodeNumber int, release newznab.Release, by string) (int64, error) {
	return s.grab(ctx, store.Grab{
		SeriesID:      sql.NullInt64{Int64: seriesID, Valid: true},
		SeasonNumber:  sql.NullInt64{Int64: int64(seasonNumber), Valid: true},
		EpisodeNumber: sql.NullInt64{Int64: int64(episodeNumber), Valid: true},
	}, release, by)
}

// GrabAlbum fetches release and sends it to a client.
func (s *DownloadService) GrabAlbum(ctx context.Context, albumID int64, release newznab.Release, by string) (int64, error) {
	return s.grab(ctx, store.Grab{AlbumID: sql.NullInt64{Int64: albumID, Valid: true}}, release, by)
}

// GrabTrack fetches release and sends it to a client for one specific track
// under albumID - the resulting grab's TrackID makes ImportService.Import
// route through importTrack (a direct single-file-to-single-track attach)
// instead of importAlbum's positional whole-release match.
func (s *DownloadService) GrabTrack(ctx context.Context, albumID, trackID int64, release newznab.Release, by string) (int64, error) {
	return s.grab(ctx, store.Grab{AlbumID: sql.NullInt64{Int64: albumID, Valid: true}, TrackID: sql.NullInt64{Int64: trackID, Valid: true}}, release, by)
}

// send resolves release's download link through its indexer and hands it
// to the client for its protocol. Deliberately does NOT set a per-download
// location - each client downloads to its own configured directory, kept
// separate from any library folder, since imports COPY a finished
// download's files (a torrent client needs the original to keep seeding),
// not move them; a library folder as the download location would leave
// every download's raw files sitting in the library alongside the imported
// copy.
//
// It also returns a torrent's infohash, and refuses a torrent whose hash is
// blocklisted for g's item when its indexer rejects blocklisted hashes.
func (s *DownloadService) send(ctx context.Context, g store.Grab, release newznab.Release) (string, store.DownloadClient, string, error) {
	client, dc, err := s.clientFor(ctx, release.Protocol)
	if err != nil {
		return "", dc, "", err
	}
	fetched, err := s.Indexers.FetchRelease(ctx, release)
	if err != nil {
		return "", dc, "", fmt.Errorf("fetch release %q: %w", release.Title, err)
	}
	var hash string
	if release.Protocol == newznab.ProtocolTorrent {
		hash = releaseHash(release, fetched)
		if err := s.rejectBlocklistedHash(ctx, g, release, hash); err != nil {
			return "", dc, "", err
		}
	}
	id, err := client.Add(ctx, fetched)
	if err != nil {
		return "", dc, "", fmt.Errorf("%s: %w", dc.Name, err)
	}
	s.applySeedRatio(ctx, client, release, id)
	return id, dc, hash, nil
}

// nextGrabStatus decides g's next status from st, handing off to
// ImportService.Import exactly once the client reports the download done.
func (s *DownloadService) nextGrabStatus(ctx context.Context, g store.Grab, st downloadclient.Status) (string, sql.NullString) {
	if st.IsFinished {
		if s.Import == nil {
			return "import_failed", sql.NullString{String: "import service not configured", Valid: true}
		}
		if st.Failed {
			return "import_failed", sql.NullString{String: st.Message, Valid: true}
		}
		files := make([]importer.File, len(st.Files))
		for i, f := range st.Files {
			files[i] = importer.File{Path: f.Path, Size: f.Size}
		}
		status, msg := s.Import.Import(ctx, g, files, st.SavePath)
		return status, sql.NullString{String: msg, Valid: msg != ""}
	}
	if st.Failed {
		return "failed", sql.NullString{String: st.Message, Valid: true}
	}
	return "downloading", sql.NullString{}
}

// defaultPerGrabCheckTimeout is DownloadService.PerGrabCheckTimeout's
// fallback when unset - see checkOneGrab.
const defaultPerGrabCheckTimeout = 60 * time.Second

// checkOneGrab runs nextGrabStatus (which, for a finished download, means
// ImportService.Import - copying the actual video file) in its own
// goroutine and waits up to timeout for it, rather than calling it in-line
// on RefreshQueue's single ticker goroutine directly.
//
// Found live 2026-09-15: grabs sat frozen at status=downloading for 8+
// minutes with zero errors logged, then all resolved within ~90s of a
// container restart. CopyFile's io.Copy has no timeout or context at all,
// and this container's disk has shown real contention/stalls. Since
// RefreshQueue's per-grab loop was synchronous and single-goroutine, one
// stuck copy blocked every other due grab in that tick AND every future
// tick forever (Go tickers drop missed ticks rather than queuing them).
//
// A context deadline alone can't stop the underlying copy. This lets the
// ticker goroutine give up waiting and move on while the goroutine keeps
// running detached, still able to call persistGrabStatus/touchGrabChecked
// whenever it actually finishes. ok=false means the timeout fired - the
// caller's copy of grabs isn't updated this round, but nothing is lost.
func (s *DownloadService) checkOneGrab(ctx context.Context, g store.Grab, st downloadclient.Status, timeout time.Duration) (store.Grab, bool) {
	if !s.beginCheck(g.ID) {
		log.Printf("refresh queue: grab %d (%s) is still being processed by an earlier check - not starting another", g.ID, g.ReleaseTitle)
		return g, false
	}
	return checkWithTimeout(g, timeout, func() store.Grab {
		defer s.endCheck(g.ID)
		status, message := s.nextGrabStatus(ctx, g, st)
		if status == g.Status {
			s.touchGrabChecked(ctx, g)
			return g
		}
		return s.clientStatusChanged(ctx, g, status, message.String)
	})
}

// checkWithTimeout runs work in its own goroutine and waits up to timeout
// for it - a plain func() store.Grab so the timeout mechanism itself is
// directly testable with a synthetic slow work func.
func checkWithTimeout(g store.Grab, timeout time.Duration, work func() store.Grab) (store.Grab, bool) {
	done := make(chan store.Grab, 1)
	go func() { done <- work() }()
	select {
	case updated := <-done:
		return updated, true
	case <-time.After(timeout):
		log.Printf("refresh queue: grab %d (%s) still processing after %s - moving on, its update will land whenever it finishes",
			g.ID, g.ReleaseTitle, timeout)
		return store.Grab{}, false
	}
}

// isTerminalGrabStatus reports whether status is one the fallback poller
// should stop touching entirely. "needs_extraction" counts as terminal
// here too - nothing changes on the client's side once a download's own
// files are archives; only the user's explicit "Check again" (RetryImport)
// can move it forward.
func isTerminalGrabStatus(status string) bool {
	switch status {
	case "imported", "import_failed", "needs_extraction", "failed", "removed":
		return true
	default:
		return false
	}
}

// persistGrabStatus is the shared tail for every status-changing path
// (RefreshQueue/RefreshGrab/RetryImport) - saves status/message (which
// also bumps last_checked, see store.UpdateGrabStatus) and reflects it
// onto the caller's in-memory grab, best-effort.
func (s *DownloadService) persistGrabStatus(ctx context.Context, grab store.Grab, status, message string) store.Grab {
	msg := sql.NullString{String: message, Valid: message != ""}
	if err := store.UpdateGrabStatus(ctx, s.DB, grab.ID, status, msg); err == nil {
		grab.Status, grab.StatusMessage = status, msg
		switch status {
		case "failed", "import_failed", "needs_extraction":
			s.Events.Record(ctx, grabEvent(ctx, s.DB, grab, status))
		}
	}
	return grab
}

// touchGrabChecked records "checked, nothing changed" - still needs to
// bump last_checked so a still-downloading grab doesn't look perpetually
// overdue and get re-polled on every single tick regardless of
// PollGracePeriod.
func (s *DownloadService) touchGrabChecked(ctx context.Context, grab store.Grab) store.Grab {
	_ = store.TouchGrabChecked(ctx, s.DB, grab.ID) // best-effort
	return grab
}

// RefreshGrab is the /downloads/{hash}/completed webhook's entry point -
// a download client pushing "this finished" instead of waiting for the
// next fallback sweep. Always counts as "reported in" (bumps
// last_checked) whether or not the status actually changed.
func (s *DownloadService) RefreshGrab(ctx context.Context, grab store.Grab) store.Grab {
	client, err := s.clientForGrab(ctx, grab)
	if err != nil || !grab.DownloadClientID.Valid {
		return s.persistGrabStatus(ctx, grab, "import_failed", "no download client to refresh from")
	}
	statuses, err := client.Statuses(ctx, []string{grab.DownloadClientID.String})
	if err != nil {
		return grab // transient client error - leave last_checked alone, let the fallback sweep retry soon
	}
	st, ok := statuses[grab.DownloadClientID.String]
	if !ok {
		return s.persistGrabStatus(ctx, grab, "import_failed", "download no longer present in the client")
	}
	status, message := s.nextGrabStatus(ctx, grab, st)
	if status == grab.Status {
		return s.touchGrabChecked(ctx, grab)
	}
	return s.clientStatusChanged(ctx, grab, status, message.String)
}

// clientStatusChanged saves a status the download client's report led to,
// and runs failed download handling when that report is a failure.
func (s *DownloadService) clientStatusChanged(ctx context.Context, g store.Grab, status, message string) store.Grab {
	wasFailed := g.Status == "failed"
	g = s.persistGrabStatus(ctx, g, status, message)
	if status == "failed" && !wasFailed && g.Status == "failed" {
		s.downloadFailed(ctx, g, message)
	}
	return g
}

// RetryImport is the "Check again" button's handler: re-attempts import
// for grab regardless of its current status, scanning the download
// directory LIVE from disk (importer.ScanDirectory) instead of trusting
// the client's own file listing - a torrent client only ever reports the
// files that were part of the original torrent (e.g. one .zip), never
// anything a user extracts into that folder afterward.
func (s *DownloadService) RetryImport(ctx context.Context, grab store.Grab) store.Grab {
	client, err := s.clientForGrab(ctx, grab)
	if err != nil || s.Import == nil || !grab.DownloadClientID.Valid {
		return s.persistGrabStatus(ctx, grab, "import_failed", "download client or import service not configured")
	}
	statuses, err := client.Statuses(ctx, []string{grab.DownloadClientID.String})
	if err != nil {
		return s.persistGrabStatus(ctx, grab, "import_failed", err.Error())
	}
	st, ok := statuses[grab.DownloadClientID.String]
	if !ok {
		return s.persistGrabStatus(ctx, grab, "import_failed", "download no longer present in the client")
	}
	// SavePath is the download ROOT, shared by every download saved there -
	// scanning it sweeps up files belonging to unrelated downloads. This
	// download's own content lives at SavePath/Name.
	root := filepath.Join(st.SavePath, st.Name)
	info, err := os.Stat(root)
	if err != nil {
		return s.persistGrabStatus(ctx, grab, "import_failed", fmt.Sprintf("download not found on disk at %s", root))
	}

	var files []importer.File
	scanRoot := root
	if info.IsDir() {
		files, err = importer.ScanDirectory(root)
		if err != nil {
			return s.persistGrabStatus(ctx, grab, "import_failed", err.Error())
		}
	} else {
		// Single-file download: Name is the file itself, so the directory
		// paths are relative to its parent.
		scanRoot = st.SavePath
		files = []importer.File{{Path: st.Name, Size: info.Size()}}
	}

	status, message := s.Import.Import(ctx, grab, files, scanRoot)
	return s.persistGrabStatus(ctx, grab, status, message)
}

const defaultPollGracePeriod = 5 * time.Minute

// RefreshQueue is the fallback poller - run on a 1-minute ticker started
// in cmd/ummarr's serve command, NOT called from the Activity page (that
// just reads the grabs table directly). Only asks a client about a grab
// that hasn't "reported in" - via the webhook (RefreshGrab) or a previous
// tick of this same function - for at least PollGracePeriod, so a
// correctly-configured webhook means this rarely calls a client at all.
// A grab with last_checked NULL (never checked) is always immediately due.
func (s *DownloadService) RefreshQueue(ctx context.Context) ([]store.Grab, error) {
	grabs, err := store.ListGrabs(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	grace := s.PollGracePeriod
	if grace <= 0 {
		grace = defaultPollGracePeriod
	}
	cutoff := time.Now().Add(-grace)

	timeout := s.PerGrabCheckTimeout
	if timeout <= 0 {
		timeout = defaultPerGrabCheckTimeout
	}

	// Due grabs, grouped by the client they went to.
	type group struct {
		client downloadclient.Client
		idx    []int
	}
	groups := map[string]*group{}
	var order []string
	for i, g := range grabs {
		if isTerminalGrabStatus(g.Status) || !g.DownloadClientID.Valid {
			continue
		}
		if g.LastChecked.Valid && g.LastChecked.Time.After(cutoff) {
			continue // reported in recently enough - not due yet
		}
		key := g.DownloadClient
		if g.DownloadClientRef.Valid {
			key = fmt.Sprintf("%s#%d", g.DownloadClient, g.DownloadClientRef.Int64)
		}
		grp, ok := groups[key]
		if !ok {
			client, err := s.clientForGrab(ctx, g)
			if err != nil {
				log.Printf("refresh queue: client for grab %d (%s): %v", g.ID, g.ReleaseTitle, err)
				continue
			}
			grp = &group{client: client}
			groups[key] = grp
			order = append(order, key)
		}
		grp.idx = append(grp.idx, i)
	}

	for _, key := range order {
		grp := groups[key]
		ids := make([]string, len(grp.idx))
		for i, idx := range grp.idx {
			ids[i] = grabs[idx].DownloadClientID.String
		}
		statuses, err := grp.client.Statuses(ctx, ids)
		if err != nil {
			log.Printf("refresh queue: get status for %d grabs from %s: %v", len(ids), key, err)
			continue // transient client error - due grabs stay due, tried again next tick
		}
		for _, idx := range grp.idx {
			g := grabs[idx]
			st, ok := statuses[g.DownloadClientID.String]
			if !ok {
				log.Printf("refresh queue: grab %d (%s, id %s) missing from %s's status response (%d ids asked, %d returned)",
					g.ID, g.ReleaseTitle, g.DownloadClientID.String, key, len(ids), len(statuses))
				continue
			}
			if updated, ok := s.checkOneGrab(ctx, g, st, timeout); ok {
				grabs[idx] = updated
			}
		}
	}
	return grabs, nil
}

// applySeedRatio tells the client to stop the download at its indexer's Seed
// Ratio, when one is set. Seed time isn't applied. A failure here doesn't
// undo the grab; it's only logged.
func (s *DownloadService) applySeedRatio(ctx context.Context, client downloadclient.Client, release newznab.Release, id string) {
	if release.IndexerID == 0 || id == "" || release.Protocol != newznab.ProtocolTorrent {
		return
	}
	ix, err := store.GetIndexer(ctx, s.DB, release.IndexerID)
	if err != nil || !ix.SeedRatio.Valid {
		return
	}
	if err := client.SetSeedRatio(ctx, id, ix.SeedRatio.Float64); err != nil {
		log.Printf("set seed ratio for %q: %v", release.Title, err)
	}
}

// ClientNameFor names the client a release of protocol would go to, or "".
func (s *DownloadService) ClientNameFor(ctx context.Context, protocol string) string {
	if s == nil {
		return ""
	}
	if _, dc, err := s.clientFor(ctx, protocol); err == nil {
		return dc.Name
	}
	return ""
}

// TestClient checks a client's connection and credentials without saving it.
func (s *DownloadService) TestClient(ctx context.Context, dc store.DownloadClient) error {
	client, err := s.Build(dc)
	if err != nil {
		return err
	}
	return client.Test(ctx)
}

// BackfillGrabCoverage re-reads the release titles of in-flight series grabs
// that recorded no season and fills one in where the title names one.
//
// Grabs made from a series-level search used to record nothing, so a season
// pack claimed every episode of its series and the whole run showed as
// Downloading. New grabs work this out when they are made (see
// grabCoverage); this repairs the ones already in the download client.
func (s *DownloadService) BackfillGrabCoverage(ctx context.Context) error {
	grabs, err := store.QueuedSeriesGrabsWithoutSeason(ctx, s.DB)
	if err != nil {
		return err
	}
	for _, g := range grabs {
		season, episode := s.grabCoverage(ctx, g.SeriesID.Int64, g.ReleaseTitle)
		if !season.Valid {
			continue // still looks like a whole-series pack - leave it alone
		}
		if err := store.SetGrabCoverage(ctx, s.DB, g.ID, season, episode); err != nil {
			return err
		}
		log.Printf("grab %d (%q) now covers season %d", g.ID, g.ReleaseTitle, season.Int64)
	}
	return nil
}
