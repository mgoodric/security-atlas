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

# Apply bootstrap roles then run versioned Atlas migrations
migrate-up:
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/bootstrap/01-roles.sql
    atlas migrate hash --dir file://migrations/sql
    atlas migrate apply --env local

# Roll back the most recent migration. Atlas OSS does not automate `down`
# the same way as `up`; we apply the hand-authored .down.sql via psql and
# rewind Atlas's revision tracker manually.
migrate-down:
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f migrations/sql/20260511000000_init.down.sql
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c "DROP TABLE IF EXISTS atlas_schema_revisions"

# Atlas migration status (which migrations applied, which pending)
migrate-status:
    atlas migrate status --env local

# Atlas migration hash regeneration (run before adding a new migration)
migrate-hash:
    atlas migrate hash --dir file://migrations/sql

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

# Generate sqlc code (no queries yet in slice 002 — canary that the schema parses)
sqlc-generate:
    sqlc generate
