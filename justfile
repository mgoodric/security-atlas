# security-atlas — task runner
# Run `just` (no args) to list available recipes.

set shell := ["bash", "-cu"]

default: list

# List all recipes
list:
    @just --list

# Build all components (Go + frontend)
build: build-go build-frontend

# Build Go binaries (atlas, atlas-cli, atlas-oscal)
build-go:
    go build ./...

# Build frontend workspaces (web, sdk/typescript)
build-frontend:
    npm run build --workspaces --if-present

# Run all tests
test: test-go test-frontend

# Run Go tests (unit only — integration tests behind the `integration` build tag)
test-go:
    go test ./...

# Run integration tests (require Postgres reachable via DATABASE_URL_APP)
test-integration:
    go test -tags=integration -race ./internal/db/...

# Run frontend tests
test-frontend:
    cd web && npm test --if-present

# Run all linters (Go + frontend + Python)
lint: lint-go lint-frontend lint-python

# Lint Go (golangci-lint)
lint-go:
    golangci-lint run ./...

# Lint frontend (eslint)
lint-frontend:
    cd web && npm run lint --if-present

# Lint Python (ruff)
lint-python:
    ruff check .

# Format all code (in-place)
fmt: fmt-go fmt-frontend fmt-python

# Format Go (gofmt + goimports). Module path resolved from go.mod so renames don't drift.
fmt-go:
    gofmt -w .
    goimports -w -local "$(go list -m)" .

# Format frontend (prettier)
fmt-frontend:
    cd web && npm run fmt --if-present

# Format Python (ruff format)
fmt-python:
    ruff format .

# Install pre-commit hooks (commit + push stages)
install-hooks:
    pre-commit install --install-hooks
    pre-commit install --hook-type pre-push --install-hooks
    @echo "pre-commit hooks installed (commit + push). Bad-format commits and pushes will be rejected locally."

# Run pre-commit against the whole tree
hooks-run:
    pre-commit run --all-files

# What CI runs (lint + test + build)
ci: lint test build

# Regenerate every derived logo asset from the canonical candidate-04
# SVG. On-demand, NOT a CI gate (slice 075 P0-A5 — generated assets are
# committed; freshness is a maintainer act tied to logo updates).
#
# Reads:
#   docs/design/logo-candidates/candidate-04/mark.svg          (canonical, full mark)
#   docs/design/logo-candidates/candidate-04/mark-favicon.svg  (simplified favicon variant)
#
# Writes (overwrites in place):
#   web/public/{logo-light,logo-dark}.{svg,png}
#   web/public/{favicon.ico,apple-touch-icon.png,icon-192.png,icon-512.png}
#   web/public/{og-image.png,twitter-card.png}
#   docs-site/docs/assets/{logo-light.svg,logo-dark.svg,favicon.png}
#   docs/images/{logo-light.png,logo-dark.png}
#
# Toolchain: Sharp (transitive of next@^16; npm install must have run
# at the workspace root). No new image-processing dependency added per
# slice 075 AC-10 + P0.
regen-logo:
    node scripts/regen-logo-variants.mjs

# Tidy Go modules and verify no diff
tidy:
    go mod tidy
    git diff --exit-code -- go.mod || (echo "go.mod changed; commit the diff" && exit 1)
    @if [ -f go.sum ]; then git diff --exit-code -- go.sum || (echo "go.sum changed; commit the diff" && exit 1); fi

# ----- Database / migrations -----
#
# DATABASE_URL points at the migration role (superuser-or-BYPASSRLS). Atlas
# uses it for DDL.
# DATABASE_URL_APP points at the application role (NOSUPERUSER NOBYPASSRLS).
# Integration tests use it.
# ATLAS_DEV_URL is a separate ephemeral Postgres for `atlas migrate diff`.

# Apply bootstrap roles, then the versioned SQL migrations in order.
# Plain psql for slice 002 — one migration doesn't justify a versioning tool.
# A real migration runner lands when slice N adds the second migration.
migrate-up:
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/bootstrap/01-roles.sql
    for f in migrations/sql/*.sql; do \
        case "$f" in *.down.sql) ;; *) \
            echo "applying $f"; \
            psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$f"; \
        ;; esac; \
    done

# Roll back: apply the latest .down.sql in reverse-timestamp order.
migrate-down:
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/sql/20260511000000_init.down.sql

# Start a local Postgres 16 in Docker for development
db-up:
    docker run -d --name security-atlas-pg \
        -e POSTGRES_PASSWORD=postgres \
        -e POSTGRES_DB=security_atlas \
        -p 5432:5432 \
        postgres:16-alpine

# Tear down the local Postgres
db-down:
    docker rm -f security-atlas-pg

# Generate sqlc code from the migration schema + queries
sqlc-generate:
    sqlc generate

# Audit every public-schema table with a `tenant_id` column. Fails if any
# such table lacks an RLS policy or FORCE ROW LEVEL SECURITY. Constitutional
# invariant 6 enforcement — see docs/architecture/rls.md. Requires
# DATABASE_URL to point at a role with full pg_catalog visibility
# (atlas_migrate is the canonical choice).
audit-rls:
    ./scripts/audit-rls.sh

# Import an SCF catalog JSON release into Postgres. DATABASE_URL must point
# at a role with INSERT on scf_anchors (atlas_migrate by default).
import-scf path:
    go run ./cmd/atlas-cli catalog import-scf "{{path}}"

# Import a SOC 2 v2017 TSC crosswalk YAML into Postgres (slice 007).
# DATABASE_URL must point at atlas_migrate. The shipped DRAFT mapping file
# is at data/crosswalks/soc2-tsc-2017.yaml — every row is community_draft
# attribution pending HITL spot-check.
import-soc2 path:
    go run ./cmd/atlas-cli catalog import-soc2 "{{path}}"

# ----- Connectors -----

# Build all connector binaries
connector-build:
    go build -o ./bin/aws-connector ./connectors/aws/cmd/aws-connector

# Trigger a connector run. Args: <vendor> <kind>. Required env vars:
#   SECURITY_ATLAS_ENDPOINT  platform gRPC endpoint
#   SECURITY_ATLAS_TOKEN     bearer token (issued via `atlas-cli credentials issue`)
#   AWS_ROLE_ARN             IAM role to assume in the target account
#   AWS_REGION               primary AWS region
#   AWS_ENVIRONMENT          environment tag fallback when Organizations is unavailable
connector-run vendor kind:
    just connector-build
    ./bin/{{vendor}}-connector run \
        --endpoint="$SECURITY_ATLAS_ENDPOINT" \
        --token="$SECURITY_ATLAS_TOKEN" \
        --kind="{{kind}}" \
        --role-arn="$AWS_ROLE_ARN" \
        --region="$AWS_REGION" \
        --environment="$AWS_ENVIRONMENT"

# ----- Protobuf / gRPC -----

# Lint the proto sources (buf STANDARD ruleset)
proto-lint:
    buf lint

# Format proto sources in-place
proto-format:
    buf format -w

# Generate Go bindings into gen/proto/. Commit the diff.
proto-generate:
    buf generate

# What CI does: lint + generate + assert no diff against committed gen/
proto-ci:
    buf lint
    buf generate
    git diff --exit-code -- gen/proto || (echo "gen/proto changed; run 'just proto-generate' and commit" && exit 1)

# ----- OSCAL bridge (slice 030) -----

# Sync the oscal-bridge Python env (uv) with test extras.
oscal-bridge-sync:
    cd oscal-bridge && uv sync --extra test

# Regenerate the oscal-bridge Python gRPC stubs from proto/oscal/v1.
oscal-bridge-gen:
    bash oscal-bridge/scripts/gen_proto.sh

# Run the oscal-bridge pytest suite (unit + in-process gRPC server).
oscal-bridge-test:
    cd oscal-bridge && PYTHONPATH=. uv run --extra test python -m pytest tests/ -q

# Lint + format-check the oscal-bridge Python sources (ruff).
oscal-bridge-lint:
    ruff check oscal-bridge
    ruff format --check oscal-bridge

# Run the OSCAL serialization service locally (sidecar to `atlas`).
oscal-bridge-serve address="127.0.0.1:50070":
    cd oscal-bridge && PYTHONPATH=. uv run python -m atlas_oscal_bridge.server --address {{address}}

# ----- Release -----
#
# The release pipeline (.github/workflows/release.yml) runs on tag push.
# These recipes are for local validation; they never push tags or assets.

# Validate the GoReleaser config without building.
release-check:
    goreleaser check

# Snapshot build into ./dist/ — no publish, no sign. Use to smoke-test
# that all 5 OS/arch targets cross-compile on the current Go toolchain.
release-snapshot:
    goreleaser release --snapshot --clean --skip=publish,sign

# Smoke test the installer script (offline, no network).
install-script-test:
    bash scripts/install_test.sh

# Print the version embedded in a locally-built atlas-cli binary.
# Useful for verifying ldflags wiring before cutting a tag.
print-version version:
    go build -ldflags "-X main.version={{version}}" -o /tmp/security-atlas-cli ./cmd/atlas-cli
    /tmp/security-atlas-cli --version
    /tmp/security-atlas-cli version
    rm /tmp/security-atlas-cli

# ----- Self-host (docker-compose single-VM bundle, slice 037) -----
#
# The bundle lives in deploy/docker/. It brings up Postgres + NATS +
# MinIO + the atlas server + the Next.js frontend on one VM, and on
# first boot runs migrations + seeds the default tenant/scope/user +
# imports the SCF catalog + uploads the 50 SOC 2 control bundles.
#
# Before the first `self-host-up`, copy the env template and edit it:
#   cp deploy/docker/.env.example deploy/docker/.env
# See docs/SELF_HOSTING.md and docs/getting-started/first-evidence.md.

_self_host_compose := "docker compose -f deploy/docker/docker-compose.yml --env-file deploy/docker/.env"

# Validate the self-host compose file parses (no containers started).
self-host-config:
    docker compose -f deploy/docker/docker-compose.yml --env-file deploy/docker/.env.example config -q
    @echo "deploy/docker/docker-compose.yml is valid"

# Build the three self-host images (atlas, atlas-cli/bootstrap, web).
self-host-build:
    {{_self_host_compose}} build

# Bring the self-host bundle up in the background. Requires
# deploy/docker/.env (copy it from .env.example first).
self-host-up:
    {{_self_host_compose}} up -d --build

# Tail logs from the running self-host bundle.
self-host-logs:
    {{_self_host_compose}} logs -f

# Stop the self-host bundle (keeps volumes — data survives).
self-host-down:
    {{_self_host_compose}} down

# Stop the self-host bundle AND delete its volumes (full wipe).
self-host-wipe:
    {{_self_host_compose}} down -v

# Show the status of every self-host service.
self-host-ps:
    {{_self_host_compose}} ps

# ----- Documentation site (mkdocs Material, slice 058) -----
#
# The user-facing docs live at docs-site/. We invoke mkdocs in isolation
# via `uv tool run --with-requirements` so it never pollutes the
# monorepo's uv workspace. Dependency pins are in docs-site/requirements.txt.

_docs_uv := "uv tool run --with-requirements docs-site/requirements.txt --with mkdocs-material"

# Serve the docs site locally with live reload at http://127.0.0.1:8000/.
docs-serve:
    {{_docs_uv}} --from mkdocs mkdocs serve --config-file docs-site/mkdocs.yml

# Strict build — fails on broken links, missing nav entries, dead pages.
# .github/workflows/docs-publish.yml runs this same recipe on every PR.
docs-build:
    {{_docs_uv}} --from mkdocs mkdocs build --strict --config-file docs-site/mkdocs.yml

# ----- README screenshots (slice 057) -----
#
# Refreshes the screenshots + animated GIF embedded in README.md from
# the actually-running app. This is an on-demand recipe — CI does NOT
# block on screenshot freshness (anti-criterion P0-A4). Run it when
# the captured views drift visibly from the merged frontend.
#
# Prerequisites (one-time):
#   - `just build-frontend` succeeds (Next.js builds without error)
#   - `npx playwright install chromium` (Playwright browser binaries)
#   - `ffmpeg` on PATH (Homebrew: `brew install ffmpeg`)
#   - `pngquant` on PATH for size optimization (optional but recommended:
#     `brew install pngquant`)
#
# What it does:
#   1. Builds the web/ workspace (`npm run build` in web/) — produces
#      the production-mode bundle that the captures render against.
#   2. Starts `next start` in the background on :3000 with ATLAS_HTTP_URL
#      pointed at a fixture-driven stub server on :8787 (spun up inside
#      the Playwright spec via `web/scripts/stub-platform-server.ts`).
#   3. Runs the capture spec at
#      `web/scripts/capture-readme-screenshots.spec.ts` — produces 8
#      PNGs + 1 webm under `docs/images/` + the Playwright `test-results/`
#      directory.
#   4. ffmpeg converts the recorded webm → `docs/images/flow-create-control.gif`
#      with a generated palette (smaller than naive single-pass).
#   5. pngquant compresses the PNGs in place (lossy palette quantization)
#      to keep the total weight ≤ 5 MB (anti-criterion P0-A3).
#
# Determinism: the stub server replays static JSON fixtures from
# `fixtures/readme-demo/**`, so every run produces the same captured
# pixels modulo font rasterization. Fixture content is neutral — no
# maintainer references, no real tenant data (anti-criterion P0-A2).
refresh-screenshots:
    @echo "[1/5] Building web/ (production mode — standalone output)…"
    cd web && npm run build
    @echo "[2/5] Copying static assets into the standalone bundle…"
    cp -R web/.next/static web/.next/standalone/web/.next/static
    @echo "[3/5] Bundling capture script via esbuild…"
    ./node_modules/.bin/esbuild \
        web/scripts/capture-readme-screenshots.ts \
        --bundle \
        --platform=node \
        --target=node20 \
        --external:playwright \
        --external:@playwright/test \
        --outfile=web/scripts/.capture-readme-screenshots.bundled.js
    @echo "[4/5] Running capture (boots production Next server + stub platform)…"
    cd web && node scripts/.capture-readme-screenshots.bundled.js
    @echo "[5/6] Converting recorded webm → optimized GIF via ffmpeg…"
    WEBM=$(find web/test-results/readme-capture-video -name '*.webm' 2>/dev/null | head -1); \
        if [ -z "$WEBM" ]; then \
            echo "no flow-recording webm produced — GIF skipped"; \
            exit 1; \
        else \
            ffmpeg -y -i "$WEBM" \
                -vf "fps=10,scale=1280:-1:flags=lanczos,split[s0][s1];[s0]palettegen=max_colors=128[p];[s1][p]paletteuse=dither=bayer:bayer_scale=3" \
                -loop 0 \
                docs/images/flow-create-control.gif; \
        fi
    @echo "[6/6] Compressing PNGs via pngquant (optional)…"
    if command -v pngquant >/dev/null 2>&1; then \
        find docs/images -name '*.png' -exec pngquant --quality=70-90 --speed=1 --force --ext .png {} \; ; \
    else \
        echo "pngquant not on PATH — skipping PNG compression"; \
    fi
    @echo "Refresh complete. Total weight:"
    @du -sh docs/images/*.{png,gif} 2>/dev/null | awk '{print "    " $0}'
    @du -sch docs/images/*.{png,gif} 2>/dev/null | tail -1 | awk '{print "  TOTAL: " $1}'

# ----- Onboarding walkthroughs (slice 070) -----
#
# Regenerates the five executable onboarding walkthroughs under
# docs/walkthroughs/ from a fresh slice-037 docker-compose self-host
# bundle. Like refresh-screenshots, this is on-demand only — CI does
# NOT block on walkthrough freshness (anti-criterion P0-A2 / AC-9). Run
# it when the underlying surface materially drifts.
#
# Walkthrough vs slice 027: the walkthroughs this recipe generates are
# PAI Walkthrough skill documents (`uvx showboat` driven). They are
# UNRELATED to slice 027's `internal/audit/walkthrough` package — that
# one is auditor evidence capture against controls. See each
# walkthrough doc's header for the full disambiguation.
#
# Prerequisites (one-time):
#   - `uvx` on PATH (Homebrew: `brew install uv`). Showboat installs
#     via `uvx showboat` at first use.
#   - `just self-host-up` succeeds. The recipe assumes a running stack
#     at the canonical ports (Postgres on 5432, atlas-server on 8080).
#     For the dev fast-path the recipe can also be pointed at any
#     migrated Postgres via the PG_CONTAINER env var (defaults to the
#     self-host bundle's postgres container).
#
# What it does:
#   1. Verifies the local stack is reachable via `just self-host-ps`.
#   2. Applies `fixtures/walkthroughs/00-seed.sql` (base seed) then each
#      per-walkthrough SQL in the order the walkthroughs run them.
#   3. Replays each walkthrough's `uvx showboat init/note/exec` sequence
#      (the canonical pattern is captured in each .md file; the recipe
#      is the operator's re-execution harness).
#   4. Rewrites the docs-site/docs/walkthroughs/ copies' relative paths
#      to GitHub URLs so `mkdocs build --strict` passes.
#   5. Runs `just docs-build` to confirm the walkthroughs render.
#
# Determinism: the fixtures are static SQL with deterministic UUIDs, so
# every run produces byte-identical output (modulo the showboat
# timestamp + UUID header per file). A large diff on the output blocks
# is a real drift signal — the underlying surface changed and the
# walkthrough needs review.
PG_CONTAINER := env_var_or_default("PG_CONTAINER", "security-atlas-pg-030")

walkthroughs-refresh:
    @echo "[1/4] Verifying local Postgres reachable via docker exec…"
    docker exec {{PG_CONTAINER}} pg_isready -U postgres > /dev/null \
        || (echo "Postgres container {{PG_CONTAINER}} not reachable. Run 'just self-host-up' or set PG_CONTAINER." && exit 1)
    @echo "[2/4] Applying fixtures…"
    @for f in fixtures/walkthroughs/00-seed.sql \
              fixtures/walkthroughs/rls-isolation.sql \
              fixtures/walkthroughs/schema-registry.sql \
              fixtures/walkthroughs/audit-period.sql \
              fixtures/walkthroughs/evaluation-pipeline.sql \
              fixtures/walkthroughs/oscal-export.sql; do \
        docker cp "$f" {{PG_CONTAINER}}:/tmp/$(basename $f); \
        docker exec {{PG_CONTAINER}} psql -U postgres -d security_atlas -v ON_ERROR_STOP=1 -f /tmp/$(basename $f); \
    done
    @echo "[3/4] Walkthrough re-capture is a manual operator workflow."
    @echo "      For each docs/walkthroughs/*.md file, replay its bash blocks"
    @echo "      via the canonical showboat shape:"
    @echo "        uvx showboat init <file> '<title>'"
    @echo "        uvx showboat note <file> '<prose>'"
    @echo "        uvx showboat exec <file> bash '<cmd>'"
    @echo "      The fixtures in step 2 establish the data preconditions."
    @echo "      See CONTRIBUTING.md 'Refreshing walkthroughs' for the full workflow."
    @echo "[4/4] Sync docs-site/docs/walkthroughs/ from docs/walkthroughs/…"
    @cp docs/walkthroughs/*.md docs-site/docs/walkthroughs/
    @for f in docs-site/docs/walkthroughs/audit-period-freezing.md \
              docs-site/docs/walkthroughs/evaluation-pipeline.md \
              docs-site/docs/walkthroughs/oscal-ssp-export.md \
              docs-site/docs/walkthroughs/rls-tenant-isolation.md \
              docs-site/docs/walkthroughs/schema-registry-seed-and-validate.md; do \
        sed -i.bak -E 's|\(\.\./\.\./([^)]+)\)|(https://github.com/mgoodric/security-atlas/blob/main/\1)|g; s|\(\.\./issues/([^)]+)\)|(https://github.com/mgoodric/security-atlas/blob/main/docs/issues/\1)|g; s|\(\.\./adr/([^)]+)\)|(https://github.com/mgoodric/security-atlas/blob/main/docs/adr/\1)|g' "$f"; \
        rm -f "$f.bak"; \
    done
    @echo "Refresh complete. Verify via 'just docs-build'."
