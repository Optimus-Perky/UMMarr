package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Optimus-Perky/UMMarr/internal/mediainfo"
	"github.com/Optimus-Perky/UMMarr/internal/metadata"
	"github.com/Optimus-Perky/UMMarr/internal/releaseparse"
	"github.com/Optimus-Perky/UMMarr/internal/titleutil"
)

// UpsertArtistMetadata creates or updates the artist_metadata row for a,
// keyed on its MusicBrainz id (the only music provider wired up so far).
// Mirrors UpsertMovieMetadata's shape - see its comments. Called both for
// an album's own tracked artist AND, identity-fields-only, for every
// distinct track-credited artist on a release (see UpsertTrack) - a
// guest vocalist on one compilation track needs an artist_metadata row
// but not a full artists (tracked-library) row.
func UpsertArtistMetadata(ctx context.Context, q Queryer, a metadata.ArtistMetadata) (int64, error) {
	mbid, ok := a.ExternalIDs["musicbrainz"]
	if !ok {
		return 0, fmt.Errorf("upsert artist_metadata: %w", ErrNoAnchorExternalID)
	}

	genres, err := marshalJSON(a.Genres.Value, "[]")
	if err != nil {
		return 0, fmt.Errorf("marshal genres: %w", err)
	}
	images, err := marshalJSON(a.Images.Value, "[]")
	if err != nil {
		return 0, fmt.Errorf("marshal images: %w", err)
	}
	ratings, err := marshalJSON(a.Ratings, "{}")
	if err != nil {
		return 0, fmt.Errorf("marshal ratings: %w", err)
	}

	cleanName := titleutil.CleanTitle(a.Name.Value)
	sortName := titleutil.SortTitle(a.Name.Value)

	existingID, found, err := FindEntityIDByExternalID(ctx, q, "artist", "musicbrainz", mbid)
	if err != nil {
		return 0, err
	}

	var metadataID int64
	if found {
		_, err = q.ExecContext(ctx, `
			UPDATE artist_metadata SET
				name = ?, clean_name = ?, sort_name = ?, overview = ?,
				disambiguation = ?, artist_type = ?, status = ?,
				genres = ?, images = ?, ratings = ?, last_info_sync = CURRENT_TIMESTAMP
			WHERE id = ?
		`, a.Name.Value, cleanName, sortName, a.Overview.Value,
			a.Disambiguation.Value, a.ArtistType.Value, a.Status.Value,
			genres, images, ratings, existingID)
		if err != nil {
			return 0, fmt.Errorf("update artist_metadata: %w", err)
		}
		metadataID = existingID
	} else {
		res, err := q.ExecContext(ctx, `
			INSERT INTO artist_metadata (
				name, clean_name, sort_name, overview, disambiguation,
				artist_type, status, genres, images, ratings, last_info_sync
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		`, a.Name.Value, cleanName, sortName, a.Overview.Value, a.Disambiguation.Value,
			a.ArtistType.Value, a.Status.Value, genres, images, ratings)
		if err != nil {
			return 0, fmt.Errorf("insert artist_metadata: %w", err)
		}
		metadataID, err = res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("get inserted artist_metadata id: %w", err)
		}
	}

	if err := upsertExternalIDs(ctx, q, "artist", metadataID, a.ExternalIDs); err != nil {
		return 0, err
	}
	provenance := map[string]string{
		"name": a.Name.Provider, "disambiguation": a.Disambiguation.Provider,
		"artist_type": a.ArtistType.Provider, "overview": a.Overview.Provider,
		"status": a.Status.Provider, "genres": a.Genres.Provider, "images": a.Images.Provider,
	}
	if err := upsertProvenance(ctx, q, "artist", metadataID, provenance); err != nil {
		return 0, err
	}

	return metadataID, nil
}

// UpsertArtist ensures an artists (per-instance tracked) row exists for
// metadataID, returning its id unchanged if already present - same
// don't-clobber-user-settings rule as UpsertMovie/UpsertSeries.
func UpsertArtist(ctx context.Context, q Queryer, metadataID, qualityProfileID, rootFolderID int64, monitored bool) (int64, error) {
	var existingID int64
	err := q.QueryRowContext(ctx, `SELECT id FROM artists WHERE artist_metadata_id = ?`, metadataID).Scan(&existingID)
	if err == nil {
		return existingID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("find artist by metadata id: %w", err)
	}

	res, err := q.ExecContext(ctx, `
		INSERT INTO artists (artist_metadata_id, monitored, quality_profile_id, root_folder_id)
		VALUES (?, ?, ?, ?)
	`, metadataID, monitored, qualityProfileID, rootFolderID)
	if err != nil {
		return 0, fmt.Errorf("insert artist: %w", err)
	}
	artistID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted artist id: %w", err)
	}

	path, err := ResolveArtistPath(ctx, q, artistID)
	if err != nil {
		return 0, fmt.Errorf("resolve path for artist %d: %w", artistID, err)
	}
	config, err := GetNamingConfig(ctx, q, "music")
	if err != nil {
		return 0, err
	}
	if _, err := q.ExecContext(ctx, `UPDATE artists SET path = ?, path_template = ? WHERE id = ?`,
		path, config.ArtistFolderFormat.String, artistID); err != nil {
		return 0, fmt.Errorf("set artist path: %w", err)
	}

	return artistID, nil
}

// UpsertAlbum creates or updates the albums row for a under
// artistMetadataID, keyed on its MusicBrainz release-group id. Unlike
// movies/series/artists, albums have no separate per-instance table -
// monitored/path live directly on albums itself, so (unlike those) this
// IS one shared row that both Add and Refresh update fully, monitored
// included on first insert only (subsequent calls leave it alone, same
// reasoning as elsewhere).
//
// Deliberately does NOT resolve/set path itself, unlike UpsertMovie/
// UpsertSeries/UpsertArtist - a Various-Artists-with-series album's path
// depends on its compilation_series_albums link, which can only be
// created after this function returns an id to link against. The
// returned created bool tells the caller (see internal/sync/music.go)
// whether to call SetAlbumPath once that link (if any) is in place.
func UpsertAlbum(ctx context.Context, q Queryer, artistMetadataID int64, a metadata.AlbumMetadata) (albumID int64, created bool, err error) {
	mbid, ok := a.ExternalIDs["musicbrainz"]
	if !ok {
		return 0, false, fmt.Errorf("upsert album: %w", ErrNoAnchorExternalID)
	}

	secondaryTypes, err := marshalJSON(a.SecondaryTypes.Value, "[]")
	if err != nil {
		return 0, false, fmt.Errorf("marshal secondary_types: %w", err)
	}
	genres, err := marshalJSON(a.Genres.Value, "[]")
	if err != nil {
		return 0, false, fmt.Errorf("marshal genres: %w", err)
	}
	images, err := marshalJSON(a.Images.Value, "[]")
	if err != nil {
		return 0, false, fmt.Errorf("marshal images: %w", err)
	}
	ratings, err := marshalJSON(a.Ratings, "{}")
	if err != nil {
		return 0, false, fmt.Errorf("marshal ratings: %w", err)
	}
	cleanTitle := titleutil.CleanTitle(a.Title.Value)
	albumType := orDefault(a.AlbumType.Value, "Album")

	existingID, found, err := FindEntityIDByExternalID(ctx, q, "album", "musicbrainz", mbid)
	if err != nil {
		return 0, false, err
	}

	if found {
		_, err = q.ExecContext(ctx, `
			UPDATE albums SET
				title = ?, clean_title = ?, disambiguation = ?, overview = ?,
				release_date = ?, album_type = ?, secondary_types = ?,
				genres = ?, images = ?, ratings = ?, last_info_sync = CURRENT_TIMESTAMP
			WHERE id = ?
		`, a.Title.Value, cleanTitle, a.Disambiguation.Value, a.Overview.Value,
			dateOrNull(a.ReleaseDate.Value), albumType, secondaryTypes,
			genres, images, ratings, existingID)
		if err != nil {
			return 0, false, fmt.Errorf("update album: %w", err)
		}
		albumID = existingID
	} else {
		res, err := q.ExecContext(ctx, `
			INSERT INTO albums (
				artist_metadata_id, title, clean_title, disambiguation, overview,
				release_date, album_type, secondary_types, genres, images, ratings,
				monitored, last_info_sync
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, CURRENT_TIMESTAMP)
		`, artistMetadataID, a.Title.Value, cleanTitle, a.Disambiguation.Value, a.Overview.Value,
			dateOrNull(a.ReleaseDate.Value), albumType, secondaryTypes, genres, images, ratings)
		if err != nil {
			return 0, false, fmt.Errorf("insert album: %w", err)
		}
		albumID, err = res.LastInsertId()
		if err != nil {
			return 0, false, fmt.Errorf("get inserted album id: %w", err)
		}
		created = true
	}

	if err := upsertExternalIDs(ctx, q, "album", albumID, a.ExternalIDs); err != nil {
		return 0, false, err
	}
	provenance := map[string]string{
		"title": a.Title.Provider, "disambiguation": a.Disambiguation.Provider,
		"album_type": a.AlbumType.Provider, "secondary_types": a.SecondaryTypes.Provider,
		"overview": a.Overview.Provider, "genres": a.Genres.Provider, "images": a.Images.Provider,
	}
	if err := upsertProvenance(ctx, q, "album", albumID, provenance); err != nil {
		return 0, false, err
	}

	return albumID, created, nil
}

// SetAlbumPath resolves and persists albumID's path - called explicitly
// by the sync package once any compilation_series link has been
// established, not from within UpsertAlbum itself. See UpsertAlbum's
// comment for why these are separate.
func SetAlbumPath(ctx context.Context, q Queryer, albumID int64) error {
	path, err := ResolveAlbumPath(ctx, q, albumID)
	if err != nil {
		return fmt.Errorf("resolve path for album %d: %w", albumID, err)
	}
	config, err := GetNamingConfig(ctx, q, "music")
	if err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE albums SET path = ?, path_template = ? WHERE id = ?`,
		path, config.AlbumFolderFormat.String, albumID); err != nil {
		return fmt.Errorf("set album path: %w", err)
	}
	return nil
}

// UpsertAlbumRelease creates or updates the album_releases row for r
// under albumID, keyed on its MusicBrainz release id.
func UpsertAlbumRelease(ctx context.Context, q Queryer, albumID int64, r metadata.ReleaseMetadata) (int64, error) {
	mbid, ok := r.ExternalIDs["musicbrainz"]
	if !ok {
		return 0, fmt.Errorf("upsert album_release: %w", ErrNoAnchorExternalID)
	}

	country, err := marshalJSON(r.Country.Value, "[]")
	if err != nil {
		return 0, fmt.Errorf("marshal country: %w", err)
	}
	label, err := marshalJSON(r.Label.Value, "[]")
	if err != nil {
		return 0, fmt.Errorf("marshal label: %w", err)
	}

	existingID, found, err := FindEntityIDByExternalID(ctx, q, "release", "musicbrainz", mbid)
	if err != nil {
		return 0, err
	}

	var releaseID int64
	if found {
		_, err = q.ExecContext(ctx, `
			UPDATE album_releases SET
				title = ?, status = ?, disambiguation = ?, country = ?, label = ?,
				track_count = ?, release_date = ?
			WHERE id = ?
		`, r.Title.Value, r.Status.Value, r.Disambiguation.Value, country, label,
			r.TrackCount.Value, dateOrNull(r.ReleaseDate.Value), existingID)
		if err != nil {
			return 0, fmt.Errorf("update album_release: %w", err)
		}
		releaseID = existingID
	} else {
		res, err := q.ExecContext(ctx, `
			INSERT INTO album_releases (
				album_id, title, status, disambiguation, country, label,
				track_count, release_date, monitored
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1)
		`, albumID, r.Title.Value, r.Status.Value, r.Disambiguation.Value, country, label,
			r.TrackCount.Value, dateOrNull(r.ReleaseDate.Value))
		if err != nil {
			return 0, fmt.Errorf("insert album_release: %w", err)
		}
		releaseID, err = res.LastInsertId()
		if err != nil {
			return 0, fmt.Errorf("get inserted album_release id: %w", err)
		}
	}

	if err := upsertExternalIDs(ctx, q, "release", releaseID, r.ExternalIDs); err != nil {
		return 0, err
	}
	provenance := map[string]string{
		"title": r.Title.Provider, "status": r.Status.Provider, "disambiguation": r.Disambiguation.Provider,
		"country": r.Country.Provider, "label": r.Label.Provider,
		"track_count": r.TrackCount.Provider, "release_date": r.ReleaseDate.Provider,
	}
	if err := upsertProvenance(ctx, q, "release", releaseID, provenance); err != nil {
		return 0, err
	}

	return releaseID, nil
}

// UpsertTrack creates or updates the tracks row for t under releaseID,
// attributed to artistMetadataID (the caller resolves this - typically
// the first entry in t.ArtistCredits, deduped/upserted via
// UpsertArtistMetadata - see internal/sync/music.go).
//
// Known limitation: migration 00007's tracks table has no UNIQUE
// constraint of its own (unlike seasons/episodes), so idempotency here is
// enforced at the application level only (a natural-key lookup on
// album_release_id + medium_number + track_number before inserting) -
// concurrent syncs of the same release could theoretically race into a
// duplicate. Acceptable for now since syncs aren't run concurrently
// anywhere in this codebase yet; worth a real UNIQUE constraint in a
// future migration if that changes.
func UpsertTrack(ctx context.Context, q Queryer, releaseID, artistMetadataID int64, t metadata.TrackSource) (int64, error) {
	var existingID int64
	err := q.QueryRowContext(ctx, `
		SELECT id FROM tracks WHERE album_release_id = ? AND medium_number = ? AND track_number = ?
	`, releaseID, t.MediumNumber, t.Number).Scan(&existingID)
	if err == nil {
		_, err = q.ExecContext(ctx, `
			UPDATE tracks SET artist_metadata_id = ?, title = ?, duration_ms = ? WHERE id = ?
		`, artistMetadataID, t.Title, t.DurationMs, existingID)
		if err != nil {
			return 0, fmt.Errorf("update track: %w", err)
		}
		return existingID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("find track: %w", err)
	}

	res, err := q.ExecContext(ctx, `
		INSERT INTO tracks (album_release_id, artist_metadata_id, track_number, medium_number, title, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?)
	`, releaseID, artistMetadataID, t.Number, t.MediumNumber, t.Title, t.DurationMs)
	if err != nil {
		return 0, fmt.Errorf("insert track: %w", err)
	}
	trackID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted track id: %w", err)
	}
	return trackID, nil
}

// UpsertCompilationSeries creates or updates a compilation_series row
// keyed on its MusicBrainz series id, returning its id.
func UpsertCompilationSeries(ctx context.Context, q Queryer, name, sortName, mbSeriesID string) (int64, error) {
	var existingID int64
	err := q.QueryRowContext(ctx, `SELECT id FROM compilation_series WHERE musicbrainz_series_id = ?`, mbSeriesID).Scan(&existingID)
	if err == nil {
		_, err = q.ExecContext(ctx, `UPDATE compilation_series SET name = ?, sort_name = ? WHERE id = ?`, name, sortName, existingID)
		if err != nil {
			return 0, fmt.Errorf("update compilation_series: %w", err)
		}
		return existingID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("find compilation_series: %w", err)
	}

	res, err := q.ExecContext(ctx, `
		INSERT INTO compilation_series (name, sort_name, musicbrainz_series_id, source)
		VALUES (?, ?, ?, 'musicbrainz')
	`, name, sortName, mbSeriesID)
	if err != nil {
		return 0, fmt.Errorf("insert compilation_series: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted compilation_series id: %w", err)
	}
	return id, nil
}

// LinkAlbumToCompilationSeries creates or updates the
// compilation_series_albums row for albumID, keyed on its own UNIQUE
// constraint on album_id (an album belongs to zero-or-one series).
func LinkAlbumToCompilationSeries(ctx context.Context, q Queryer, compilationSeriesID, albumID int64, sequenceNumber int) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO compilation_series_albums (compilation_series_id, album_id, sequence_number)
		VALUES (?, ?, ?)
		ON CONFLICT (album_id) DO UPDATE SET
			compilation_series_id = excluded.compilation_series_id,
			sequence_number = excluded.sequence_number
	`, compilationSeriesID, albumID, sequenceNumber)
	if err != nil {
		return fmt.Errorf("link album %d to compilation series %d: %w", albumID, compilationSeriesID, err)
	}
	return nil
}

// AlbumSummary is a read-side view of one album for library listing pages
// - a thin projection of albums joined to its artist and (optionally) its
// compilation series. CompilationSeriesName is "" when the album isn't
// part of one - handlers group the flat list by ArtistName (or by
// CompilationSeriesName when set) before rendering, matching the
// Various-Artists-under-series tree from the approved mockup.
type AlbumSummary struct {
	ID                    int64
	ArtistMetadataID      int64
	ArtistName            string
	Title                 string
	Year                  sql.NullInt64
	Path                  sql.NullString
	Monitored             bool
	Added                 time.Time
	CompilationSeriesName string
	CompilationSeqNumber  sql.NullInt64
	PosterURL             string
	ArtistSlug            string // address segments: /music/albums/{ArtistSlug}/{Slug}
	Slug                  string
	RootFolderID          int64 // the artist's library folder
}

const albumSummaryQuery = `
	SELECT al.id, al.artist_metadata_id, am.name, al.title,
	       CAST(strftime('%Y', al.release_date) AS INTEGER), al.path, al.monitored, al.added,
	       COALESCE(cs.name, ''), csa.sequence_number, al.images,
	       COALESCE((SELECT ar.root_folder_id FROM artists ar WHERE ar.artist_metadata_id = al.artist_metadata_id), 0)
	FROM albums al
	JOIN artist_metadata am ON am.id = al.artist_metadata_id
	LEFT JOIN compilation_series_albums csa ON csa.album_id = al.id
	LEFT JOIN compilation_series cs ON cs.id = csa.compilation_series_id
`

// ListAlbums lists every tracked album, newest first.
func ListAlbums(ctx context.Context, q Queryer) ([]AlbumSummary, error) {
	return queryAlbumSummaries(ctx, q, albumSummaryQuery+` ORDER BY al.added DESC`)
}

// ListRecentAlbums lists the most recently added albums, for the home
// dashboard's "recently added" strip.
func ListRecentAlbums(ctx context.Context, q Queryer, limit int) ([]AlbumSummary, error) {
	return queryAlbumSummaries(ctx, q, albumSummaryQuery+` ORDER BY al.added DESC LIMIT ?`, limit)
}

func queryAlbumSummaries(ctx context.Context, q Queryer, query string, args ...any) ([]AlbumSummary, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list albums: %w", err)
	}
	defer rows.Close()

	var albums []AlbumSummary
	for rows.Next() {
		var a AlbumSummary
		var images string
		if err := rows.Scan(&a.ID, &a.ArtistMetadataID, &a.ArtistName, &a.Title, &a.Year, &a.Path, &a.Monitored, &a.Added,
			&a.CompilationSeriesName, &a.CompilationSeqNumber, &images, &a.RootFolderID); err != nil {
			return nil, fmt.Errorf("scan album summary: %w", err)
		}
		a.PosterURL = firstOrEmpty(unmarshalStringSlice(images))
		a.ArtistSlug, a.Slug = titleutil.Slug(a.ArtistName), titleutil.Slug(a.Title)
		albums = append(albums, a)
	}
	applyPosters(ctx, q, "album", len(albums), func(i int) int64 { return albums[i].ID }, func(i int, url string) { albums[i].PosterURL = url })
	return albums, rows.Err()
}

// ArtistSummary is a read-side view of one tracked artist. Unlike
// AlbumSummary (joined from albums, which only surfaces artists that
// already have at least one album), this lists every artist row directly
// - including one just added with no albums yet, which is exactly the
// state a user needs to browse that artist's MusicBrainz discography and
// pick albums to add.
type ArtistSummary struct {
	ID               int64
	ArtistMetadataID int64
	RootFolderID     int64
	QualityProfileID sql.NullInt64
	Name             string
	Path             sql.NullString
	Monitored        bool
	Added            time.Time
	PosterURL        string
}

// AlbumDetail is the full read-side view of one album for its detail page
// - unlike AlbumSummary, includes descriptive metadata the detail page
// renders (overview/genres/ratings/images/quality profile).
type AlbumDetail struct {
	AlbumSummary
	Overview           sql.NullString
	AlbumType          string
	Genres             []string
	Ratings            map[string]float64
	PosterURL          string
	ArtistID           sql.NullInt64 // artists.id, for linking back to /music/artists/{id}/albums
	QualityProfileID   sql.NullInt64
	QualityProfileName sql.NullString
}

// GetAlbumDetail fetches albumID's full detail view. found is false if no
// album with that id exists. Albums have no separate per-instance table
// (unlike movies/series/artists) - quality_profile_id lives on artists,
// not albums, so it's joined via the album's own artist.
func GetAlbumDetail(ctx context.Context, q Queryer, albumID int64) (AlbumDetail, bool, error) {
	var d AlbumDetail
	var genres, images, ratings string
	err := q.QueryRowContext(ctx, `
		SELECT al.id, al.artist_metadata_id, am.name, al.title,
		       CAST(strftime('%Y', al.release_date) AS INTEGER), al.path, al.monitored, al.added,
		       COALESCE(cs.name, ''), csa.sequence_number,
		       al.overview, al.album_type, al.genres, al.images, al.ratings,
		       a.id, a.quality_profile_id, qp.name
		FROM albums al
		JOIN artist_metadata am ON am.id = al.artist_metadata_id
		LEFT JOIN compilation_series_albums csa ON csa.album_id = al.id
		LEFT JOIN compilation_series cs ON cs.id = csa.compilation_series_id
		LEFT JOIN artists a ON a.artist_metadata_id = al.artist_metadata_id
		LEFT JOIN quality_profiles qp ON qp.id = a.quality_profile_id
		WHERE al.id = ?
	`, albumID).Scan(&d.ID, &d.ArtistMetadataID, &d.ArtistName, &d.Title,
		&d.Year, &d.Path, &d.Monitored, &d.Added,
		&d.CompilationSeriesName, &d.CompilationSeqNumber,
		&d.Overview, &d.AlbumType, &genres, &images, &ratings,
		&d.ArtistID, &d.QualityProfileID, &d.QualityProfileName)
	if errors.Is(err, sql.ErrNoRows) {
		return AlbumDetail{}, false, nil
	}
	if err != nil {
		return AlbumDetail{}, false, fmt.Errorf("get album detail %d: %w", albumID, err)
	}
	d.Genres = unmarshalStringSlice(genres)
	d.Ratings = unmarshalRatings(ratings)
	d.PosterURL = firstOrEmpty(unmarshalStringSlice(images))
	return d, true, nil
}

// UpdateAlbumMonitored sets albumID's monitored flag - the album detail
// page's clickable Monitored/Unmonitored chip. See UpdateMovieMonitored's
// comment for why this is separate from UpsertAlbum's one-time set.
func UpdateAlbumMonitored(ctx context.Context, q Queryer, albumID int64, monitored bool) error {
	if _, err := q.ExecContext(ctx, `UPDATE albums SET monitored = ? WHERE id = ?`, monitored, albumID); err != nil {
		return fmt.Errorf("update album %d monitored: %w", albumID, err)
	}
	return nil
}

// TrackDetail is one track row for an album detail page's track list -
// HasFile mirrors TrackImportInfo's track_file_id IS NOT NULL pattern
// (internal/store/import.go's FindImportRelease), applied here with the
// richer fields a detail page (rather than the import pipeline) needs.
type TrackDetail struct {
	ID           int64
	MediumNumber int
	TrackNumber  string
	Title        string
	DurationMs   sql.NullInt64
	HasFile      bool
	FileSize     sql.NullInt64
	Quality      releaseparse.FileQuality
	MediaInfo    mediainfo.Info
}

// GetAlbumIDForTrack resolves trackID's owning album - needed by
// ImportService.importTrack to resolve the destination folder
// (ResolveAlbumPath takes an album id, not a track id).
func GetAlbumIDForTrack(ctx context.Context, q Queryer, trackID int64) (int64, error) {
	var albumID int64
	err := q.QueryRowContext(ctx, `
		SELECT ar.album_id FROM tracks t JOIN album_releases ar ON ar.id = t.album_release_id WHERE t.id = ?
	`, trackID).Scan(&albumID)
	if err != nil {
		return 0, fmt.Errorf("get album id for track %d: %w", trackID, err)
	}
	return albumID, nil
}

// ListTracksForAlbum lists albumID's tracks in play order, picking the
// same "which album_releases row to use" release as FindImportRelease
// (internal/store/import.go) - see that function's comment for the
// monitored/most-tracks/lowest-id tie-break, since albums can have
// multiple releases/editions with no canonical-release flag.
func ListTracksForAlbum(ctx context.Context, q Queryer, albumID int64) ([]TrackDetail, error) {
	var releaseID int64
	err := q.QueryRowContext(ctx, `
		SELECT ar.id
		FROM album_releases ar
		WHERE ar.album_id = ?
		ORDER BY ar.monitored DESC,
		         (SELECT COUNT(*) FROM tracks t WHERE t.album_release_id = ar.id) DESC,
		         ar.id ASC
		LIMIT 1
	`, albumID).Scan(&releaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find release for album %d: %w", albumID, err)
	}

	rows, err := q.QueryContext(ctx, `
		SELECT t.id, t.medium_number, t.track_number, t.title, t.duration_ms,
		       t.track_file_id IS NOT NULL, tf.size, COALESCE(tf.quality, '{}'), COALESCE(tf.media_info, '{}')
		FROM tracks t
		LEFT JOIN track_files tf ON tf.id = t.track_file_id
		WHERE t.album_release_id = ?
		ORDER BY t.medium_number ASC, t.track_number ASC
	`, releaseID)
	if err != nil {
		return nil, fmt.Errorf("list tracks for release %d: %w", releaseID, err)
	}
	defer rows.Close()

	var tracks []TrackDetail
	for rows.Next() {
		var t TrackDetail
		var quality, mediaInfo string
		if err := rows.Scan(&t.ID, &t.MediumNumber, &t.TrackNumber, &t.Title, &t.DurationMs,
			&t.HasFile, &t.FileSize, &quality, &mediaInfo); err != nil {
			return nil, fmt.Errorf("scan track detail: %w", err)
		}
		t.Quality = unmarshalFileQuality(quality)
		t.MediaInfo = mediainfo.Decode(mediaInfo)
		tracks = append(tracks, t)
	}
	return tracks, rows.Err()
}

// ListArtists lists every tracked artist, newest first.
func ListArtists(ctx context.Context, q Queryer) ([]ArtistSummary, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT a.id, a.artist_metadata_id, a.root_folder_id, a.quality_profile_id, am.name, a.path, a.monitored, a.added, am.images
		FROM artists a JOIN artist_metadata am ON am.id = a.artist_metadata_id
		ORDER BY a.added DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list artists: %w", err)
	}
	defer rows.Close()

	var artists []ArtistSummary
	for rows.Next() {
		var a ArtistSummary
		var images string
		if err := rows.Scan(&a.ID, &a.ArtistMetadataID, &a.RootFolderID, &a.QualityProfileID, &a.Name, &a.Path, &a.Monitored, &a.Added, &images); err != nil {
			return nil, fmt.Errorf("scan artist summary: %w", err)
		}
		a.PosterURL = firstOrEmpty(unmarshalStringSlice(images))
		artists = append(artists, a)
	}
	applyPosters(ctx, q, "artist", len(artists), func(i int) int64 { return artists[i].ID }, func(i int, url string) { artists[i].PosterURL = url })
	return artists, rows.Err()
}

// FindAlbumIDBySlugs resolves an address like /music/albums/daft-punk/homework
// to an album. With more than one match, the oldest entry wins.
func FindAlbumIDBySlugs(ctx context.Context, q Queryer, artistSlug, albumSlug string) (int64, bool, error) {
	var id int64
	err := q.QueryRowContext(ctx, `
		SELECT al.id FROM albums al JOIN artist_metadata am ON am.id = al.artist_metadata_id
		WHERE am.clean_name = ? AND al.clean_title = ?
		ORDER BY al.id LIMIT 1`, titleutil.CleanTitle(artistSlug), titleutil.CleanTitle(albumSlug)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("find album by slug %q/%q: %w", artistSlug, albumSlug, err)
	}
	return id, true, nil
}

// DeleteAlbum removes an album, its releases, tracks, file rows and grab
// history. Files on disk are the caller's business.
func DeleteAlbum(ctx context.Context, q Queryer, albumID int64) error {
	for _, stmt := range []string{
		`DELETE FROM grabs WHERE album_id = ?`,
		`DELETE FROM track_files WHERE id IN (SELECT t.track_file_id FROM tracks t JOIN album_releases r ON r.id = t.album_release_id WHERE r.album_id = ? AND t.track_file_id IS NOT NULL)`,
		`DELETE FROM tracks WHERE album_release_id IN (SELECT id FROM album_releases WHERE album_id = ?)`,
		`DELETE FROM album_releases WHERE album_id = ?`,
		`DELETE FROM compilation_series_albums WHERE album_id = ?`,
		`DELETE FROM albums WHERE id = ?`,
	} {
		if _, err := q.ExecContext(ctx, stmt, albumID); err != nil {
			return fmt.Errorf("delete album %d: %w", albumID, err)
		}
	}
	return nil
}

// UpdateTrackFilePath records a track file's new name after an organize.
// The path is relative to its album's folder, as ListTrackFilesForAlbum
// returns it.
func UpdateTrackFilePath(ctx context.Context, q Queryer, fileID int64, relativePath string) error {
	if _, err := q.ExecContext(ctx, `UPDATE track_files SET relative_path = ? WHERE id = ?`, relativePath, fileID); err != nil {
		return fmt.Errorf("update track_file %d path: %w", fileID, err)
	}
	return nil
}
