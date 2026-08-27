# Multi-stage build for hive. Compiled binary lands in a distroless
# runtime image so the final container has no shell / package
# manager / etc. — smaller attack surface, ~15 MB image.
#
# Build args:
#   HIVE_VERSION — passed to -ldflags for version string (optional).

FROM golang:1.26-alpine AS build

ARG HIVE_VERSION=dev

WORKDIR /src

# Copy the full source tree in one shot. Deps come from the vendor/
# directory (populated on the host by `go mod vendor` — see the
# docker-build recipe in justfile). Vendor mode avoids the network
# entirely at build time and — importantly — resolves the local
# `replace` for gsvalidator that Docker's build context can't see
# on its own.
COPY . .

# CGO off for a static binary (modernc.org/sqlite is pure-Go, no CGO needed).
# -trimpath strips build-machine paths from stack traces.
# -mod=vendor uses the staged vendor/ directory instead of the
# module cache (no `go mod download` needed inside the container).
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -mod=vendor \
    -ldflags="-s -w -X main.version=${HIVE_VERSION}" \
    -o /out/hive \
    .

# ------------------------------------------------------------------
# Runtime image. debian:12-slim (~40 MB) instead of distroless
# because the JATS-validation rule shells out to `xmllint` (from
# libxml2-utils) to validate the augmented-JATS sidecar against the
# hive-jats.rng schema. Distroless has no package manager and no
# libxml2 pre-installed, so copying xmllint + its shared-lib deps
# in from another stage is fragile. debian:12-slim keeps the image
# small enough while giving us a real package manager and a shell
# for debugging.
FROM debian:12-slim

# Install xmllint (libxml2-utils) for JATS RNG validation,
# pdftocairo (poppler-utils) for extracting figure/table image
# crops from source PDFs via GROBID's bounding-box coords, and
# CA certs for outbound HTTPS. Purge apt caches after install to
# keep the image small.
RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		libxml2-utils \
		poppler-utils \
		ca-certificates \
	&& rm -rf /var/lib/apt/lists/*

# Non-root user so the hive process doesn't run as root in the
# container. matches the previous distroless-nonroot behavior.
RUN groupadd --system --gid 65532 hive \
	&& useradd --system --uid 65532 --gid hive --home /data hive

# Sidecar directories (<archive>.library/) live next to their .db;
# operators mount an archive-hosting volume at /data and point the
# hive command at /data/<name>.db.
WORKDIR /data
RUN chown hive:hive /data
USER hive:hive

# Copy the compiled binary from the build stage.
COPY --from=build /out/hive /usr/local/bin/hive

# HTTP port. Change via `hive serve --bind :NNNN` if a different
# port is needed; the container port then has to match.
EXPOSE 2365

# Default command runs `hive serve --create /data/archive.db`:
#   --create               makes fresh installs work without
#                          pre-populating an archive.
#   --bind 0.0.0.0:2365    listens on all container interfaces so
#                          host → container traffic reaches it
#                          (docker-compose maps the host port to
#                          127.0.0.1 so effective exposure stays
#                          loopback).
#   --allow-unauthenticated
#                          bypasses hive's non-loopback-bind safety
#                          guard. Necessary because 0.0.0.0 inside
#                          the container LOOKS like a public bind
#                          but is behind Docker's network isolation
#                          + the compose file's loopback-only port
#                          mapping. Operators exposing hive to a
#                          real public network need to run their
#                          own reverse-proxy + auth layer.
ENTRYPOINT ["/usr/local/bin/hive"]
CMD ["serve", "--create", "--allow-unauthenticated", "--bind", "0.0.0.0:2365", "/data/archive.db"]
