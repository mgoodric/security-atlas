# Contributing to security-atlas

Thanks for your interest in contributing. This document covers the local dev setup, the commit and review conventions, and the developer-certificate-of-origin requirement.

---

## Prerequisites

- Go 1.26+ (matches `go.mod`; slice 089/090 hardened the govulncheck pin under Go 1.26)
- Node.js 20+
- Python 3.11+ (for `oscal-bridge` and ruff)
- Postgres 16+ (for migrations + integration tests) — `brew install postgresql@16` or via Docker
- [`sqlc v1.31.1`](https://github.com/sqlc-dev/sqlc/releases/tag/v1.31.1) — `brew install sqlc` (the pinned version lives in `justfile` as `SQLC_VERSION`; `just sqlc-version-check` asserts your local binary matches). Running `sqlc generate` with a different version produces drift across `internal/db/dbx/*.go` that no committer intended — see slice 109.
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

| Recipe                      | What it does                                                               |
| --------------------------- | -------------------------------------------------------------------------- |
| `just`                      | List all recipes                                                           |
| `just db-up`                | Start a local Postgres 16 in Docker                                        |
| `just db-down`              | Tear down the local Postgres                                               |
| `just migrate-up`           | Bootstrap roles + apply forward SQL migrations (requires `$DATABASE_URL`)  |
| `just migrate-down`         | Apply the latest reverse migration                                         |
| `just sqlc-generate`        | Run `sqlc generate` against the schema                                     |
| `just test-integration`     | Run integration tests (requires `$DATABASE_URL_APP`)                       |
| `just build`                | Build all components (Go + frontend)                                       |
| `just build-go`             | Build Go binaries only                                                     |
| `just build-frontend`       | Build the `web/` workspace                                                 |
| `just test`                 | Run all tests                                                              |
| `just test-go`              | Run Go tests (`go test -race ./...` in CI)                                 |
| `just test-frontend`        | Run frontend tests                                                         |
| `just lint`                 | Run all linters (Go + frontend + Python)                                   |
| `just lint-go`              | `golangci-lint run ./...`                                                  |
| `just lint-frontend`        | `npm run lint` in `web/`                                                   |
| `just lint-python`          | `ruff check .`                                                             |
| `just fmt`                  | Format all code (in-place)                                                 |
| `just fmt-go`               | `gofmt -w` + `goimports -w -local github.com/mgoodric/security-atlas`      |
| `just fmt-python`           | `ruff format .`                                                            |
| `just install-hooks`        | Install pre-commit hooks (one-time)                                        |
| `just hooks-run`            | Run pre-commit against the whole tree                                      |
| `just tidy`                 | `go mod tidy` and fail if `go.mod` / `go.sum` change                       |
| `just ci`                   | Run what CI runs (lint + test + build)                                     |
| `just refresh-screenshots`  | Re-capture README screenshots + animated GIF (slice 057)                   |
| `just walkthroughs-refresh` | Apply walkthrough fixtures + sync docs-site walkthrough copies (slice 070) |

---

## Repository layout

| Path                                                | What it is                                                                                                | First slice that fills it           |
| --------------------------------------------------- | --------------------------------------------------------------------------------------------------------- | ----------------------------------- |
| [`Plans/`](./Plans)                                 | Design canvas, mockups, deep-dive docs                                                                    | (already populated)                 |
| [`docs/issues/`](./docs/issues)                     | v1 backlog (69 issues, all merged) + index + dep graph + post-v1 follow-on slices (070+)                  | (already populated)                 |
| [`docs/walkthroughs/`](./docs/walkthroughs)         | Executable onboarding walkthroughs (showboat-generated; canonical copies)                                 | slice 070                           |
| [`fixtures/walkthroughs/`](./fixtures/walkthroughs) | Deterministic seed data the walkthroughs run against                                                      | slice 070                           |
| [`CLAUDE.md`](./CLAUDE.md)                          | Constitutional principles + AI-assist boundary + tech stack lock                                          | (already populated)                 |
| `cmd/atlas/`                                        | Platform server binary                                                                                    | slice 013 + ongoing                 |
| `cmd/atlas-cli/`                                    | CLI binary                                                                                                | slice 003                           |
| `cmd/atlas-oscal/`                                  | OSCAL bridge binary (Python via gRPC)                                                                     | slice 030                           |
| `internal/`                                         | Private Go packages (catalog, evidence, eval, ucf, scope, risk, policy, audit, board, auth, tenancy, api) | slices 002+                         |
| `pkg/sdk-go/`                                       | Public Go SDK (evidence push)                                                                             | slice 003                           |
| `connectors/`                                       | Per-connector implementations (AWS, GitHub, Okta, 1Password, osquery, Jira/Linear, manual-upload)         | slices 004, 044–049                 |
| `sdk/python/` `sdk/typescript/` `sdk/java/`         | Non-Go SDKs                                                                                               | slice 003 (Python + TS); Java in v2 |
| `web/`                                              | Next.js 16 frontend                                                                                       | slice 005                           |
| `oscal-bridge/`                                     | Python service wrapping `compliance-trestle`                                                              | slice 030                           |
| `proto/`                                            | gRPC protobuf definitions                                                                                 | slice 003                           |
| `schemas/`                                          | JSON Schemas for `evidence_kind`                                                                          | slice 014                           |
| `migrations/`                                       | Versioned SQL migrations + role bootstrap                                                                 | slice 002                           |
| `policies/`                                         | OPA Rego (authz + control policies)                                                                       | slice 035                           |
| `deploy/docker/` `deploy/helm/`                     | Deployment artifacts                                                                                      | slices 037, 038                     |

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

### Phantom dependencies

`scripts/audit-deps.sh` classifies every direct dependency across the four manifests (`web/package.json`, `go.mod`, `oscal-bridge/pyproject.toml`, `docs-site/requirements.txt`) as USED, USED-VIA-CONFIG, USED-VIA-SCRIPT, or PHANTOM. The CI workflow re-runs the audit on every PR that touches one of those manifests and posts a comment listing any PHANTOM candidates — informational only, never blocks the merge (slice 120). If a PR you authored draws a phantom-dependency comment, drop the unused dep in the same PR (or document in the PR description why the comment is a false positive — known KEEP cases like Next.js's runtime peer `react-dom` are recorded in [`docs/audit-log/120-audit-and-remove-phantom-dependencies-decisions.md`](./docs/audit-log/120-audit-and-remove-phantom-dependencies-decisions.md)). The script is also runnable locally (`bash scripts/audit-deps.sh` from the repo root) — output is TSV, scoped runs via `--ecosystem <npm|go|pip-bridge|pip-docs>`.

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

### Local CI parity

`just install-hooks` installs pre-commit on both the `pre-commit` and `pre-push` stages. The pre-push hook runs the full pre-commit suite against the about-to-push commits and `npm run lint -w web` for frontend ESLint. This catches the "prettier reformats `_STATUS.md` after the status-flip commit" pattern that produced 5 of the 62 CI failures observed on 2026-05-15. Emergency bypass remains available via `git push --no-verify`; do not use it casually — the recurring pre-commit-failure data is the reason this hook exists.

### Dependency hygiene

CI runs through [StepSecurity Harden-Runner](https://github.com/step-security/harden-runner) in **audit mode** as the first step of every job in every workflow under `.github/workflows/` (slice 117). The action instruments the runner to record outbound network calls, file writes, and process executions — catching supply-chain attacks (malicious package post-installs, exfiltration, compromised actions) that PR-time analysis cannot see. Audit mode never fails a job; the data lands on the StepSecurity dashboard at `https://app.stepsecurity.io/github/mgoodric/security-atlas/actions/runs/<run-id>` (one URL per workflow run, surfaced via the "Action Security Insights" link in the GitHub Actions job summary). If you see new outbound destinations flagged in the workflow summary that you can't justify, surface them in the PR description; we treat unexplained egress as a review-blocker even while we're in audit mode. Block-mode promotion (with an `allowed-endpoints` allowlist derived from the audit baseline) is filed as slice 118, `not-ready`, gated on ~2 weeks of audit-mode data.

When you touch any file under `internal/db/queries/`, run `just sqlc-generate` (which version-checks first) and commit the regenerated `internal/db/dbx/*.go` in the same commit as the query edit. Do NOT hand-edit `internal/db/dbx/*.go` outside of the two documented hand-narrows (`policies.sql.go` `AckDenominator`/`AckNumerator`, `scf_anchors.sql.go` `StateResult`/`StateFreshnessStatus`) — both are annotated in place and explained in slice 109's decisions log. New queries should regen cleanly; if the regen also rewrites unrelated files, your local sqlc binary is the wrong version (compare against `SQLC_VERSION` in `justfile`).

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

## Refreshing walkthroughs

`docs/walkthroughs/` ships five executable onboarding documents (slice
070): `evaluation-pipeline.md`, `audit-period-freezing.md`,
`rls-tenant-isolation.md`, `schema-registry-seed-and-validate.md`,
`oscal-ssp-export.md`. Each one is generated by the PAI Walkthrough
skill (`uvx showboat`) against a live local stack. They are
version-controlled artifacts refreshed on demand — CI does NOT block on
walkthrough freshness (same anti-criterion as the README screenshots).
When the underlying surface materially drifts, regenerate:

```sh
# Bring up the slice-037 self-host bundle (or set PG_CONTAINER to any
# already-migrated Postgres on your machine).
just self-host-up

# Apply fixtures + sync the docs-site copies. The bash blocks in each
# walkthrough are replayed against the seeded stack via uvx showboat.
just walkthroughs-refresh

# Confirm the walkthroughs render under mkdocs.
just docs-build
```

The recipe (1) verifies the local Postgres is reachable, (2) applies
`fixtures/walkthroughs/*.sql` in canonical order, (3) prompts you to
replay each walkthrough's `uvx showboat exec` sequence (manual step —
the bash blocks are visible in the `.md` files), and (4) syncs the
canonical `docs/walkthroughs/*.md` copies into
`docs-site/docs/walkthroughs/`, rewriting `../../` relative paths to
GitHub URLs so `mkdocs build --strict` continues to pass.

Determinism: the fixtures use deterministic UUIDs, so a clean re-run
produces byte-identical captured output (modulo each walkthrough's
showboat timestamp + UUID header). A large diff on the captured blocks
is a real drift signal — the underlying surface changed and the
walkthrough needs review.

Fixture constraints — see `fixtures/walkthroughs/README.md`. All seed
data is neutral: no maintainer references, no real tenant names, no
vendor-prefixed credentials. When you edit fixtures, run the recipe to
regenerate the captured output and commit both in the same change.

**Walkthrough vs slice 027 (load-bearing disambiguation):** the
walkthroughs this recipe generates are PAI Walkthrough skill documents
(showboat-driven). They are unrelated to slice 027's
`internal/audit/walkthrough` package, which records auditor evidence
capture against controls. Every walkthrough doc's header restates this.

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

## Linting

Run lint locally against the frontend workspace:

```sh
npm run lint -w web
```

**Current state (slice 078, 2026-05-16):** `web/package.json` pins `eslint: ^9` (not `^10`) because `eslint-plugin-react@7.37.5` (latest stable) caps its peerDeps at `^9.7` and crashes under ESLint 10 with `TypeError: contextOrFilename.getFilename is not a function`. The `next` dist-tag is stale at `7.8.0-rc.0`. Path B per slice 078 — pin ESLint to `^9` until upstream ships a 10-compat release.

**Where the pin lives:** [`web/package.json`](./web/package.json) `devDependencies.eslint`. The decision rationale (including why a direct downgrade instead of a pure `overrides` block) lives in [`docs/audit-log/078-eslint-10-react-plugin-incompat-decisions.md`](./docs/audit-log/078-eslint-10-react-plugin-incompat-decisions.md) D2.

**Re-upgrade path:** when `npm view eslint-plugin-react@latest peerDependencies` returns a value listing `^10` (or higher), [`docs/issues/095-eslint-10-re-upgrade.md`](./docs/issues/095-eslint-10-re-upgrade.md) becomes `ready` and flips the pin back. ~5-minute slice.

**CI gate:** the `Frontend · lint` job runs `npm run lint -w web` on every PR that touches code paths (slice-061 path-filter pattern). It's informational only — NOT in required-checks — because lint regressions on every dep bump would flake the merge queue. Promote-to-required is a future cadence-stability slice.

## Open-redirect prevention

The post-login `signIn` server action (`web/app/login/actions.ts`) reads a
`from` form field that originates from `/login?from=...`. The
2026-Q2 security audit flagged HIGH that the unvalidated value was passed
straight to Next.js `redirect()`, enabling phishing-pivot attacks via
`?from=https://evil.example.com/phish`. Slice 086 introduced a small
helper at `web/lib/safe-redirect.ts`:

```ts
import { safeRedirectTarget } from "@/lib/safe-redirect";

// Three checks + fallback: rejects fully-qualified URLs,
// protocol-relative URLs (`//evil.com`), backslash-prefixed paths
// (`/\evil.com`), `javascript:` URLs, empty strings, and bare `/`.
// Returns `/dashboard` on any non-safe input.
const target = safeRedirectTarget(rawTarget);
```

**Reviewer-discipline rule:** every redirect target sourced from user
input MUST flow through `safeRedirectTarget` before reaching
`redirect()`, `NextResponse.redirect()`, `router.push()`, or any
equivalent. If you add a new redirect-from-user-input call site, route
it through the helper and add a case to
`web/lib/safe-redirect.test.ts` if your call site exposes a new attack
shape. The unit test is the long-term gate that keeps the helper
short — extend the test rather than weakening the helper. Open-redirect
findings outside the signIn flow should be filed as follow-on slices
(per slice 086 P0-A4 — no in-place scope expansion).

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
