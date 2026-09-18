package sync

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Optimus-Perky/UMMarr/internal/metadata/providers/discogs"
	"github.com/Optimus-Perky/UMMarr/internal/store"
	"github.com/Optimus-Perky/UMMarr/internal/titleutil"
)

// Cover art from Discogs, for the albums whose folders have none.
//
// MusicBrainz returns no images at all, so an album with no folder.jpg
// beside its tracks had nothing to show. Discogs has the artwork and
// answers without a token (at a lower rate), so this fills the gaps.
//
// The image is written into UMMarr's own directory, never into the music
// library: those folders are the user's, and a hand-managed library is
// exactly where an unexpected file would be unwelcome. A cover the user
// already has always wins - see store.CoverFile.

// CoverFetcher fills in missing album artwork from Discogs.
type CoverFetcher struct {
	DB      *sql.DB
	Discogs *discogs.Client
	// Dir is where fetched images are kept, e.g. /config/artwork.
	Dir string
	// HTTP fetches the image itself; nil uses the default client.
	HTTP *http.Client
}

// CoverReport is what a fetch run did.
type CoverReport struct {
	Looked  int
	Fetched int
	NoMatch int
	Failed  int
}

// Summary is the report in one line.
func (r CoverReport) Summary() string {
	return fmt.Sprintf("%d album(s) without artwork: %d fetched, %d had no match on Discogs, %d failed.",
		r.Looked, r.Fetched, r.NoMatch, r.Failed)
}

// FetchMissingCovers looks up every album with no artwork and caches the
// front cover of the best matching Discogs release. limit caps how many
// are attempted in one run (0 for all), because Discogs answers 25
// requests a minute without a token and each album costs two.
func (f *CoverFetcher) FetchMissingCovers(ctx context.Context, limit int) (CoverReport, error) {
	var report CoverReport
	if f.Discogs == nil {
		return report, fmt.Errorf("Discogs isn't configured")
	}
	if err := os.MkdirAll(f.Dir, 0o755); err != nil {
		return report, fmt.Errorf("create artwork folder: %w", err)
	}
	candidates, err := store.AlbumsWithoutCover(ctx, f.DB)
	if err != nil {
		return report, err
	}
	for _, c := range candidates {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		if limit > 0 && report.Looked >= limit {
			break
		}
		report.Looked++
		url, err := f.findCover(ctx, c)
		if err != nil {
			report.Failed++
			continue
		}
		if url == "" {
			report.NoMatch++
			continue
		}
		file, err := f.download(ctx, c.AlbumID, url)
		if err != nil {
			report.Failed++
			continue
		}
		if err := store.SetCachedCover(ctx, f.DB, c.AlbumID, file, "discogs"); err != nil {
			report.Failed++
			continue
		}
		report.Fetched++
	}
	return report, nil
}

// findCover picks the Discogs release that best matches an album and
// returns its front cover URL. A search hit already carries a cover image,
// so a second call is only needed when it doesn't.
func (f *CoverFetcher) findCover(ctx context.Context, c store.AlbumCoverCandidate) (string, error) {
	results, err := f.Discogs.SearchRelease(ctx, c.Artist, c.Album)
	if err != nil {
		return "", err
	}
	best, ok := bestDiscogsMatch(results, c)
	if !ok {
		return "", nil
	}
	if best.CoverImage != "" && !strings.Contains(best.CoverImage, "spacer.gif") {
		return best.CoverImage, nil
	}
	release, err := f.Discogs.GetRelease(ctx, best.ID)
	if err != nil {
		return "", err
	}
	return release.FrontCover(), nil
}

// bestDiscogsMatch keeps the search honest: Discogs matches loosely, so a
// hit whose title isn't this album is no match at all, and one from the
// right year is preferred.
func bestDiscogsMatch(results []discogs.SearchResult, c store.AlbumCoverCandidate) (discogs.SearchResult, bool) {
	want := titleutil.CleanTitle(c.Artist + " " + c.Album)
	var best discogs.SearchResult
	found := false
	for _, r := range results {
		if titleutil.CleanTitle(r.Title) != want {
			continue
		}
		if !found {
			best, found = r, true
		}
		if c.Year > 0 && r.Year == strconv.Itoa(c.Year) {
			return r, true
		}
	}
	return best, found
}

// download saves the image beside the others, named for the album so a
// re-run overwrites rather than accumulating.
func (f *CoverFetcher) download(ctx context.Context, albumID int64, url string) (string, error) {
	client := f.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch cover: HTTP %d", resp.StatusCode)
	}
	ext := ".jpg"
	if strings.Contains(resp.Header.Get("Content-Type"), "png") {
		ext = ".png"
	}
	file := filepath.Join(f.Dir, fmt.Sprintf("album-%d%s", albumID, ext))
	out, err := os.Create(file)
	if err != nil {
		return "", err
	}
	defer out.Close()
	// A cover is a picture, not a payload: cap it so a wrong URL can't
	// fill the disk.
	if _, err := io.Copy(out, io.LimitReader(resp.Body, 20<<20)); err != nil {
		return "", err
	}
	return file, nil
}

// DiscogsFromSettings builds a client from the Discogs row in Settings →
// Metadata, or nil when it isn't enabled. The token is optional, so an
// enabled provider with an empty key still works - just slower.
func DiscogsFromSettings(ctx context.Context, db *sql.DB, userAgent string) *discogs.Client {
	provider, ok := store.EnabledMetadataProvider(ctx, db, "discogs")
	if !ok {
		return nil
	}
	client, err := discogs.New(discogs.Options{Token: provider.APIKey, UserAgent: userAgent})
	if err != nil {
		return nil
	}
	return client
}
