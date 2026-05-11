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

# Run Go tests
test-go:
    go test ./...

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
