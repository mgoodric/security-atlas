# CI hardening — required gates, informational signals, and how to reproduce them locally

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - Which CI checks gate a PR merge vs which are informational
    - How to reproduce every required gate on your laptop before pushing
    - What the three CI hardening slices (117 + 127 + 128) added and why
<!-- prettier-ignore-end -->

A PR cannot merge until the **required checks** pass. Some additional
checks run informationally — they surface signal without blocking
merge. This page maps every check to what it covers and how to run it
locally.

## Required checks (block merge)

These are configured in `.github/branch-protection.json` and enforced
on `main` (and recently `next/*` per slice 127's drift fix).

### `Go · build + test`

| Surface           | Entry point                                                                                       |
| ----------------- | ------------------------------------------------------------------------------------------------- |
| What it covers    | Per-package Go compilation + unit tests.                                                          |
| Reproduce locally | `just test-go` or `go test ./...`                                                                 |
| Coverage gate     | Per-package floor in `cmd/scripts/coverage-thresholds.json`; gate is `cmd/scripts/coverage-gate`. |

Raising a floor requires writing the tests in the SAME PR as the
threshold lift. The gate is a ratchet — it must monotonically
increase.

### `Go · integration (Postgres RLS)`

| Surface           | Entry point                                                       |
| ----------------- | ----------------------------------------------------------------- |
| What it covers    | RLS coverage, migrations, Postgres-backed handlers, NATS + MinIO. |
| Reproduce locally | `just test-integration`                                           |
| Floor             | Presence-of-tests is the gate — CI fails on test failure.         |

Integration tests live under `internal/<pkg>/*_test.go` with a
`//go:build integration` build tag.

### `Go · lint`

| Surface           | Entry point                                     |
| ----------------- | ----------------------------------------------- |
| What it covers    | `gofmt`, `goimports`, `golangci-lint` (strict). |
| Reproduce locally | `just lint-go`                                  |

### `Frontend · vitest`

| Surface           | Entry point                                                    |
| ----------------- | -------------------------------------------------------------- |
| What it covers    | Module-level web logic: BFF route handlers, `lib/api.ts`, etc. |
| Reproduce locally | `cd web && npm run test`                                       |

### `Frontend · Playwright e2e`

| Surface           | Entry point                                                  |
| ----------------- | ------------------------------------------------------------ |
| What it covers    | User flows: dashboard, control detail, audit workspace, etc. |
| Reproduce locally | `cd web && npm run test:e2e`                                 |

See `web/e2e/README.md` for the seed-data harness and Playwright config.

### `Frontend · build`

| Surface           | Entry point                                                        |
| ----------------- | ------------------------------------------------------------------ |
| What it covers    | Next.js `output: standalone` build for the slice-037 Docker shape. |
| Reproduce locally | `cd web && npm run build`                                          |

### `Frontend · lint`

| Surface           | Entry point                           |
| ----------------- | ------------------------------------- |
| What it covers    | `eslint`, `prettier`, `tsc --strict`. |
| Reproduce locally | `cd web && npm run lint`              |

### `Python · lint` / `Python · oscal-bridge`

| Surface           | Entry point                                       |
| ----------------- | ------------------------------------------------- |
| What it covers    | `ruff` lint + format on the OSCAL bridge package. |
| Reproduce locally | `just lint-python` and `just oscal-bridge-test`   |

### `proto-ci`

| Surface           | Entry point                                       |
| ----------------- | ------------------------------------------------- |
| What it covers    | `buf lint`, `buf format`, `buf generate` no-diff. |
| Reproduce locally | `just proto-ci`                                   |

### `precommit`

| Surface           | Entry point                                                                  |
| ----------------- | ---------------------------------------------------------------------------- |
| What it covers    | The full pre-commit hook chain — formatting, basic linting, CHANGELOG check. |
| Reproduce locally | `pre-commit run --all-files`                                                 |

The CHANGELOG-bullet check has been caught **4×** when forgotten —
running pre-commit locally before push is the cheapest defense.

### `openapi-drift-check`

| Surface           | Entry point                                                      |
| ----------------- | ---------------------------------------------------------------- |
| What it covers    | `docs/openapi.yaml` matches the live handler surface (no drift). |
| Reproduce locally | `just openapi-drift-check`                                       |

### `sqlc-drift`

| Surface           | Entry point                                           |
| ----------------- | ----------------------------------------------------- |
| What it covers    | Generated Go from `sqlc` is in sync with the queries. |
| Reproduce locally | `just sqlc-generate` (then check `git diff`)          |

Slice 109 pinned sqlc to `v1.31.1` in the `justfile` — version drift
across contributors is a chronic source of CI flakes.

### `actions-pin-check`

| Surface           | Entry point                                                         |
| ----------------- | ------------------------------------------------------------------- |
| What it covers    | Every GitHub Action reference is commit-SHA-pinned, not tag-pinned. |
| Reproduce locally | `scripts/check-action-pins.sh`                                      |

Slice 128 added this — tag-jacking is the documented supply-chain
attack vector this defends against.

### `branch-protection-drift-validate`

| Surface           | Entry point                                                        |
| ----------------- | ------------------------------------------------------------------ |
| What it covers    | `.github/branch-protection.json` is structurally valid + complete. |
| Reproduce locally | `scripts/check-branch-protection-drift.sh`                         |

A `-live` variant runs nightly against the live GitHub config; that
one is informational (see below).

## Informational checks (do NOT block merge)

These run on every PR; failures surface in the PR conversation but do
NOT block the merge button.

| Check                              | Why informational                                                                                                                   |
| ---------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| `codecov/patch`, `codecov/project` | Codecov is a noisy signal — useful as feedback, lethal as a gate.                                                                   |
| `Go · sqlc generate diff`          | Reports what `sqlc-generate` would produce against the current schema. Adds info without forcing the engineer to regenerate mid-PR. |
| `branch-protection-drift-live`     | Reports whether live GitHub config has drifted from the JSON. Engineering knob, not a per-PR knob.                                  |
| `npm-audit`                        | Reports npm advisories. False-positive-prone; tracked separately.                                                                   |
| `govulncheck`                      | Reports Go module vulnerabilities. Same FP concern as npm-audit.                                                                    |
| `trivy-image`                      | Container vulnerability scan. Reported on the artifact, not the source.                                                             |
| `phantom-deps`                     | Detects implicit (un-declared) dependencies. Informational because the noise floor is higher than the signal floor today.           |

## The three CI hardening slices

### Slice 117 — StepSecurity Harden-Runner

Adds the `step-security/harden-runner` action to every workflow as the
first step. The runner is instrumented to monitor outbound network
calls, file writes, and process executions. v1 ships in **audit
mode** — the runner records but does not block; the dashboard at
`app.stepsecurity.io/github/<owner>/<repo>` shows what would be
blocked.

A follow-on slice flips to **block mode** after a 2-week soak; see
slice 117 doc for the cutover criteria.

### Slice 127 — branch-protection drift detection

Adds `branch-protection-drift-validate` (required on every PR) and
`branch-protection-drift-live` (nightly informational). The first
catches the case where someone removes a check name from the JSON;
the second catches the case where someone flips the live GitHub
config without updating the JSON.

Before slice 127, the JSON and the live config silently drifted —
slice 127 was filed the day that drift was discovered.

### Slice 128 — SHA-pin every action

Replaces tag-pin (`actions/checkout@v6`) with SHA-pin
(`actions/checkout@<full sha> # v6`) for every action reference in
every workflow. Adds `actions-pin-check` (required on every PR) to
prevent regression.

The defense is against **tag-jacking**: an attacker who compromises
an action's git push permissions can move a floating tag to point at
malicious code; every consumer pinned to `@v6` picks it up silently.
SHA-pin defeats this — the SHA is content-addressed.

## Running the full required set locally

Before pushing, run every required check:

```sh
just lint           # all three: Go, frontend, Python
just test-go        # Go unit
just test-integration  # Go integration (Postgres + NATS + MinIO via docker compose)
cd web && npm run test && npm run test:e2e && npm run lint && npm run build && cd ..
just openapi-drift-check
just proto-ci
just sqlc-generate  # then check git diff is empty
pre-commit run --all-files
```

Pre-commit alone catches CHANGELOG bullet omission, formatting drift,
and trivial linting — running it last is a no-cost defense against
the most common cause of CI-only failures.

## Branch protection JSON

The full required-checks set is the JSON at
`.github/branch-protection.json`. To inspect:

```sh
jq '.required_status_checks.contexts' .github/branch-protection.json
```

Changes to this file go through PR review like any other change.
**Do NOT skip branch protection** via admin override unless the user
explicitly requests it — slice 127's discipline assumes nobody
silently flips the live config.

## Next steps

- [Audit logs →](audit-logs.md) — what the CI authorization decisions
  feed into
- [Connector authoring →](connector-authoring.md) — what passes CI
  before shipping
- The slice docs: [`docs/issues/117-stepsecurity-harden-runner.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/issues/117-stepsecurity-harden-runner.md), [`docs/issues/127-branch-protection-drift-fix.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/issues/127-branch-protection-drift-fix.md), [`docs/issues/128-sha-pin-all-github-actions.md`](https://github.com/mgoodric/security-atlas/blob/main/docs/issues/128-sha-pin-all-github-actions.md)

---

## Was this helpful?

Tell us in [GitHub
Discussions](https://github.com/mgoodric/security-atlas/discussions).
