package sync

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/indexer/newznab"
	"github.com/Optimus-Perky/UMMarr/internal/indexer/prowlarr"
	"github.com/Optimus-Perky/UMMarr/internal/store"
)

// ProwlarrConversion is where the old single Prowlarr connection came from:
// Settings (app_settings) or, failing that, the .env bootstrap.
type ProwlarrConversion struct {
	BootstrapBaseURL string
	BootstrapAPIKey  string
	UserAgent        string
	HTTP             *http.Client // test override
}

// ConvertProwlarrConnection turns UMMarr's old Prowlarr connection into one
// indexer per enabled Prowlarr indexer, built the way Prowlarr's own sync
// builds them ("Name (Prowlarr)", {prowlarr}/{id}/, /api, Prowlarr's key),
// with movie, TV and music categories. It runs once: when there's nothing to
// convert, or indexers already exist, it just records that it's done. An
// unreachable Prowlarr returns an error and leaves it to be tried again.
func ConvertProwlarrConnection(ctx context.Context, db *sql.DB, opts ProwlarrConversion) (int, error) {
	settings, err := store.GetIndexerSettings(ctx, db)
	if err != nil || settings.ProwlarrConverted {
		return 0, err
	}
	existing, err := store.ListIndexers(ctx, db)
	if err != nil {
		return 0, err
	}
	app, err := store.GetAppSettings(ctx, db)
	if err != nil {
		return 0, err
	}
	baseURL, apiKey := opts.BootstrapBaseURL, opts.BootstrapAPIKey
	if app.ProwlarrBaseURL != "" {
		baseURL = app.ProwlarrBaseURL
	}
	if app.ProwlarrAPIKey != "" {
		apiKey = app.ProwlarrAPIKey
	}
	if len(existing) > 0 || baseURL == "" || apiKey == "" {
		return 0, store.MarkProwlarrConverted(ctx, db)
	}

	client := prowlarr.New(prowlarr.Options{BaseURL: baseURL, APIKey: apiKey, UserAgent: opts.UserAgent, HTTP: opts.HTTP})
	indexers, err := client.Indexers(ctx)
	if err != nil {
		return 0, fmt.Errorf("convert prowlarr connection: %w", err)
	}
	profiles, err := client.AppProfiles(ctx)
	if err != nil {
		return 0, fmt.Errorf("convert prowlarr connection: %w", err)
	}
	profileByID := map[int]prowlarr.AppProfile{}
	for _, p := range profiles {
		profileByID[p.ID] = p
	}

	var rows []store.Indexer
	for _, pix := range indexers {
		if !pix.Enable {
			continue
		}
		if row, ok := convertedIndexer(client.BaseURL(), apiKey, pix, profileByID); ok {
			rows = append(rows, row)
		}
	}
	err = store.WithTx(ctx, db, func(tx *sql.Tx) error {
		for _, row := range rows {
			if _, err := store.CreateIndexer(ctx, tx, row); err != nil {
				return err
			}
		}
		return store.MarkProwlarrConverted(ctx, tx)
	})
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

func convertedIndexer(baseURL, apiKey string, pix prowlarr.Indexer, profiles map[int]prowlarr.AppProfile) (store.Indexer, bool) {
	supported := map[int]bool{}
	for _, id := range pix.CategoryIDs() {
		supported[id] = true
	}
	var cats []int
	for _, mediaType := range []string{newznab.MediaMovie, newznab.MediaSeries, newznab.MediaMusic} {
		for _, id := range newznab.SyncCategories[mediaType] {
			if supported[id] {
				cats = append(cats, id)
			}
		}
	}
	var anime []int
	if supported[newznab.AnimeCategory] {
		anime = []int{newznab.AnimeCategory}
	}
	if len(cats) == 0 && len(anime) == 0 {
		return store.Indexer{}, false
	}

	implementation := newznab.Torznab
	if strings.EqualFold(pix.Protocol, "usenet") {
		implementation = newznab.Newznab
	}
	priority := pix.Priority
	if priority < 1 || priority > 50 {
		priority = store.DefaultIndexerPriority
	}
	row := store.Indexer{
		Name:           pix.Name + " (Prowlarr)",
		Implementation: implementation,
		EnableRSS:      true, EnableAutomaticSearch: true, EnableInteractiveSearch: true,
		Priority:        priority,
		BaseURL:         strings.TrimRight(baseURL, "/") + "/" + strconv.Itoa(pix.ID) + "/",
		APIPath:         "/api",
		APIKey:          apiKey,
		Categories:      cats,
		AnimeCategories: anime,
		MinimumSeeders:  1,
		Converted:       true,
	}
	if p, ok := profiles[pix.AppProfileID]; ok {
		row.EnableRSS, row.EnableAutomaticSearch, row.EnableInteractiveSearch = p.EnableRss, p.EnableAutomaticSearch, p.EnableInteractiveSearch
		row.MinimumSeeders = p.MinimumSeeders
	}
	return row, true
}
