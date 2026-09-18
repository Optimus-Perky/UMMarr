# This is a Work In Progress - use at your own risk

# UMMarr — Unified Media Manager

A web interface for managing Movies, TV, and Music with a custom backend,
all written in Go — a from-scratch replacement for running Sonarr (TV),
Radarr (movies), and Lidarr (music) as three separate .NET containers. See
the full planning document for context and rationale.

This covers the **database schema**, the **metadata-provider layer**
(fetch + merge), the **sync service** (persisting merged metadata into the
database), the **folder-path builder** (turning naming templates into
actual on-disk paths), the **web UI** (server-rendered Go + htmx),
**indexer search + grabbing** (Torznab/Newznab indexers, Prowlarr sync + Deluge), **importers** (copying
a finished download into the library), and **auth** (a login page + a
webhook token) - the full add→search→grab→import loop, deployable via
Docker (see Deployment, below).

## Status

**Schema** is built, migrated, and verified against five golden-path
scenarios (`internal/store/fixtures/fixtures_test.go`):

1. Normal artist → album → tracks.
2. A Various Artists compilation grouped under a named `compilation_series`
   (e.g. "Now That's What I Call Music"), with per-track artist resolution
   working independently of the album-level (Various Artists) artist.
3. A single, confirmed to resolve through the exact same `albums` shape as a
   full album — no special-casing needed.
4. A movie with external IDs from multiple providers (TMDB/IMDb/OMDb) at
   once.
5. A TV series with real `seasons` rows (not embedded JSON).

**Metadata providers** (`internal/metadata/`) fetch from TMDB, TVMaze,
MusicBrainz, and OMDb, and merge results from more than one source into a
single record with per-field provenance. TVDB, Discogs, and TheAudioDB are
deliberately deferred — see the package doc comments in
`internal/metadata/providers/{tmdb,tvmaze,musicbrainz,omdb}` for why, and
`internal/metadata/merge/` for the merge engine. Verified via:

1. Fixture TMDB+OMDb responses merging into one record, both external IDs
   (plus `imdb`, cross-referenced from TMDB) written and idempotently
   re-upsertable, provenance correctly attributing `overview→tmdb` and
   `ratings→omdb`.
2. Fixture TMDB+TVMaze season/episode lists with a deliberate count
   mismatch, confirming the union produces the superset and the mismatch
   is flagged in `MergeReport.Conflicts`.
3. Live (no API key needed) calls to TVMaze and MusicBrainz — including a
   real MusicBrainz series-relationship lookup confirming "Now That's What
   I Call Music" parses correctly into the same shape that populates
   `compilation_series`.

```
go test ./...                    # unit tests, no network/keys needed
go test -tags=integration ./...  # + live TVMaze/MusicBrainz calls
```

TMDB and OMDb both need a free API key you obtain yourself (see Running,
below) — nothing here has been tested against them live, only against
fixture responses matching their documented shapes, since no key should
ever be typed into this environment.

**The sync service** (`internal/sync/`) ties fetch and merge to actual
persistence: `MovieService`/`SeriesService`/`MusicService`, each with
`Add*`/`Refresh` methods that fetch, merge, and write inside one
transaction (`internal/store/tx.go`'s `WithTx`). Every write is an upsert
keyed on the entity's anchor provider id (TMDB for movies/series,
MusicBrainz for artists/albums/releases) via `internal/store`'s
`Upsert<Entity>` functions, so `Add` and `Refresh` share the exact same
code path — idempotent by construction, not by a separate check. User-
owned settings (`monitored`, quality profile, root folder) are only ever
set on first creation; a refresh never silently overwrites them. Verified
via:

1. Store-layer idempotency tests (`internal/store/{movies,music}_test.go`)
   — upserting the same external ID twice updates in place, and a
   simulated user "unmonitor" survives a refresh.
2. A live (no API key needed) capstone integration test
   (`internal/sync/music_integration_test.go`) running the **full real
   pipeline** end to end: add Various Artists, find a real "Now That's
   What I Call Music" entry via MusicBrainz, add the album, and confirm
   both `compilation_series` is populated and at least one synced track
   resolves to its own real performing artist rather than "Various
   Artists" itself.
3. **Update**: TMDB and OMDb keys are now in place. `internal/metadata/
   providers/{tmdb,omdb}/integration_test.go` verify both live (skipped
   via `t.Skip` if their env var isn't set), and
   `internal/sync/{movie,series}_integration_test.go` prove the full
   pipeline end to end: a real movie (Inception) merges ratings from all
   four providers at once and resolves to the correct path; a real series
   (Breaking Bad) syncs 6 real seasons and 71 real episodes with TMDB and
   TVMaze merged. All three media types are now verified against real
   data, not just fixtures.

Two real gaps found and fixed while building this: `MergeRelease` was
silently dropping the release's own MusicBrainz id (nothing anchored
`album_releases` rows for upserting), and a track-artist resolution step
was calling `SearchArtist` with an MBID as if it were a text query — fixed
by adding a proper `GetArtist(mbid)` lookup to the MusicBrainz client.

**The folder-path builder** (`internal/pathbuilder/` + `internal/store/
paths.go`) resolves the naming templates already seeded in `naming_config`
(migration 00001) into real paths, wired into `UpsertMovie`/`UpsertSeries`/
`UpsertArtist`/`SetAlbumPath` so a path gets set the moment something's
added — never touched again on a refresh, so a file you've manually
reorganized stays put. `pathbuilder` itself is pure and DB-free (same
shape as `internal/titleutil`): it substitutes `{Token Name}` and
`{token:00}`-style padded tokens, and sanitizes each path *segment*
separately after splitting a template on `/` — done in that order
specifically so a resolved value containing `/` (e.g. a title with a slash
in it) gets sanitized away rather than mistaken for an extra directory
level. Various Artists detection has no schema flag to key off — it
compares an artist's MusicBrainz id against the well-known VA MBID via the
existing `GetExternalID`, not a fragile name match. Verified via:

1. Pure `pathbuilder` unit tests: substitution, zero-padding, every
   Windows-reserved character, multi-segment templates, and the
   slash-in-token-value case specifically.
2. Real temp-SQLite tests for all four branches from the original
   folder-path design: a movie, a series with `season_folder` both
   on/off (plus a user override), a normal artist/album, a single
   (confirmed identical shape to a normal album), a VA album linked to a
   compilation series, and a VA album with no series link.
3. A real ordering bug found while wiring this up: a VA album's path
   depends on its `compilation_series_albums` link, which can only exist
   *after* the album row does — so path-setting couldn't stay inside
   `UpsertAlbum` like it does for the other three entities. Fixed by
   having `UpsertAlbum` report whether it just created a row, and having
   `internal/sync`'s `AddAlbumByMBID` call the new `SetAlbumPath`
   explicitly, after any series link is in place.

## Design highlights

- **Metadata/instance split**, applied uniformly across movies, series, and
  artists: an immutable `*_metadata` row (shared, provider-sourced) decoupled
  from a per-instance tracked row (path, monitoring, quality profile) —
  carried forward from Radarr's `Movie`/`MovieMetadata` pattern.
- **Generic `external_ids` table** instead of hardcoded per-provider columns
  (the approach Sonarr's `Series` table uses today) — any entity can carry
  IDs from multiple metadata providers at once, which is required to
  merge/fall back across sources rather than depend on a single hosted
  proxy per media type.
- **`seasons` is a real table**, not embedded JSON like Sonarr — the one
  deliberate deviation from "movies/TV are fine as-is", needed so season
  lists from different providers can be merged as rows rather than inside a
  JSON blob.
- **Per-track `artist_metadata_id`**, unchanged from Lidarr — the mechanism
  that already makes Various Artists compilations resolve correctly without
  any special-case code: the album's artist can be "Various Artists" while
  each track points at its own real performing artist.
- **`compilation_series`** — new, doesn't exist in Lidarr today. Groups VA
  albums into a named franchise via a join table (zero-or-one series
  membership per album, `sequence_number` for ordering), sourced from
  MusicBrainz's native Series entity where available, with a manual
  fallback.

## Project layout

```
cmd/ummarr/              main entrypoint, cobra CLI (migrate; serve/import land later)
internal/config/         env-var config (API keys) - not for the assistant to fill in, see below
internal/domain/         pure entities (no DB tags) — not yet populated
internal/store/
  migrations/            goose SQL migrations, embedded into the binary
  queries/               sqlc query files — not yet populated
  fixtures/              golden-path round-trip tests
  metadata_upserts.go    upsert helpers for external_ids / metadata_field_provenance
internal/metadata/       provider result types (Field[T], MovieMetadata, ...)
  providers/
    httpclient/          shared rate-limited HTTP helper
    tmdb/ tvmaze/ musicbrainz/ omdb/   built
    tvdb/ discogs/ theaudiodb/ imdb/   deferred/not applicable, stay empty
  merge/                 the merge engine + one adapt_<provider>.go per provider
internal/sync/           orchestrates fetch -> merge -> persist per media type
internal/titleutil/      clean/sort title computation (NOT NULL schema columns)
internal/pathbuilder/    pure template resolution + sanitization
  (path.go in internal/store holds the DB-aware Resolve*Path functions
  that call into pathbuilder - see Design highlights)
internal/importer/       arr-database importers — not yet populated
  sonarr/, radarr/, lidarr/
internal/api/            web UI + HTTP handlers (server-rendered Go + htmx)
  templates/             html/template pages + htmx partials, embedded
  static/                vendored htmx.min.js + style.css, embedded
```

## The web UI

Server-rendered `html/template` pages with [htmx](https://htmx.org) for
interactivity — deliberately not a JS/React SPA, to preserve the
single-static-binary, no-npm-CVE-surface goal that was central to
choosing Go for this whole project. Separate pages per media type
(Movies/TV/Music each show only their own library, no shared scrolling
page), plus a Home dashboard with a 4-item "recently added" strip per
category. Each library page has an inline search-as-you-type Add panel
against the real provider APIs; adding something calls straight into
`internal/sync`'s services and redirects back to the refreshed page via
htmx's `HX-Redirect` response header.

A **real bug found and fixed** while building this: `html/template`'s
`{{define "content"}}` isn't scoped per file — it's one shared name
across everything parsed together, so parsing all five page templates
into a single `*template.Template` made each page silently render
whichever file's `content` block happened to parse last (every page
showed as "TV Series" during initial testing). Fixed by cloning the base
layout once per page (`internal/api/api.go`'s `parsePages`) so each
page's `content` definition stays isolated.

Root folders and quality profiles have no defaults — `/settings` is
required before anything can be added, matching how Sonarr/Radarr/Lidarr
themselves require configuring a root folder first.

Verified live end-to-end via the real HTTP endpoints (curl, not just Go
tests — no browser available in this environment) against real
TMDB/TVMaze/MusicBrainz data: added Inception (with merged multi-provider
ratings), Breaking Bad (real seasons/episodes), Daft Punk + Homework
(correct path and release year), and a real "Now That's What I Call
Music" compilation rendering correctly in the Various-Artists/series tree
view. A related gap found and fixed along the way: `MergeAlbum` never
actually carried a release-group's `FirstReleaseDate` through at all
(the field didn't exist on `albumSource`), so every album's year was
silently missing regardless of what MusicBrainz actually returned.

## Running

```
go run ./cmd/ummarr migrate --db ummarr.db   # apply pending migrations only
go run ./cmd/ummarr serve --db ummarr.db     # run the web UI (default :8080)
go run ./cmd/ummarr episode-names --db ummarr.db "MobLand"   # one task, no UI
go run ./cmd/ummarr rename-preview --db ummarr.db            # what Organize would change
```

`migrate` applies any pending migrations to the given SQLite file (created
if it doesn't exist). `episode-names` runs the Find episode names task
headlessly - every series with unnamed episodes, or the one whose title
matches the argument (`--season N` narrows it further). It is safe to run
against a live database, and against a running container:
`docker exec ummarr /ummarr episode-names --db /config/ummarr.db "MobLand"`.
`serve` applies migrations too, then starts the web server -
set `UMMARR_LISTEN_ADDR` to change the bind address (default `:8080`).
Postgres support is planned via `jackc/pgx/v5` but not yet wired up —
SQLite (via `modernc.org/sqlite`, pure Go, no cgo) is the only driver
right now, keeping the single-static-binary build simple.

### Metadata provider API keys

TMDB and OMDb both need a free key you get yourself — sign up at
themoviedb.org (Settings → API, request a "Developer" key, then use the v4
Read Access Token) and omdbapi.com/apikey.aspx, then set:

```
export UMMARR_TMDB_TOKEN=...
export UMMARR_OMDB_API_KEY=...
```

(Configured and verified live as of this pass — see the integration
tests above. The actual key file isn't part of this repo.)

TVMaze and MusicBrainz need no key. TMDB attribution ("This product uses
the TMDB API but is not endorsed or certified by TMDB") is contractually
required and must appear in the web UI once it exists.

## Deployment (Docker)

```
cp .env.example .env    # then fill in real values - .env is git/docker-ignored
docker compose up -d --build
```

Edit `docker-compose.yml`'s `volumes:` section first - the example maps a
host media/downloads root to `/data` inside the container. That mount
point is load-bearing, not arbitrary: it must match the SAME
container-internal path your download client itself uses (check Deluge's
`core.conf` - `download_location`/`move_completed_path` should both be
under `/data/...` from *Deluge's own* container's point of view, i.e. it
has the same host directory mounted the same way). UMMarr's importer
copies files using the path Deluge reports over its API (e.g.
`/data/completed/<release>/movie.mkv`) - if UMMarr doesn't have that
identical host directory mounted at that identical path, it can't find
the file to copy at all. Once running, point UMMarr's own Settings → Root
Folders at subpaths under that same mount - `/data/Movies`, `/data/TV`,
`/data/Music`.

**Image**: multi-stage build - `golang:1.26-alpine` compiles a fully
static binary (`CGO_ENABLED=0`; `modernc.org/sqlite` is pure Go, no cgo
needed), then the runtime image is
`gcr.io/distroless/static-debian12:nonroot` - no shell, no package
manager, nothing but the binary, CA certs, and a non-root user. This
combination is chosen specifically to minimize CVE surface: there is no
apk/apt package inventory for a scanner to flag, and Go's own dependency
tree was confirmed clean via
[`govulncheck`](https://go.dev/security/vuln) (`go install
golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...`) before
this Dockerfile was written. Independently re-run `docker scout cves
ummarr:latest` or `trivy image ummarr:latest` after building for a second,
OS-layer opinion - that step couldn't be run here (no Docker in this dev
container), so treat it as unverified until you've built and scanned the
real image yourself.

### Auth

The web UI has no login unless `UMMARR_AUTH_PASSWORD` (in `.env`) is set
- highly recommended once this is reachable beyond your own machine.
Sessions are AES-256-GCM encrypted cookies (`internal/auth`), so there's
no server-side session store; set `UMMARR_SESSION_KEY` too (`openssl rand
-base64 32`) so sessions survive a container restart - if left unset, a
random key is generated at startup and every session is invalidated on
the next restart/redeploy.

### Deluge "on complete" webhook

Rather than waiting for UMMarr's own once-a-minute fallback poll, Deluge
can push a "finished" notification the instant a torrent completes, via
its **Execute** plugin (Deluge → Preferences → Plugins → enable
"Execute" → Preferences → Execute → add a "Torrent Complete" event
running a command):

```
curl -s -X POST "http://<ummarr-host>:8080/downloads/%I/completed?token=<UMMARR_WEBHOOK_TOKEN>"
```

`%I` is Deluge's own placeholder for the completed torrent's infohash.
Omit `?token=...` entirely if `UMMARR_WEBHOOK_TOKEN` isn't set in your
`.env` (the route is then unauthenticated, matching its pre-auth
behavior) - otherwise it must match exactly, since this route is exempt
from the cookie-based login (a shell script can't hold a browser
session) and only this token gates it.
