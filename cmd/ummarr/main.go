// Command ummarr is the entrypoint for UMMarr (Unified Media Manager) - a
// web interface with a custom Go backend replacing Sonarr/Radarr/Lidarr as
// three separate apps. Wires up "migrate" (apply pending schema
// migrations) and "serve" (run the web UI/API, including indexer search
// through Torznab/Newznab indexers, grabbing releases to Deluge, and importing finished
// downloads into the library).
package main

import (
	// The runtime image has no zoneinfo; embedding it lets TZ (e.g.
	// Europe/London) set the times the UI shows.
	_ "time/tzdata"

	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Optimus-Perky/UMMarr/internal/api"
	"github.com/Optimus-Perky/UMMarr/internal/auth"
	"github.com/Optimus-Perky/UMMarr/internal/backup"
	"github.com/Optimus-Perky/UMMarr/internal/config"
	"github.com/Optimus-Perky/UMMarr/internal/health"
	"github.com/Optimus-Perky/UMMarr/internal/importer"
	"github.com/Optimus-Perky/UMMarr/internal/importlist"
	"github.com/Optimus-Perky/UMMarr/internal/logbuf"
	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/musicbrainz"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/omdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tmdb"
	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/tvmaze"
	"github.com/Optimus-Perky/UMMarr/internal/nfo"
	"github.com/Optimus-Perky/UMMarr/internal/notify"
	"github.com/Optimus-Perky/UMMarr/internal/proxy"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/sync"
	"github.com/Optimus-Perky/UMMarr/internal/tasks"
	"github.com/Optimus-Perky/UMMarr/internal/updates"
	"github.com/Optimus-Perky/UMMarr/internal/urlbase"
	"github.com/Optimus-Perky/UMMarr/internal/version"
)

func main() {
	var dbPath string

	root := &cobra.Command{
		Use:   "ummarr",
		Short: "Unified media manager (movies, TV, music)",
	}
	root.PersistentFlags().StringVar(&dbPath, "db", "ummarr.db", "path to the SQLite database file")

	root.AddCommand(&cobra.Command{
		Use:   "migrate",
		Short: "Apply any pending database migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			fmt.Println("database schema up to date:", dbPath)
			return nil
		},
	})

	// The same work as the "Find episode names" task, runnable without the
	// web UI: handy over `docker exec`, and the only way in when whoever
	// holds the login isn't at the keyboard. Safe alongside a running
	// server - the database is WAL with a 60s busy timeout (internal/store/
	// store.go), so the two processes take turns rather than collide.
	var episodeNamesSeason int
	episodeNames := &cobra.Command{
		Use:   "episode-names [series title]",
		Short: "Ask the metadata providers for missing episode titles, summaries and air dates",
		Long: "With no argument this covers every series that still has unnamed episodes,\n" +
			"exactly like the Find episode names task. With a title (a case-insensitive\n" +
			"substring, which must match one series) it does that series alone.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			cfg := config.Load()
			svc := &sync.SeriesService{DB: db, TMDB: tmdb.New(tmdb.Options{Token: cfg.TMDBToken, UserAgent: cfg.UserAgent}),
				TVMaze: tvmaze.New(tvmaze.Options{UserAgent: cfg.UserAgent}), UserAgent: cfg.UserAgent}
			ctx := cmd.Context()

			if len(args) == 0 {
				report, err := svc.SearchMissingEpisodeNames(ctx)
				fmt.Println(report.Summary())
				return err
			}
			seriesID, title, err := findSeriesByTitle(ctx, db, args[0])
			if err != nil {
				return err
			}
			var season *int
			if episodeNamesSeason >= 0 {
				season = &episodeNamesSeason
			}
			report, err := svc.SearchEpisodeNames(ctx, seriesID, season)
			fmt.Printf("%s: %s\n", title, report.Summary())
			return err
		},
	}
	episodeNames.Flags().IntVar(&episodeNamesSeason, "season", -1, "only this season number (default: every season)")
	root.AddCommand(episodeNames)

	// Read-only on purpose: it prints what Organize & Rename WOULD do and
	// never touches a file. Renaming stays in the web UI, where each file
	// has a tick box.
	var renameAll bool
	renamePreview := &cobra.Command{
		Use:   "rename-preview [artist name]",
		Short: "Show which music files don't match the naming templates (changes nothing)",
		Long: "Lists every track file whose location doesn't match the album folder and\n" +
			"track file templates, artist by artist. With no argument it covers the whole\n" +
			"music library. Nothing is renamed - use Preview Rename in the web UI for that.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			ctx := cmd.Context()
			svc := &sync.ImportService{DB: db}

			artists, err := store.ListArtists(ctx, db)
			if err != nil {
				return err
			}
			totalFiles, totalArtists, failed := 0, 0, 0
			totalSkipped, skippedFolders := 0, 0
			for _, artist := range artists {
				if len(args) > 0 && !strings.Contains(strings.ToLower(artist.Name), strings.ToLower(args[0])) {
					continue
				}
				root, items, skipped, err := svc.ArtistRenamePreview(ctx, artist.ID)
				if err != nil {
					fmt.Printf("%s: %v\n", artist.Name, err)
					failed++
					continue
				}
				for _, skip := range skipped {
					totalSkipped += skip.Files
					skippedFolders++
					if renameAll {
						fmt.Printf("\n%s: left alone, %d file(s) in %s\n", artist.Name, skip.Files, skip.Folder)
					}
				}
				if len(items) == 0 {
					continue
				}
				totalArtists++
				totalFiles += len(items)
				fmt.Printf("\n%s (%d file(s)) under %s\n", artist.Name, len(items), root)
				show := items
				if !renameAll && len(show) > 3 {
					show = show[:3]
				}
				for _, item := range show {
					fmt.Printf("  - %s\n  + %s\n", item.Current, item.New)
				}
				if len(show) < len(items) {
					fmt.Printf("  ... and %d more (--all to list them)\n", len(items)-len(show))
				}
			}
			fmt.Printf("\n%d file(s) across %d artist(s) would be renamed.\n", totalFiles, totalArtists)
			if skippedFolders > 0 {
				fmt.Printf("%d file(s) in %d subfolder(s) were left alone - a disc or a second edition the templates can not express (--all lists them).\n", totalSkipped, skippedFolders)
			}
			if failed > 0 {
				fmt.Printf("%d artist(s) couldn't be read - see above.\n", failed)
			}
			return nil
		},
	}
	renamePreview.Flags().BoolVar(&renameAll, "all", false, "list every file rather than three per artist")
	root.AddCommand(renamePreview)

	root.AddCommand(&cobra.Command{
		Use:   "artwork",
		Short: "Import the cover art already sitting in the library folders",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer db.Close()
			svc := &sync.ImportService{DB: db}
			report, err := svc.ImportArtwork(cmd.Context())
			fmt.Println(report.Summary())
			if err != nil {
				return err
			}
			filled, err := svc.BackfillAudioQuality(cmd.Context())
			fmt.Printf("%d track file(s) had their audio quality worked out from an earlier analysis.\n", filled)
			return err
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Run the UMMarr web UI and API",
		RunE: func(cmd *cobra.Command, args []string) error {
			if applied, err := backup.ApplyPending(dbPath); err != nil {
				return err
			} else if applied {
				log.Println("applied the staged database restore")
			}
			db, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer db.Close()

			cfg := config.Load()
			logs := &logbuf.Buffer{Capacity: 3000}
			log.SetOutput(io.MultiWriter(os.Stderr, logs))
			log.Printf("UMMarr %s built %s", version.Commit, version.Built)

			// appSettings is only needed here to decide whether auth should
			// be enabled at all (see authConfigured below) - indexer and Deluge
			// connection info is looked up fresh, per call, by
			// IndexerService/DownloadService themselves (internal/sync/
			// settings.go), so a Settings -> Indexers/Download client save
			// takes effect immediately with no restart.
			appSettings, err := store.GetAppSettings(context.Background(), db)
			if err != nil {
				return err
			}

			tmdbClient := tmdb.New(tmdb.Options{Token: cfg.TMDBToken, UserAgent: cfg.UserAgent})
			tvmazeClient := tvmaze.New(tvmaze.Options{UserAgent: cfg.UserAgent})
			var omdbClient *omdb.Client
			if cfg.OMDbAPIKey != "" {
				omdbClient = omdb.New(omdb.Options{APIKey: cfg.OMDbAPIKey, UserAgent: cfg.UserAgent})
			}
			musicBrainzClient, err := musicbrainz.New(musicbrainz.Options{UserAgent: cfg.UserAgent})
			if err != nil {
				return err
			}

			indexerService := &sync.IndexerService{DB: db, UserAgent: cfg.UserAgent}
			movieService := &sync.MovieService{DB: db, TMDB: tmdbClient, OMDb: omdbClient}
			seriesService := &sync.SeriesService{DB: db, TMDB: tmdbClient, TVMaze: tvmazeClient, UserAgent: cfg.UserAgent}
			notifier := &notify.Service{DB: db, UserAgent: cfg.UserAgent}
			// Media analysis: FFprobe when it's installed (the image bundles it).
			var probe func(context.Context, string) (mediainfo.Info, error)
			if prober, err := mediainfo.FindFFprobe(); err != nil {
				log.Printf("media analysis: %v", err)
			} else {
				probe = prober.Probe
			}
			mediaAnalyzer := &sync.MediaAnalyzer{DB: db, Probe: probe, Workers: 2}
			// After probe exists: the release picker reads the files' own tags.
			musicService := &sync.MusicService{DB: db, MusicBrainz: musicBrainzClient, Probe: probe}
			store.MediaInfoTokens = func(ctx context.Context, path string) map[string]string {
				if probe == nil {
					return nil
				}
				info, err := probe(ctx, path)
				if err != nil {
					return nil
				}
				return info.Tokens()
			}
			events := &sync.Events{DB: db, Listeners: []sync.Listener{notifier, mediaAnalyzer}}
			metadataWriter := &nfo.Writer{DB: db, Permissions: func(ctx context.Context) importer.Permissions {
				ms, err := store.GetMediaSettings(ctx, db)
				if err != nil {
					return importer.Permissions{}
				}
				return sync.FilePermissions(ms)
			}}
			importService := &sync.ImportService{DB: db, Movies: movieService, Series: seriesService, Music: musicService, Events: events, Metadata: metadataWriter, Probe: probe}
			downloadService := &sync.DownloadService{
				DB: db, Import: importService, UserAgent: cfg.UserAgent, Events: events,
				Indexers:               indexerService,
				BootstrapDelugeBaseURL: cfg.DelugeBaseURL, BootstrapDelugePassword: cfg.DelugePassword,
				PollGracePeriod: cfg.DownloadPollGracePeriod,
			}

			searchService := &sync.SearchService{DB: db, Indexers: indexerService, Download: downloadService}
			downloadService.Redownload = searchService.Redownload

			var sessionCipher *auth.SessionCipher
			authConfigured := cfg.AuthPassword != "" || appSettings.AuthUsername != "" || appSettings.AuthPasswordHash != ""
			if authConfigured {
				sessionKey := cfg.SessionKey
				if sessionKey == nil {
					// UMMARR_SESSION_KEY wasn't set (or was malformed) - generate an
					// ephemeral key so login still works this run, but every session
					// is invalidated on the next restart/redeploy. Fine for a quick
					// local check; a real deployment should set UMMARR_SESSION_KEY
					// (32 random bytes, base64) so sessions survive restarts.
					sessionKey = make([]byte, auth.KeySize)
					if _, err := rand.Read(sessionKey); err != nil {
						return fmt.Errorf("generate ephemeral session key: %w", err)
					}
					log.Println("warning: UMMARR_SESSION_KEY not set - using an ephemeral key, all sessions will be invalidated on restart")
				}
				sessionCipher, err = auth.NewSessionCipher(sessionKey)
				if err != nil {
					return err
				}
			} else {
				log.Println("warning: UMMARR_AUTH_PASSWORD not set - the web UI has no login and is open to anyone who can reach it")
			}

			deps := api.Deps{
				DB:          db,
				TMDB:        tmdbClient,
				OMDb:        omdbClient,
				TVMaze:      tvmazeClient,
				MusicBrainz: musicBrainzClient,
				Movies:      movieService,
				Series:      seriesService,
				Music:       musicService,
				Indexer:     indexerService,
				Download:    downloadService,
				Search:      searchService,
				Import:      importService,
				Events:      events,
				Logs:        logs,
				Notifier:    notifier,
				Metadata:    metadataWriter,
				MediaInfo:   mediaAnalyzer,

				AuthUsername:  cfg.AuthUsername,
				AuthPassword:  cfg.AuthPassword,
				SessionCipher: sessionCipher,
				WebhookToken:  cfg.WebhookToken,

				BootstrapDelugeBaseURL:     cfg.DelugeBaseURL,
				BootstrapDelugePasswordSet: cfg.DelugePassword != "",
			}

			if _, err := store.EnsureAPIKey(context.Background(), db); err != nil {
				return err
			}

			// Repair grabs made before a series-level grab worked out which
			// season it had taken - see BackfillGrabCoverage.
			if err := downloadService.BackfillGrabCoverage(context.Background()); err != nil {
				log.Printf("backfill grab coverage: %v", err)
			}

			// Turn the old single Prowlarr connection into indexers, retrying
			// hourly while Prowlarr can't be reached.
			go func() {
				conversion := sync.ProwlarrConversion{
					BootstrapBaseURL: cfg.ProwlarrBaseURL, BootstrapAPIKey: cfg.ProwlarrAPIKey, UserAgent: cfg.UserAgent,
				}
				for {
					created, err := sync.ConvertProwlarrConnection(context.Background(), db, conversion)
					if err == nil {
						if created > 0 {
							log.Printf("converted the Prowlarr connection into %d indexers", created)
						}
						return
					}
					log.Printf("convert prowlarr connection (trying again in an hour): %v", err)
					time.Sleep(time.Hour)
				}
			}()

			// Background jobs, on the System → Tasks page and runnable from it.
			scheduler := tasks.New()
			healthChecker := &health.Checker{DB: db, TMDBConfigured: cfg.TMDBToken != "", FFprobeAvailable: mediaAnalyzer.FFprobeAvailable(), TestClient: downloadService.TestClient, SSL: appSettings.Host,
				OnNewIssue: func(ctx context.Context, issue health.Issue) {
					events.Record(ctx, store.Event{Event: store.EventHealth, Title: "Health issue", Detail: issue.Message, Source: "health check"})
				}}
			backups := &backup.Service{DB: db, DBPath: dbPath, Dir: filepath.Join(filepath.Dir(dbPath), "backups"), Keep: 4}
			updateChecker := &updates.Checker{Repo: "Optimus-Perky/UMMarr", Current: version.Commit, UserAgent: cfg.UserAgent}
			scheduler.Register(&tasks.Task{Name: "Refresh download queue", Description: "Asks the download clients about grabs that haven't reported in, and imports what finished.", Interval: time.Minute,
				Run: func(ctx context.Context) error { _, err := downloadService.RefreshQueue(ctx); return err }})
			scheduler.Register(&tasks.Task{Name: "RSS sync", Description: "Fetches every RSS-enabled indexer's feed when the RSS Sync Interval has passed, and grabs what the library wants.", Interval: time.Minute,
				Run: func(ctx context.Context) error {
					due, err := searchService.RSSDue(ctx)
					if err != nil || !due {
						return err
					}
					report, err := searchService.RSSSync(ctx)
					if err == nil {
						log.Printf("rss sync: %s", report.Summary())
					}
					return err
				}})
			scheduler.Register(&tasks.Task{Name: "Health check", Description: "Looks for configuration problems: indexers, download clients, library folders, free space.", Interval: 10 * time.Minute,
				Run: func(ctx context.Context) error { healthChecker.Check(ctx); return nil }})
			scheduler.Register(&tasks.Task{Name: "Backup", Description: "Copies the database to the backups folder; the newest four scheduled backups are kept.", Interval: 7 * 24 * time.Hour,
				Run: backups.Scheduled})
			scheduler.Register(&tasks.Task{Name: "Update check", Description: "Compares this build with the newest commit on GitHub.", Interval: 24 * time.Hour,
				Run: func(ctx context.Context) error { updateChecker.Check(ctx); return nil }})
			importLists := &importlist.Service{DB: db, TMDB: tmdbClient, Movies: movieService, Series: seriesService, Search: searchService, Events: events, UserAgent: cfg.UserAgent}
			scheduler.Register(&tasks.Task{Name: "Import list sync", Description: "Fetches every enabled import list and adds the titles the library doesn't have.", Interval: 6 * time.Hour, Run: importLists.SyncAll})
			deps.ImportLists = importLists
			scheduler.Register(&tasks.Task{Name: "Find episode names",
				Description: "Asks the metadata providers for titles, summaries and air dates of episodes still called \"Episode 5\" or nothing at all, without touching the episode lists. Cheaper than a full refresh - run it when a new season is announced.",
				Run: func(ctx context.Context) error {
					report, err := seriesService.SearchMissingEpisodeNames(ctx)
					log.Printf("find episode names: %s", report.Summary())
					return err
				}})
			scheduler.Register(&tasks.Task{Name: "Refresh metadata", Description: "Re-fetches every movie and series from the metadata providers (new episodes, titles, posters and season posters). Manual: metadata is fetched when something is added, and a single item is refreshed from its own page.",
				Run: func(ctx context.Context) error {
					report, err := sync.RefreshLibrary(ctx, movieService, seriesService)
					log.Printf("refresh metadata: %s", report.Summary())
					return err
				}})
			scheduler.Register(&tasks.Task{Name: "Import artwork",
				Description: "Finds the cover art already in the library folders (cover.jpg, folder.jpg and friends) and shows it on the album and artist pages. Nothing is downloaded or copied.",
				Run: func(ctx context.Context) error {
					report, err := importService.ImportArtwork(ctx)
					log.Printf("import artwork: %s", report.Summary())
					if err != nil {
						return err
					}
					filled, err := importService.BackfillAudioQuality(ctx)
					if filled > 0 {
						log.Printf("import artwork: filled in the audio quality of %d track file(s)", filled)
					}
					return err
				}})
			scheduler.Register(&tasks.Task{Name: "Analyze media files", Description: "Reads codecs, resolution, HDR, audio tracks and subtitles from files not analyzed yet, with FFprobe or from Plex (Settings → Media Management).", Interval: time.Hour, Run: mediaAnalyzer.Run})
			scheduler.Register(&tasks.Task{Name: "Write metadata", Description: "Writes .nfo files and images for the whole library for the providers ticked under Settings → Metadata (Kodi / Emby, Jellyfin, Plex).", Run: metadataWriter.WriteAll})
			scheduler.Register(&tasks.Task{Name: "Library scan", Description: "Scans every library folder for files already on disk and imports folders UMMarr doesn't track yet.",
				Run: func(ctx context.Context) error {
					if _, err := importService.ScanLibrary(ctx, sync.ScanScope{}); err != nil {
						return err
					}
					for _, mediaType := range []string{"movie", "series", "music"} {
						folders, err := store.ListRootFolders(ctx, db, mediaType)
						if err != nil {
							return err
						}
						for _, f := range folders {
							importService.StartLibraryImport(f.ID, f.Path, mediaType)
						}
					}
					return nil
				}})
			scheduler.Start(context.Background(), 30*time.Second)
			deps.Tasks, deps.Health, deps.Backups, deps.Updates = scheduler, healthChecker, backups, updateChecker
			deps.DBPath, deps.StartedAt = dbPath, time.Now()
			deps.URLBase = urlbase.Normalize(appSettings.Host.URLBase)
			deps.Restart = func() {
				log.Println("restarting to apply a restored backup")
				os.Exit(0)
			}

			host := appSettings.Host
			if host.ProxyEnabled && host.ProxyURL != "" {
				if err := proxy.Configure(host.ProxyURL, host.ProxyBypass); err != nil {
					log.Printf("proxy not used: %v", err)
				} else {
					log.Printf("outbound requests go through %s (bypassing %s)", host.ProxyURL, host.ProxyBypass)
				}
			}
			router := urlbase.Middleware(host.URLBase, api.NewRouter(deps))
			if base := urlbase.Normalize(host.URLBase); base != "" {
				log.Printf("serving under the URL base %s", base)
			}
			if host.SSLEnabled {
				go func() {
					addr := fmt.Sprintf(":%d", host.SSLPort)
					log.Printf("UMMarr listening with SSL on %s", addr)
					if err := http.ListenAndServeTLS(addr, host.SSLCertPath, host.SSLKeyPath, router); err != nil {
						log.Printf("ssl listener failed: %v", err)
					}
				}()
			}
			fmt.Println("UMMarr listening on", cfg.ListenAddr)
			return http.ListenAndServe(cfg.ListenAddr, router)
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "healthcheck",
		Short: "Check whether the local UMMarr server is responding (used as the Docker HEALTHCHECK)",
		RunE: func(cmd *cobra.Command, args []string) error {
			addr := config.Load().ListenAddr
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get("http://" + healthcheckHost(addr) + "/")
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 500 {
				return fmt.Errorf("healthcheck: server returned %d", resp.StatusCode)
			}
			return nil
		},
	})

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// findSeriesByTitle resolves a title typed on the command line to one
// series. A substring is enough, but an ambiguous one is an error listing
// the candidates rather than a guess at which series to rewrite.
func findSeriesByTitle(ctx context.Context, db *sql.DB, want string) (int64, string, error) {
	all, err := store.ListSeries(ctx, db)
	if err != nil {
		return 0, "", err
	}
	var matches []store.SeriesSummary
	for _, s := range all {
		if strings.Contains(strings.ToLower(s.Title), strings.ToLower(want)) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].ID, matches[0].Title, nil
	case 0:
		return 0, "", fmt.Errorf("no series matches %q", want)
	}
	var titles []string
	for _, m := range matches {
		titles = append(titles, m.Title)
	}
	return 0, "", fmt.Errorf("%q matches %d series: %s", want, len(matches), strings.Join(titles, ", "))
}

// healthcheckHost turns a bind-all listen address like ":8080" into
// something actually connectable ("127.0.0.1:8080") - ListenAddr is a
// bind address, not a client-usable host, and the healthcheck runs
// in-container against its own server.
func healthcheckHost(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	return addr
}
