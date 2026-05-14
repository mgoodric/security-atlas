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

# Install pre-commit hooks
install-hooks:
    pre-commit install --install-hooks
    @echo "pre-commit hooks installed. Bad-format commits will be rejected locally."

# Run pre-commit against the whole tree
hooks-run:
    pre-commit run --all-files

# What CI runs (lint + test + build)
ci: lint test build

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
