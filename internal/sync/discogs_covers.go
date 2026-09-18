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

// findCover returns the front cover of the release that best matches the
// album, and "" when Discogs has nothing that is this album. A search hit
// already carries a cover image, so a second call is only needed when it
// doesn't.
//
// It picks the pressing the same way the edition lookup does, because a
// cover is a picture of a particular sleeve: the Japanese CD and the 2009
// remaster of the same album do not look alike.
func (f *CoverFetcher) findCover(ctx context.Context, c store.AlbumEditionCandidate) (string, error) {
	best, ok, err := f.findEdition(ctx, c)
	if err != nil || !ok {
		return "", err
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

// EditionReport is what an edition-detail run did.
type EditionReport struct {
	Looked  int
	Filled  int
	NoMatch int
	Failed  int
}

// Summary is the report in one line.
func (r EditionReport) Summary() string {
	return fmt.Sprintf("%d album(s) without edition detail: %d filled in, %d had no match on Discogs, %d failed.",
		r.Looked, r.Filled, r.NoMatch, r.Failed)
}

// FetchEditions fills in what pressing each album is - the label,
// catalogue number and format descriptions that tell a remaster from an
// original. MusicBrainz has the country and date; this is the detail it
// doesn't carry.
//
// The search is narrowed to the country and medium MusicBrainz already
// settled on for the copy on disk, and what comes back is scored rather
// than taken in order - see rankEdition. A cover found on the way is kept
// too, since the release has been fetched anyway.
func (f *CoverFetcher) FetchEditions(ctx context.Context, limit int) (EditionReport, error) {
	var report EditionReport
	if f.Discogs == nil {
		return report, fmt.Errorf("Discogs isn't configured")
	}
	candidates, err := store.AlbumsWithoutEditionDetail(ctx, f.DB)
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

		best, ok, err := f.findEdition(ctx, c)
		if err != nil {
			report.Failed++
			continue
		}
		if !ok {
			report.NoMatch++
			continue
		}
		release, err := f.Discogs.GetRelease(ctx, best.ID)
		if err != nil {
			report.Failed++
			continue
		}
		label, catalogue := release.FirstLabel()
		edition := store.AlbumEdition{
			Label: label, Catalogue: catalogue, Format: release.FormatSummary(),
			Country: release.Country, Year: release.Year, DiscogsReleaseID: int64(release.ID),
		}
		if err := store.SetAlbumEdition(ctx, f.DB, c.AlbumID, edition); err != nil {
			report.Failed++
			continue
		}
		report.Filled++
		f.cacheCoverIfMissing(ctx, c.AlbumID, release)
	}
	return report, nil
}

// cacheCoverIfMissing keeps the artwork from a release fetched for its
// edition detail, so an album with neither doesn't need a second lookup.
func (f *CoverFetcher) cacheCoverIfMissing(ctx context.Context, albumID int64, release *discogs.Release) {
	if f.Dir == "" {
		return
	}
	if existing, err := store.CoverFile(ctx, f.DB, "album", albumID); err != nil || existing != "" {
		return
	}
	url := release.FrontCover()
	if url == "" {
		return
	}
	if err := os.MkdirAll(f.Dir, 0o755); err != nil {
		return
	}
	if file, err := f.download(ctx, albumID, url); err == nil {
		_ = store.SetCachedCover(ctx, f.DB, albumID, file, "discogs")
	}
}

// findEdition picks the Discogs release that best matches the copy on
// disk. Discogs' search ranking is not ours: asking it for "AC/DC" and
// "Back in Black" returns a page of pressings in no order worth trusting,
// which is how a Russian bootleg CD and an Australian cassette turned up
// as the edition of a UK album. So the search is narrowed by what
// MusicBrainz already decided - country first, then medium - and each
// remaining hit is scored.
func (f *CoverFetcher) findEdition(ctx context.Context, c store.AlbumEditionCandidate) (discogs.SearchResult, bool, error) {
	// Narrowest first. Each fallback drops one constraint, because a
	// filter Discogs has no hit for returns nothing at all rather than
	// something close.
	queries := []discogs.SearchQuery{{Country: c.Country, Format: c.Format}}
	if c.Country != "" && c.Format != "" {
		queries = append(queries, discogs.SearchQuery{Country: c.Country})
	}
	if c.Format != "" {
		queries = append(queries, discogs.SearchQuery{Format: c.Format})
	}
	queries = append(queries, discogs.SearchQuery{})
	for _, q := range queries {
		q.Artist, q.Album, q.PerPage = c.Artist, c.Album, 50
		results, err := f.Discogs.Search(ctx, q)
		if err != nil {
			return discogs.SearchResult{}, false, err
		}
		if best, ok := bestEdition(results, c); ok {
			return best, true, nil
		}
	}
	return discogs.SearchResult{}, false, nil
}

// bestEdition scores the hits and returns the highest, or false when none
// of them is this album at all.
func bestEdition(results []discogs.SearchResult, c store.AlbumEditionCandidate) (discogs.SearchResult, bool) {
	want := titleutil.CleanTitle(c.Artist + " " + c.Album)
	var best discogs.SearchResult
	bestScore := 0
	for _, r := range results {
		if titleutil.CleanTitle(r.Title) != want {
			continue
		}
		if score := rankEdition(r, c); score > bestScore {
			best, bestScore = r, score
		}
	}
	return best, bestScore > 0
}

// rankEdition scores one hit. Every release that is genuinely this album
// scores at least 1, so a single poor match still beats nothing; the
// weights below only decide between them.
func rankEdition(r discogs.SearchResult, c store.AlbumEditionCandidate) int {
	score := 1
	// Country, in the order the user asked for: the release MusicBrainz
	// chose, then home, then the US, which is where most of the rest of
	// the catalogue is pressed.
	switch {
	case c.Country != "" && strings.EqualFold(r.Country, c.Country):
		score += 40
	case strings.EqualFold(r.Country, "UK"), strings.EqualFold(r.Country, "GB"):
		score += 25
	case strings.EqualFold(r.Country, "US"):
		score += 15
	case strings.EqualFold(r.Country, "Europe"), strings.EqualFold(r.Country, "UK & Europe"):
		score += 12
	}
	// Medium. A rip of a CD described as a cassette is wrong even when
	// every other detail lines up.
	if c.Format != "" && hasFormat(r.Format, c.Format) {
		score += 30
	}
	// Year, from the chosen release first and the album's own date as a
	// fallback: a remaster and its original differ by little else.
	if year, err := strconv.Atoi(r.Year); err == nil && year > 0 {
		switch {
		case c.ReleaseYear > 0 && year == c.ReleaseYear:
			score += 20
		case c.Year > 0 && year == c.Year:
			score += 10
		}
	}
	// Discogs marks what isn't a proper commercial release in the format
	// list. None of it belongs in a library's edition line.
	for _, f := range r.Format {
		switch strings.ToLower(f) {
		case "unofficial release", "promo", "test pressing", "transcription", "mispress":
			score -= 35
		case "album":
			score += 5
		}
	}
	if score < 1 {
		score = 1
	}
	return score
}

// hasFormat reports whether a hit's format list mentions this medium.
func hasFormat(formats []string, want string) bool {
	for _, f := range formats {
		if strings.EqualFold(f, want) {
			return true
		}
	}
	return false
}
