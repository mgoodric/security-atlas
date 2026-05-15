# Contributing to security-atlas

Thanks for your interest in contributing. This document covers the local dev setup, the commit and review conventions, and the developer-certificate-of-origin requirement.

---

## Prerequisites

- Go 1.25+
- Node.js 20+
- Python 3.11+ (for `oscal-bridge` and ruff)
- Postgres 16+ (for migrations + integration tests) — `brew install postgresql@16` or via Docker
- [`sqlc`](https://docs.sqlc.dev) — `brew install sqlc`
- [`just`](https://just.systems) — `brew install just`
- [`pre-commit`](https://pre-commit.com) — `pip install pre-commit`
- [`golangci-lint`](https://golangci-lint.run) — `brew install golangci-lint`
- [`uv`](https://docs.astral.sh/uv/) (optional, for the Python workspace) — `brew install uv`

---

## Local setup

```sh
git clone https://github.com/mgoodric/security-atlas.git
cd security-atlas
just install-hooks   # one-time: installs pre-commit hooks
just build           # build Go binaries + frontend
just test            # run all tests
```

After `just install-hooks`, commits with malformed Go (or unformatted YAML / JSON / Markdown) are rejected locally before they reach the remote.

---

## Task surface (`just`)

| Recipe                     | What it does                                                              |
| -------------------------- | ------------------------------------------------------------------------- |
| `just`                     | List all recipes                                                          |
| `just db-up`               | Start a local Postgres 16 in Docker                                       |
| `just db-down`             | Tear down the local Postgres                                              |
| `just migrate-up`          | Bootstrap roles + apply forward SQL migrations (requires `$DATABASE_URL`) |
| `just migrate-down`        | Apply the latest reverse migration                                        |
| `just sqlc-generate`       | Run `sqlc generate` against the schema                                    |
| `just test-integration`    | Run integration tests (requires `$DATABASE_URL_APP`)                      |
| `just build`               | Build all components (Go + frontend)                                      |
| `just build-go`            | Build Go binaries only                                                    |
| `just build-frontend`      | Build the `web/` workspace                                                |
| `just test`                | Run all tests                                                             |
| `just test-go`             | Run Go tests (`go test -race ./...` in CI)                                |
| `just test-frontend`       | Run frontend tests                                                        |
| `just lint`                | Run all linters (Go + frontend + Python)                                  |
| `just lint-go`             | `golangci-lint run ./...`                                                 |
| `just lint-frontend`       | `npm run lint` in `web/`                                                  |
| `just lint-python`         | `ruff check .`                                                            |
| `just fmt`                 | Format all code (in-place)                                                |
| `just fmt-go`              | `gofmt -w` + `goimports -w -local github.com/mgoodric/security-atlas`     |
| `just fmt-python`          | `ruff format .`                                                           |
| `just install-hooks`       | Install pre-commit hooks (one-time)                                       |
| `just hooks-run`           | Run pre-commit against the whole tree                                     |
| `just tidy`                | `go mod tidy` and fail if `go.mod` / `go.sum` change                      |
| `just ci`                  | Run what CI runs (lint + test + build)                                    |
| `just refresh-screenshots` | Re-capture README screenshots + animated GIF (slice 057)                  |

---

## Repository layout

| Path                                        | What it is                                                                                                | First slice that fills it           |
| ------------------------------------------- | --------------------------------------------------------------------------------------------------------- | ----------------------------------- |
| [`Plans/`](./Plans)                         | Design canvas, mockups, deep-dive docs                                                                    | (already populated)                 |
| [`docs/issues/`](./docs/issues)             | v1 backlog (58 issues + index + dep graph + review)                                                       | (already populated)                 |
| [`CLAUDE.md`](./CLAUDE.md)                  | Constitutional principles + AI-assist boundary + tech stack lock                                          | (already populated)                 |
| `cmd/atlas/`                                | Platform server binary                                                                                    | slice 013 + ongoing                 |
| `cmd/atlas-cli/`                            | CLI binary                                                                                                | slice 003                           |
| `cmd/atlas-oscal/`                          | OSCAL bridge binary (Python via gRPC)                                                                     | slice 030                           |
| `internal/`                                 | Private Go packages (catalog, evidence, eval, ucf, scope, risk, policy, audit, board, auth, tenancy, api) | slices 002+                         |
| `pkg/sdk-go/`                               | Public Go SDK (evidence push)                                                                             | slice 003                           |
| `connectors/`                               | Per-connector implementations (AWS, GitHub, Okta, 1Password, osquery, Jira/Linear, manual-upload)         | slices 004, 044–049                 |
| `sdk/python/` `sdk/typescript/` `sdk/java/` | Non-Go SDKs                                                                                               | slice 003 (Python + TS); Java in v2 |
| `web/`                                      | Next.js 16 frontend                                                                                       | slice 005                           |
| `oscal-bridge/`                             | Python service wrapping `compliance-trestle`                                                              | slice 030                           |
| `proto/`                                    | gRPC protobuf definitions                                                                                 | slice 003                           |
| `schemas/`                                  | JSON Schemas for `evidence_kind`                                                                          | slice 014                           |
| `migrations/`                               | Versioned SQL migrations + role bootstrap                                                                 | slice 002                           |
| `policies/`                                 | OPA Rego (authz + control policies)                                                                       | slice 035                           |
| `deploy/docker/` `deploy/helm/`             | Deployment artifacts                                                                                      | slices 037, 038                     |

---

## Conventional Commits

All commits MUST follow [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/). The release pipeline (`release-please`) reads commit types to compute semver bumps and generate changelog entries.

Allowed types:

| Type       | Bump  | When to use                              |
| ---------- | ----- | ---------------------------------------- |
| `feat`     | minor | New feature                              |
| `fix`      | patch | Bug fix                                  |
| `docs`     | none  | Docs-only change                         |
| `deps`     | none  | Dependency bump (Dependabot)             |
| `chore`    | none  | Tooling / housekeeping                   |
| `refactor` | none  | Code restructure without behavior change |
| `test`     | none  | Adding or refining tests only            |
| `perf`     | patch | Performance improvement                  |
| `build`    | none  | Build / dependency change                |
| `ci`       | none  | CI / workflow change                     |
| `revert`   | patch | Reverting a previous commit              |
| `style`    | none  | Formatting / lint                        |

Breaking changes — add `!` after the type (e.g. `feat(api)!: drop deprecated /v0`) **and** include a `BREAKING CHANGE:` footer. This triggers a major bump.

Scope is optional but recommended (`feat(evidence):`, `fix(ucf):`, `docs(canvas):`).

### Dependency updates

Dependabot opens PRs every Monday with the `deps:` prefix (`deps(deps):`, `deps(deps-dev):`, `deps(actions):`, etc.) across all ecosystems (Go modules, npm, pip, Docker, GitHub Actions). Release-please surfaces them under the **Dependencies** section in `CHANGELOG.md`. Patch and minor bumps are reviewed individually; majors are investigated for breaking-change exposure before merge.

---

## Developer Certificate of Origin (DCO)

This project uses the [Developer Certificate of Origin](https://developercertificate.org/) — there is **no separate CLA to sign**. By contributing, you certify the four DCO statements: you wrote the change (or have rights to submit it), you are licensing it under the project license (Apache 2.0), and your sign-off is a public record.

Every commit MUST carry a `Signed-off-by:` trailer:

```sh
git commit -s -m "feat(area): your change"
```

CI rejects PRs whose commits lack a sign-off.

If a commit was AI-assisted, also include a `Co-authored-by:` trailer naming the assistant.

---

## Pull request workflow

1. Branch from `main` using the pattern `<area>/<short-description>` (for example `evidence/sdk-push-protocol` or `ucf/scf-importer`).
2. Open a draft PR early. Use the PR template; fill every section.
3. Run `pre-commit run --all-files` locally before pushing. CI runs the same hooks; passing locally avoids CI churn.
4. Resolve every review comment thread before merge.
5. PRs are squash-merged. The squash commit message is rewritten to Conventional-Commit form by the maintainer.

`main` is protected:

- ≥1 approving review required
- All CI status checks must pass (build, lint, test, codeql, codecov, container-publish-on-release)
- Linear history (squash- or rebase-merge only)
- Force-push blocked
- Direct push blocked
- All review-thread comments resolved
- Stale approvals dismissed on new commits

See [`.github/branch-protection.json`](./.github/branch-protection.json) for the full ruleset.

---

## Refreshing the README screenshots

The README embeds four screenshots + one animated GIF of the running app
(`docs/images/`). They are version-controlled artifacts refreshed on
demand — CI does NOT block on screenshot freshness. When the merged
frontend drifts visibly from what's captured, regenerate:

```sh
just refresh-screenshots
```

The recipe:

1. Builds `web/` in production mode (`npm run build`).
2. Spins up a fixture-driven stub of the Go platform on `:8787`
   (`web/scripts/stub-platform-server.ts`) serving JSON from
   `fixtures/readme-demo/**`.
3. Runs the capture spec
   (`web/scripts/capture-readme-screenshots.spec.ts`) under
   `web/playwright.config.ts` to produce eight PNGs (light + dark for
   each of four views) at 1440×900, plus a webm of an 8-second flow.
4. Converts the webm to a palette-optimized GIF via `ffmpeg`.
5. Quantizes the PNGs in place via `pngquant` (optional but recommended).

Prerequisites: `ffmpeg` and `pngquant` on `$PATH` (Homebrew installs
both); `npx playwright install chromium` once per machine. The Next.js
server boots in ~2 seconds on a modern laptop; the whole capture run
typically completes in under a minute.

Fixture constraints — see `fixtures/readme-demo/README.md`. All seed
data is neutral: no maintainer references, no real tenant names, no
vendor-prefixed credentials. When you edit fixtures, run the recipe to
regenerate the artifacts and commit both in the same change.

## Test infrastructure

The `Frontend · Playwright e2e` CI job is currently **quarantined**
(slice 079 — `continue-on-error: true` on the job, since 2026-05-15)
because the five un-shimmed specs (`admin-bootstrap`, `audit-workspace`,
`control-detail`, `dashboard`, `risk-hierarchy`) reference fixtures that
exist on disk but are not applied to the platform at job startup. Runs
fail predictably; the job is non-required, so the red annotations are
noise — not your bug. The two route-mocked specs (`first-time-login`,
`version-footer`) are unaffected. The fix lives in slice 082
(`Playwright e2e seed-data harness`, status `not-ready`); when it lands,
the quarantine line comes out and the job again gates the PR.

## AI-assist boundary

The platform supports AI assistance in narrowly-defined places (see [`CLAUDE.md`](./CLAUDE.md) → "AI-assist boundary"). Contributor-side rules:

- **AI may help author code, docs, and tests.** Disclose with a `Co-authored-by:` trailer naming the assistant.
- **AI may NOT generate audit-binding text** (policy bodies, SOC 2 mapping rationale, board-report narrative) without human review. PRs that introduce such text without an audit-log entry under `docs/audit-log/` will be asked to add one.
- **AI may NOT use confidential data from one tenant to seed drafts in another.** This is enforced at the schema level (`ai_assisted=true` rows cannot have `human_approved=true` without `human_approver` populated).
- **Cloud LLM routing is opt-in per tenant** and surfaces a visible banner. Default inference backend is local Ollama.

If your contribution touches AI-assist surface, link the relevant `CLAUDE.md` section in the PR description.

---

## Reporting bugs and requesting features

- Bug report: open an issue using the `Bug report` template.
- Feature request: open an issue using the `Feature request` template.
- Security vulnerability: **do not** open a public issue — follow [`SECURITY.md`](./SECURITY.md).

---

## Code of conduct

See [`CODE_OF_CONDUCT.md`](./CODE_OF_CONDUCT.md). The project adopts the Contributor Covenant v2.1.
