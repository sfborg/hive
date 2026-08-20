# hive — sfga taxonomic editor
# Install `just` from https://github.com/casey/just

# Default: show available recipes.
default:
    @just --list

# Build hive and the mkdemo helper into ./bin/
build:
    @mkdir -p bin
    go build -o bin/hive .
    go build -o bin/mkdemo ./tools/mkdemo

# Install the hive binary to $GOPATH/bin (or GOBIN).
install:
    go install .

# Run the full test suite with race detection.
test:
    go test -race -count=1 ./...

# Verbose test output (for debugging a single flake).
test-v:
    go test -v -race -count=1 ./...

# gofmt every package.
fmt:
    gofmt -w .

# Static analysis via `go vet`.
vet:
    go vet ./...

# Format + vet + tidy — cheap pre-commit hygiene.
check: fmt vet
    go mod tidy

# Tidy go.mod on its own.
tidy:
    go mod tidy

# Generate a small demo archive at ./demo.db (or the given path).
# Idempotent: deletes the file first so re-runs produce a clean archive.
demo path='./demo.db':
    go run ./tools/mkdemo {{path}}

# Build, then open `hive view` on the demo archive (or the given archive).
# Regenerates the demo if the path is ./demo.db and it doesn't exist yet.
view path='./demo.db': build
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "{{path}}" = "./demo.db" ] && [ ! -f "{{path}}" ]; then
        just demo
    fi
    ./bin/hive view {{path}}

# Build, then run `hive edit` on the demo archive (or the given archive).
# Auto-demos ./demo.db if it doesn't exist yet. Actor comes from HIVE_ORCID
# when set; otherwise the binary logs a warning and stamps "local".
edit path='./demo.db': build
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "{{path}}" = "./demo.db" ] && [ ! -f "{{path}}" ]; then
        just demo
    fi
    ./bin/hive edit {{path}}

# Build, then run `hive serve` on the demo archive (or the given archive).
# Auto-demos ./demo.db if it doesn't exist yet.
serve path='./demo.db': build
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "{{path}}" = "./demo.db" ] && [ ! -f "{{path}}" ]; then
        just demo
    fi
    ./bin/hive serve {{path}}

# Stop any running hive serve, rebuild, and start a fresh one. Useful
# during frontend iteration: internal/wui/dist/ is embedded into the binary
# via //go:embed, so JS/CSS changes only reach the browser after a rebuild.
#
# Uses `pkill -x hive` (exact process-name match) rather than a
# full-command grep — greping for "bin/hive serve" also matches the
# calling shell's own argv and would kill this recipe mid-flight.
restart-serve path='./demo.db':
    #!/usr/bin/env bash
    set -euo pipefail
    pkill -x hive 2>/dev/null || true
    # Give the OS a moment to release the port. modernc.org/sqlite closes
    # cleanly so this is generally instant; the sleep is a belt-and-braces
    # guard against WAL cleanup racing the next bind.
    sleep 0.5
    just serve {{path}}

# Pass arbitrary args through to the compiled binary.
# Example: `just run view /tmp/other.db`
run *ARGS: build
    ./bin/hive {{ARGS}}

# Fetch the pinned Lit bundle and print its SHA-256 for verification.
# The recipe deliberately does NOT auto-update
# internal/wui/dist/vendor/README.md — hash-mismatch reviews are how we
# catch supply-chain surprises. Compare the printed SHA against the README
# before committing an upgrade.
vendor-lit:
    #!/usr/bin/env bash
    set -euo pipefail
    url="https://cdn.jsdelivr.net/gh/lit/dist@3/all/lit-all.min.js"
    dest="internal/wui/dist/vendor/lit-3.x.x.min.js"
    tmp="$(mktemp)"
    echo "fetching $url"
    curl -sSfL -o "$tmp" "$url"
    sha=$(sha256sum "$tmp" | awk '{print $1}')
    size=$(stat -c%s "$tmp")
    echo ""
    echo "SHA-256: $sha"
    echo "Size:    $size bytes"
    echo ""
    mv "$tmp" "$dest"
    echo "wrote $dest — verify SHA-256 against internal/wui/dist/vendor/README.md before committing"

# Remove build outputs and demo archives (including SQLite WAL sidecars).
clean:
    rm -rf bin
    rm -f demo.db demo.db-wal demo.db-shm

# ---------- docker / compose ----------
#
# The reference-ingest pipeline runs a PDF through GROBID (ML-based
# TEI extraction) before hive derives JATS and downstream data. GROBID
# is Java + trained models — heavy for a curator's laptop. docker
# compose spins it up alongside hive so operators don't have to run
# GROBID by hand. See docker-compose.yml + REFERENCE_PDF_PLAN.md.

# Build the hive Docker image (multi-stage, static binary, distroless
# runtime). Vendors deps first so gsvalidator's local `replace`
# resolves inside the container (Docker's build context can't see
# host paths outside the repo). Passes the current git short-sha as
# HIVE_VERSION.
docker-build:
    go mod vendor
    docker build \
        --build-arg HIVE_VERSION="$(git rev-parse --short HEAD)" \
        -t hive:local \
        .

# Bring up hive + GROBID via docker compose. First run pulls GROBID's
# ~2 GB image and takes ~60s to warm up the ML models; watch
# `just docker-logs` for the "ready" line before hitting the WUI.
# Data directory (./data) is created if missing so `docker compose up`
# works from a clean checkout. Depends on docker-build so the vendor
# step runs before compose's internal `build: .` fires.
docker-up: docker-build
    @mkdir -p data
    docker compose up -d
    @echo ""
    @echo "  hive:   http://localhost:2365"
    @echo "  grobid: http://localhost:8070"
    @echo ""
    @echo "  logs:   just docker-logs"
    @echo "  stop:   just docker-down"

# Tail logs from both services (Ctrl-C to detach — services keep
# running).
docker-logs:
    docker compose logs -f

# Stop and remove the compose stack. Preserves volumes (./data
# stays; the mounted archive is safe).
docker-down:
    docker compose down

# Bring up JUST the GROBID service — useful during hive development
# when you want `just serve` running your local binary against a
# compose-managed GROBID. Point your local hive at it with
# HIVE_GROBID_URL=http://localhost:8070.
docker-grobid:
    docker compose up -d grobid
    @echo ""
    @echo "  grobid: http://localhost:8070"
    @echo ""
    @echo "  run local hive against it with:"
    @echo "    HIVE_GROBID_URL=http://localhost:8070 just serve"


