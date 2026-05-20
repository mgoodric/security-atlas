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
| `just refresh-screenshots`  | Re-capture README screenshots — requires `ATLAS_DEMO_SEED=1` per slice 132 |
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

### Action pinning

Every `uses:` line in every workflow under `.github/workflows/` MUST reference a 40-character commit SHA, never a floating tag like `@v6` or `@main`. The shape is:

```yaml
uses: <owner>/<repo>@<40-char-sha> # <tag>
```

Example: `uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6`.

**Why:** A tag-pinned action is exposed to the tag-jacking supply-chain attack class — an attacker who compromises an action's push permissions can move a floating tag like `v6` to point at malicious code, and every consumer pinned to `@v6` silently picks it up on the next CI run. SHA pins are immutable; the attacker cannot retroactively change what `@<sha>` resolves to. Slice 117 SHA-pinned `step-security/harden-runner`; slice 128 extended the discipline to every action in every workflow.

**Adding a new action.** Look up the SHA for the tag you want to pin to:

```sh
gh api repos/<owner>/<repo>/git/refs/tags/<tag> --jq '.object.sha'
```

If `.object.type == "tag"` (annotated tag — common for `actions/attest-build-provenance`, `github/codeql-action`, and some others), dereference one more hop to get the commit:

```sh
gh api repos/<owner>/<repo>/git/tags/<sha-from-above> --jq '.object.sha'
```

Use the resulting 40-character commit SHA in the workflow with the `# <tag>` trailing comment so the next reader can see what version it corresponds to. Dependabot's `github-actions` ecosystem (configured in `.github/dependabot.yml`) understands this convention and proposes SHA-bump PRs that update both the SHA and the comment together when the upstream tag moves.

**Updating an existing pin.** Re-run the same `gh api` lookup against the new tag, replace the SHA, update the `# <tag>` comment. Dependabot handles this for you on a weekly cadence; manual updates are only needed for out-of-cycle security bumps.

**Sub-paths share a SHA.** Multiple `uses:` lines that refer to sub-paths of the same repo (e.g. `github/codeql-action/init`, `github/codeql-action/analyze`, `github/codeql-action/autobuild`) all use the same SHA from `repos/github/codeql-action/git/refs/tags/<tag>`. Pin all three to the same value.

**CI guard.** The `actions-pin-check` job in `.github/workflows/ci.yml` runs `scripts/check-action-pins.sh` on every PR and every push to `main`. The job is **blocking** — a tag-pinned action fails the build and the merge button stays disabled. This is the slice-117/128 supply-chain mitigation; an informational-only check would silently allow regression.

**Local repro.** Reproduce the CI check locally with:

```sh
bash scripts/check-action-pins.sh
```

Exit 0 = every `uses:` line is SHA-pinned; exit 1 = one or more tag-pinned actions (the failing lines are printed to stderr with file:line + a reconcile hint); exit 2 = environment misconfigured (workflows dir missing, no `.yml` files).

### Dependency hygiene

CI runs through [StepSecurity Harden-Runner](https://github.com/step-security/harden-runner) in **audit mode** as the first step of every job in every workflow under `.github/workflows/` (slice 117). The action instruments the runner to record outbound network calls, file writes, and process executions — catching supply-chain attacks (malicious package post-installs, exfiltration, compromised actions) that PR-time analysis cannot see. Audit mode never fails a job; the data lands on the StepSecurity dashboard at `https://app.stepsecurity.io/github/mgoodric/security-atlas/actions/runs/<run-id>` (one URL per workflow run, surfaced via the "Action Security Insights" link in the GitHub Actions job summary). If you see new outbound destinations flagged in the workflow summary that you can't justify, surface them in the PR description; we treat unexplained egress as a review-blocker even while we're in audit mode. Block-mode promotion (with an `allowed-endpoints` allowlist derived from the audit baseline) is filed as slice 118, `not-ready`, gated on ~2 weeks of audit-mode data.

When you touch any file under `internal/db/queries/`, run `just sqlc-generate` (which version-checks first) and commit the regenerated `internal/db/dbx/*.go` in the same commit as the query edit. Do NOT hand-edit `internal/db/dbx/*.go` outside of the two documented hand-narrows (`policies.sql.go` `AckDenominator`/`AckNumerator`, `scf_anchors.sql.go` `StateResult`/`StateFreshnessStatus`) — both are annotated in place and explained in slice 109's decisions log. New queries should regen cleanly; if the regen also rewrites unrelated files, your local sqlc binary is the wrong version (compare against `SQLC_VERSION` in `justfile`).

### Branch protection

`main` is protected. The current ruleset (as of slice 127):

- All 10 named CI status checks must report success (Go build/test/lint/integration, Frontend install+build, Python ruff, pre-commit, Analyze go/javascript-typescript via CodeQL, GitGuardian — full list in [`.github/branch-protection.json`](./.github/branch-protection.json)).
- Linear history (squash- or rebase-merge only).
- Force-push blocked.
- Direct push to `main` blocked.
- All review-thread comments resolved.
- `enforce_admins: true` (maintainer cannot bypass).
- `required_approving_review_count` is currently `null` (solo maintainer; documented in the file's `$deviations_from_slice_050_AC11` block). Re-evaluate when the contributor base passes ~3 active committers.

The file at [`.github/branch-protection.json`](./.github/branch-protection.json) is the **source of truth for intent**. The live GitHub branch-protection config on `main` is the **source of truth for enforcement**. They must be kept in sync.

**Apply ritual.** When you edit `.github/branch-protection.json`, push the change to live by running:

```sh
bash scripts/apply-branch-protection.sh
```

The script reads the file, strips the `$`-prefixed annotation keys (GitHub's PUT API rejects unknown top-level fields), `PUT`s the cleaned payload to `repos/mgoodric/security-atlas/branches/main/protection`, then re-reads live and asserts the contexts list converged. Re-running the script with no file change is a no-op (idempotent — P0-A2). The equivalent manual call is `gh api -X PUT repos/mgoodric/security-atlas/branches/main/protection --input <(jq 'with_entries(select(.key | startswith("$") | not))' .github/branch-protection.json)`.

**Failure mode of omission.** If you edit the file but skip the apply, the file's "source of truth" claim becomes a lie and security controls degrade silently. This exact failure cost the project four PRs of churn during the 2026-05-17/18 cascade-unblock session — see slice 127's narrative.

**Drift detection (slice 127 + slice 158).** Two informational CI surfaces watch for drift:

- `Infra · branch-protection (PR-time validate)` runs on every PR. It validates ONLY the shape of `.github/branch-protection.json` (valid JSON + non-empty `required_status_checks.contexts`). It does not call the GitHub API, so it needs no elevated token and cannot leak a credential. The sticky PR comment fires only when the file shape is broken.
- `Infra · branch-protection (live drift)` runs on push to `main`. It compares the file against the live ruleset via `gh api`. Drift findings surface in the workflow run summary + as an artifact (no PR exists on a push event).

The live job needs the `Administration: Read` repo permission, which `GITHUB_TOKEN` cannot have. Slice 158 grants it via a fine-grained PAT in `secrets.BRANCH_PROTECTION_READ_TOKEN` ([ADR 0005](./docs/adr/0005-branch-protection-pat-vs-app.md) records the PAT-vs-GitHub-App decision and the maintainer setup steps including the 90-day rotation ritual). Until the secret is configured, the live job exits with a clear "secret not configured" message and stays informational.

**Local repro for drift findings.** Reproduce the CI check locally with:

```sh
bash scripts/check-branch-protection-drift.sh
```

Exit 0 = in sync; exit 1 = drift detected (diff printed on stderr); exit 2 = environment misconfigured (missing `gh` / `jq`, malformed file). Locally the script reads whatever `gh` is authenticated as (`gh auth status`); CI reads the PAT.

### Workflow linting (actionlint)

`.github/workflows/*.yml` is linted by [actionlint](https://github.com/rhysd/actionlint) on every commit (pre-commit hook) and every PR (`pre-commit · all hooks` CI job, slice 158). The most common error class actionlint catches is **invalid `GITHUB_TOKEN` permission scopes** — PR #311 (closed unmerged) tried to add `administration: read` to a job's `permissions:` block, but `administration` is not a valid scope, and GHA silently dropped the entire workflow file at parse. The actionlint guard prevents that mistake recurring.

**Install locally.** macOS: `brew install actionlint`. Debian/Ubuntu: `apt install actionlint` (or download a release binary from https://github.com/rhysd/actionlint/releases). The pre-commit hook is a `local` entry that calls the system binary — no extra Python wrapper to install.

**Reproduce a CI finding.**

```sh
actionlint -shellcheck "" -no-color .github/workflows/*.yml
```

The `-shellcheck ""` flag disables actionlint's embedded shellcheck pass — pre-existing `SC2034`/`SC2045` warnings in some `run:` blocks are not the failure mode the guard is for and would be a noisy distraction. The wrong-permission-scope error fires regardless.

**Smoke test (slice 158 AC-17).** `bash scripts/check-actionlint-fixture.sh` asserts that actionlint still flags the fixture at `scripts/actionlint-fixture-invalid-scope.yml` (the exact `administration: read` mistake). If this test ever passes incorrectly (actionlint stopped flagging the scope), the guard is silently broken and a follow-up slice should pick a different still-invalid scope as the canary.

**Valid `GITHUB_TOKEN` scopes.** Per actionlint 1.7.12 + the GHA docs: `actions`, `artifact-metadata`, `attestations`, `checks`, `contents`, `deployments`, `discussions`, `id-token`, `issues`, `models`, `packages`, `pages`, `pull-requests`, `repository-projects`, `security-events`, `statuses`. **There is no `administration` scope on `GITHUB_TOKEN`.** Higher-privilege reads (branch-protection, org membership, etc.) require a PAT or GitHub App, as documented in [ADR 0005](./docs/adr/0005-branch-protection-pat-vs-app.md).

### API spec

The REST API surface has a machine-readable contract at
[`docs/openapi.yaml`](./docs/openapi.yaml) — an OpenAPI 3.1 document
generated deterministically from the in-tree route declarations. It is
the single source of truth for what HTTP endpoints the platform
exposes, what auth tier each requires, and which are internal-only
(filtered out of the public Redoc render). The gRPC surface stays
specified separately in `proto/*.proto` — this OpenAPI spec covers
REST only (slice 140 P0-A7).

**When to update the spec.** Every PR that adds, removes, or changes
an HTTP route on `chi.Mux` MUST also edit
[`internal/api/openapi/routes.go`](./internal/api/openapi/routes.go)
to reflect the change AND re-run `just openapi-generate` to refresh
the committed YAML. The BLOCKING `openapi-drift-check` CI guard fails
the build on any mismatch — see below.

**How to regenerate.**

```sh
just openapi-generate
```

The recipe runs `go run ./cmd/atlas-openapi --out docs/openapi.yaml`.
Output is deterministic: two back-to-back runs produce byte-identical
results. Commit the regenerated `docs/openapi.yaml` along with the
`internal/api/openapi/routes.go` edit in the same PR.

**Drift-detect guard.** The `openapi-drift-check` job in
`.github/workflows/ci.yml` runs `scripts/check-openapi-drift.sh` on
every PR and every push to `main`. The job is **blocking** — a spec
out of sync with handler reality fails the build and the merge button
stays disabled. This is the slice-140 D3 mitigation (same precedent as
slice 128's `actions-pin-check`): a misleading spec is silent control
degradation, not an informational nice-to-have.

The script checks two things:

1. **Inventory drift** — the committed `docs/openapi.yaml` matches
   `cmd/atlas-openapi`'s output against the current `RouteSpecs`.
   Catches "edited routes.go but forgot to regen the spec".
2. **Coverage drift** — every chi route registration grep-extracted
   from `internal/api/*/` is declared in `RouteSpecs`. Catches "added
   a chi route but forgot to declare it in `RouteSpecs`".

**Local repro.**

```sh
bash scripts/check-openapi-drift.sh
```

Exit 0 = no drift; exit 1 = drift detected (per-file fix steps printed
to stderr); exit 2 = environment misconfigured (missing Go toolchain,
malformed routes.go).

**Operator post-merge ritual.** When this slice (or any future PR that
adds a new BLOCKING CI guard to `required_status_checks`) merges, the
maintainer runs:

```sh
bash scripts/apply-branch-protection.sh
```

to push the updated `.github/branch-protection.json` contexts list to
the live GitHub branch-protection config on `main`. Without this step,
the file-as-source-of-truth claim is structurally untrue (the new
required check is declared but not enforced) — see the slice 127
narrative for the exact failure mode this protects against.

**Redoc UI.** The user-facing render of the spec lives at
[`docs-site/docs/api/index.md`](./docs-site/docs/api/index.md) and
ships as part of the mkdocs Material site at
`/api/` on the published docs. Internal endpoints (`/health`,
`/metrics`, `/v1/version`, `/v1/install-state`) carry `x-internal:
true` in the source spec and are filtered out at build time by
[`docs-site/hooks/openapi_pipeline.py`](./docs-site/hooks/openapi_pipeline.py)
before reaching the page (slice 140 P0-A3 mitigation against
information disclosure).

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

## Empty-set robustness

Every `GET /v1/*` list or aggregate endpoint MUST return `200 OK` with a
well-shaped empty envelope on a zero-row database — NEVER `500 Internal
Server Error`. Slice 150 made this a constitutional invariant after a
v1.10.0 operator report surfaced three panels (recent drift, board
metrics, policies) crashing the dashboard on a fresh install.

The convention:

| Surface                    | Empty-row response                                                                                                                                                                                                                     |
| -------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| List endpoint              | `{ <plural>: [], count: 0 }` — the plural key is an array, never `null`                                                                                                                                                                |
| Aggregate panel            | A populated object with zero-valued numerics + an empty array for any embedded list                                                                                                                                                    |
| Bootstrap-only credentials | List endpoints that key off `cred.UserID` (e.g. `/v1/me/acknowledgments`) MUST return 200 empty when the UserID is not a UUID — bootstrap / service-account credentials are valid callers and the dashboard panel must not 500 on them |

Mechanics:

- The handler's `make(...)` for the row slice goes BEFORE the database
  call, not after — so the empty slice is the default, and the loop is
  purely additive.
- The wire shape uses an empty JSON array `[]`, not `null` — the
  frontend iterates the array directly.
- Division-by-zero on rate / percentage fields is a separate concern:
  return `null` for `percent` when the denominator is zero (the
  slice-023 `rateResponse` is the canonical shape).
- A non-UUID identifier on a tenant-scoped read is a SERVICE-ACCOUNT
  marker, not a 500 — return the empty envelope.

The gate:

- **Per-package integration test.** Every package that owns a list /
  aggregate endpoint ships an `empty_set_integration_test.go` (build
  tag `integration`) that exercises the 0-row path against real
  Postgres + RLS and asserts the wire-shape contract. See
  `internal/api/freshnessdrift/empty_set_integration_test.go` for the
  canonical shape.
- **Cross-cutting sweep.** `internal/api/emptyset/audit_integration_test.go`
  hits every GET list/aggregate endpoint in one subtest table. Adding
  a new GET list endpoint to the platform is a constitutional
  commitment to add a row to that test's `cases` slice.

`go test -tags=integration -p 1 ./internal/api/emptyset/...` is what
the audit runs locally. The same path runs in CI as part of the `Go ·
integration (Postgres RLS)` check, so a regression that re-introduces
a 500-on-empty fails the merge before the PR can land.

## Data exports

The slice 135 export library (`internal/export/`) backs three audit-log
download endpoints — CSV, JSON, and XLSX — under
`GET /v1/admin/audit-log/export` plus the BFF at
`web/app/api/audit-log/export/route.ts`. Slice 145 added two
operator-facing knobs on top of slice 135's baseline:

**1. `?include_payload=<bool>` — forensics vs. external-audit-handoff**

The export emits a `payload_json` column populated with the raw
audit-log row payload — control titles, evidence kinds, before/after
diffs, etc. That blob is correct for forensic use cases (internal
incident response, in-house compliance review) but is more than an
external auditor or third party needs. The query parameter lets the
operator choose:

- **`?include_payload=true` (default)** — forensics. Full payload in
  every row. Preserves the slice 135 wire shape; existing callers see
  no change.
- **`?include_payload=false`** — external-audit-handoff. CSV emits an
  empty cell; JSON emits the literal `null` (not `""`); XLSX emits an
  empty cell. All other columns (occurred_at, actor_id, tenant_id,
  kind, target_type, target_id, action, row_id, actor_name) render
  normally — only `payload_json` is redacted.

The meta-audit row (`me_audit_log WHERE action = 'audit_log_export'`)
records the `include_payload` value used so an operator can prove
which export went to which audience. Legacy slice 135 rows that
predate slice 145 do not carry the key — readers MUST treat absence
as `true` (the slice 135 default).

curl examples:

```sh
# Forensics workflow — full payload (default).
curl -sH "Authorization: Bearer $TOK" \
  "https://atlas.example/v1/admin/audit-log/export?format=csv&from=2026-04-01T00:00:00Z&to=2026-04-30T23:59:59Z" \
  -o audit-2026-04-forensics.csv

# External-audit-handoff workflow — payload redacted.
curl -sH "Authorization: Bearer $TOK" \
  "https://atlas.example/v1/admin/audit-log/export?format=csv&from=2026-04-01T00:00:00Z&to=2026-04-30T23:59:59Z&include_payload=false" \
  -o audit-2026-04-handoff.csv
```

**2. Per-(tenant, user) concurrency cap**

A buggy client or an authenticated misbehaving caller firing N
concurrent `/export` requests would saturate the per-tenant pgxpool —
each export streams for minutes, degrading every other endpoint in
that tenant. Slice 145 adds a process-wide semaphore keyed on
`(tenant_id, user_id)` with default cap 2. Excess requests get
`429 Too Many Requests` with a `Retry-After: 30` header AND a JSON
body explaining the limit so operators reading curl output without
`-i` still see the message.

Tune via env (no restart of dependent containers needed):

```
ATLAS_EXPORT_MAX_CONCURRENT_PER_USER=4
```

The cap is **per-(tenant, user)**, NOT global — a super_admin running
exports across five tenants is NOT throttled by cap=2 in any single
tenant. Cross-tenant scope is the granularity at which the DoS lives
(per-tenant pgxpool), and so it is the granularity at which the cap
applies.

The 429 outcome path writes a meta-audit row with
`result=denied:concurrency_cap_exceeded` — operators can grep
`me_audit_log` for these events to detect a runaway script.

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

## Contributing an `evidence_kind` schema

Schemas live in-tree at `internal/api/schemaregistry/schemas/<kind>/<semver>.json`. The platform embeds them at compile time via `//go:embed`; new schemas land as a PR against this repo. The conventions below are the result of canvas open-question #9 + #17 resolution (see `Plans/canvas/11-open-questions.md`).

**Three rules govern community schema contributions:**

### 1. In-tree until trigger

Schemas stay in-tree (`internal/api/schemaregistry/schemas/`) until **either** (a) the schema count crosses 100 **or** (b) community schema PRs exceed ~1 per week sustained. At that point a maintainer files a slice to migrate to an out-of-tree `security-atlas-schemas` registry repo. Today (16 schemas, low-frequency community contribution) the in-tree model is the right shape.

Practically: open a PR against this repo with the new schema at `internal/api/schemaregistry/schemas/<kind>/1.0.0.json`. The `go test ./internal/api/schemaregistry/...` suite round-trips it through embed-load and Postgres at boot.

### 2. Maintainer-only review (for now)

Every community schema PR requires a maintainer's approval before merge. There is no "verified contributor" tier yet. The expectation is that maintainers scrutinize:

- **JSON Schema correctness** (CI's `go test ./internal/api/schemaregistry/...` already covers this structurally)
- **Semver discipline** (CI's `CheckAdditiveOver` in `internal/api/schemaregistry/additive.go` rejects non-additive minor bumps)
- **`x-default-scf-anchors` accuracy** (THE manual checkpoint — does the contributor's claim "my schema covers IAC-06" actually hold? Loose anchor declarations weaken the UCF graph; maintainer review is the load-bearing mitigation)
- **Naming convention** (`<vendor>.<resource>.<observation>` for connector-produced; `manual.<observation>` for operator-attested)

A "verified contributor" CODEOWNERS-style tier will be designed when **both** (a) >20 community-contributed schemas have landed **and** (b) ≥3 contributors have shipped >5 schemas each. The design happens with those contributors in the room, not speculatively.

### 3. 90-day deprecation window for breaking-major bumps

When a schema's `2.0.0` (or `3.0.0`, etc.) lands, the previous major's latest version stays in the registry for **at least 90 days from the day v2.0.0 lands on `main`**. During the window:

- Both `v1.x.x` and `v2.x.x` are pushable by connectors / SDKs.
- The platform marks `v1`-versioned records as "deprecated since `<v2.0.0-land-date>`" in the UI.
- Connector contributors get a 90-day migration runway.

After the window:

- A maintainer files a PR removing the `v1.x.x` schema files from `internal/api/schemaregistry/schemas/<kind>/`.
- `v1` records remain queryable in the evidence ledger forever (append-only invariant); new `v1.x.x` pushes return `400 schema deprecated`.

CI enforces the floor: any PR that removes a schema version file younger than 90 days fails the `schema-removal-age` check. The check has an explicit override label (`[deprecation-override]` on the PR) for emergencies (e.g., a schema was published with a security-sensitive defect and must be unpublished immediately) — overrides require a maintainer's approval and a note in the audit log under `docs/audit-log/`.

Pattern source: OpenTelemetry semantic-conventions deprecation model. Battle-tested at scale; copy verbatim rather than designing our own.

---

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
