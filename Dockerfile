# syntax=docker/dockerfile:1

# --- Build stage -------------------------------------------------------
# Pinned to a specific Go minor version matching go.mod, not "latest",
# for reproducible builds. Alpine here only matters for build-time tools
# (nothing from this stage ships in the final image).
FROM golang:1.26-alpine AS build

WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 for a fully static binary - modernc.org/sqlite is pure Go
# (no cgo sqlite driver needed), so this carries no functionality cost and
# removes any dynamic-linking attack surface. -trimpath/-ldflags="-s -w"
# strip local build paths and debug symbols from the binary.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/ummarr ./cmd/ummarr

# Pre-create the config directory here (with the runtime image's nonroot
# uid/gid) since the distroless final stage has no shell to mkdir with -
# Docker preserves this ownership when a fresh named volume/bind mount is
# first mounted over it. /config holds only ummarr.db - the actual media
# library and a download client's own files are a separate mount (see
# docker-compose.yml), deliberately not baked into this image at all.
RUN mkdir -p /out/config && chown 65532:65532 /out/config

# --- Runtime stage -------------------------------------------------------
# distroless "static, nonroot" - no shell, no package manager, no libc
# beyond what's statically linked in, nothing but the CA cert bundle and
# the binary itself. Google rebuilds these regularly against current
# Debian security patches; there is effectively nothing here for a CVE
# scanner to find (no apk/apt packages, no busybox, no shell). Confirmed
# via `go install golang.org/x/vuln/cmd/govulncheck@latest && govulncheck
# ./...` against this project's actual Go dependencies before this image
# was built (see the project README) - independently re-run
# `docker scout cves` or `trivy image` against the built image if you
# want a second, OS-layer confirmation.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/ummarr /ummarr
# FFprobe for Analyze Video Files and Analyze Audio Files: a static build, so
# it runs without libc on distroless. Pinned by digest.
COPY --from=mwader/static-ffmpeg:7.1.1@sha256:11a44711684c0b9f754c047dcd64235b8b52deab251bd0e0a86f22faa160749c /ffprobe /usr/local/bin/ffprobe
COPY --from=build --chown=nonroot:nonroot /out/config /config

# distroless:nonroot already runs as uid/gid 65532 by default; stated
# explicitly here for clarity.
USER nonroot:nonroot

EXPOSE 8080
VOLUME ["/config"]

# Distroless has no shell/curl/wget, so the healthcheck is a real
# subcommand of the binary itself (cmd/ummarr's "healthcheck") rather
# than the usual CMD-shell-out-to-curl pattern.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/ummarr", "healthcheck"]

ENTRYPOINT ["/ummarr"]
CMD ["serve", "--db", "/config/ummarr.db"]
