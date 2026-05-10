# 001 — Monorepo skeleton + CI green build

**Cluster:** Spine
**Estimate:** 1.5d
**Type:** AFK

## Narrative

Establish the empty monorepo shell that every subsequent slice extends: `justfile` at root, `go.work` for multi-module Go, `package.json` for npm workspaces (frontend + TS SDK), `pyproject.toml` for the Python OSCAL bridge. Create the directory tree per CLAUDE.md "Planned repository layout" — `cmd/`, `internal/`, `pkg/`, `connectors/`, `sdk/`, `web/`, `oscal-bridge/`, `proto/`, `schemas/`, `migrations/`, `policies/`, `deploy/`. Wire `.github/workflows/` to lint, build, and test on every PR. Land pre-commit hooks enforcing the linting set (`golangci-lint`, `ruff`, `prettier`, `eslint`). The slice delivers value because CI is green on a trivial PR — proving the toolchain works for every later contributor.

## Acceptance criteria

- [ ] AC-1: `just build` succeeds from a fresh clone in under 5 minutes
- [ ] AC-2: A PR with a trivial change triggers GitHub Actions; build + lint + test jobs all return green
- [ ] AC-3: Pre-commit hook installed via `just install-hooks` rejects a `.go` file with bad formatting before commit completes
- [ ] AC-4: `go.work` lists at least one module per `cmd/` entry; `go mod tidy` is clean
- [ ] AC-5: `npm install` from repo root installs both `web/` and `sdk/typescript/` deps via workspaces
- [ ] AC-6: README at root explains the `just` task surface (build, test, lint, fmt, install-hooks)

## Constitutional invariants honored

- Working norms ("No code commits without explicit user approval to start scaffolding") — this slice IS that approval point; user approves to kick off

## Canvas references

- `CLAUDE.md` — "Planned repository layout" + "When code begins" step 1
- `Plans/canvas/09-tech-stack.md` — locked tech choices

## Dependencies

None — can start immediately.

## Anti-criteria (P0)

- Does NOT scaffold any application code (no handlers, no migrations, no domain types)
- Does NOT introduce dependencies beyond what's needed for build/lint/test infrastructure
- Does NOT commit any `.env` / secrets / credentials

## Skill mix (3–5)

- Go modules + `go.work`
- npm workspaces
- GitHub Actions
- `just` task runner
- pre-commit + golangci-lint config
