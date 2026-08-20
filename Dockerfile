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
# Runtime image. Distroless static — glibc-free, shell-free. If you
# need to debug inside the container, swap this for
# gcr.io/distroless/static-debian12:debug (adds busybox).
FROM gcr.io/distroless/static-debian12:nonroot

# Sidecar directories (<archive>.library/) live next to their .db;
# operators mount an archive-hosting volume at /data and point the
# hive command at /data/<name>.db.
WORKDIR /data

# Copy the compiled binary from the build stage.
COPY --from=build /out/hive /usr/local/bin/hive

# HTTP port. Change via `hive serve --bind :NNNN` if a different
# port is needed; the container port then has to match.
EXPOSE 2365

# Default command runs `hive serve /data/archive.db` — operators
# override with a real archive name via docker-compose or `docker run`.
ENTRYPOINT ["/usr/local/bin/hive"]
CMD ["serve", "--bind", "0.0.0.0:2365", "/data/archive.db"]
