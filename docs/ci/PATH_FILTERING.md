# CI path-based filtering (slice 061)

This repo skips the expensive Go / Frontend / Python / Proto jobs on
PRs that touch only documentation. Security and auto-format jobs
(CodeQL, GitGuardian, pre-commit) always run.

## Why not `paths-ignore:` at workflow level?

The obvious solution — `paths-ignore: ['**/*.md', 'Plans/**', 'docs/**']`
at the `on:` level of a workflow — is **wrong** for this repo.

Branch protection (`.github/branch-protection.json`) lists required
status-check names. If the workflow does not run, those check names
never report a status. GitHub shows them as
`Expected — waiting for status to be reported` and the merge button
stays disabled forever. The PR is unmergeable not because anything
failed but because the gate never resolved.

The same trap kills `paths:` filters at the workflow level — the
workflow only runs when one of the listed paths matches, so a
docs-only PR sees no workflow, no checks, no merge.

## The pattern this repo uses

Path filtering moves _inside_ the workflow. The workflow always runs.
A `changes` job runs first and uses
[`dorny/paths-filter@v3`](https://github.com/dorny/paths-filter) to
classify the PR as `code: true` or `code: false`.

Every expensive job is split into two siblings:

- **Real job** — gated on `if: needs.changes.outputs.code == 'true'`.
  Runs only when the PR touches code.
- **Stub job** — same `name:`, gated on
  `if: needs.changes.outputs.code != 'true'`. Runs only when the PR
  touches no code. One `echo` step, finishes in < 5 s.

The two jobs are mutually exclusive — exactly one runs per PR. Both
post their result under the same job-level `name:`. Branch protection
sees a single named check resolve green either way.

## Why the stub-job name MUST match the real job

GitHub Actions posts each job's status as a check named after the job
`name:` field (or the default `id`). Branch protection
(`.github/branch-protection.json`) matches required checks by name.
If the stub used `name: Go · build + test (docs)`, branch protection
would still wait forever for `Go · build + test` to report — and
deadlock.

The stub job-level `name:` therefore **must** be byte-for-byte
identical to the real job's. The `if:` conditions guarantee mutual
exclusion so GitHub never sees two parallel runs of the same check.

When `.github/branch-protection.json` is updated, audit the workflow
file in the same PR. Real-job rename + stub-job rename + protection
update all land together.

## Path filter — what counts as `code`

The filter list lives inline in `.github/workflows/ci.yml` under the
`changes` job. `code: true` if the PR touches any of:

- Go: `**/*.go`, `go.mod`, `go.sum`, `go.work`, `go.work.sum`
- Frontend: `**/*.ts`, `**/*.tsx`, `**/*.js`,
  `package.json`, `package-lock.json`, `pnpm-lock.yaml`,
  `**/package.json`, `**/package-lock.json`, `web/**`
- Python: `**/*.py`, `pyproject.toml`, `uv.lock`, `oscal-bridge/**`
- Migrations + SQL: `migrations/**`, `sql/**`, `atlas.hcl`
- Proto + generated: `proto/**`, `gen/**`, `buf.yaml`, `buf.gen.yaml`
- Policies + schemas: `policies/**`, `schemas/**`
- Source trees: `internal/**`, `cmd/**`, `pkg/**`, `connectors/**`
- Containers + deploy: `Dockerfile*`, `**/Dockerfile`,
  `docker-compose*.yml`, `deploy/**`
- Build / tool config: `.github/workflows/**`, `justfile`,
  `.golangci.yml`, `.pre-commit-config.yaml`, `scripts/**`

Anything else is `code: false`. The big buckets are `*.md`,
`Plans/**`, `docs/**`, `LICENSE`, `CHANGELOG.md`, `.gitignore`,
`.editorconfig`.

## What always runs

Three categories never gate on `code`:

| Job                           | Why                                                           |
| ----------------------------- | ------------------------------------------------------------- |
| `pre-commit · all hooks`      | Auto-formats markdown + yaml; we want it on docs PRs too      |
| `Analyze (go)` (CodeQL)       | Security scan; never skipped, even on docs PRs                |
| `Analyze (javascript-…)`      | Same                                                          |
| `GitGuardian Security Checks` | Secret scan; runs as a GitHub App, not a workflow file        |

Cost optimization is fine for build/test/lint. Security scans and
the auto-formatter are not on the optimization budget.

## Adding a new workflow

Most workflows don't need path-filtering. Add gating ONLY if both
are true:

1. The workflow is triggered by `pull_request:` (so it runs on docs
   PRs that have no code intent).
2. The workflow runs an expensive job (~30 s+) that has no value on
   docs-only PRs.

If yes, mirror the `changes` + real-job + stub-job pattern in
`ci.yml`. Otherwise (release pipelines, container-publish, scheduled
scans), leave the workflow as-is.

## Adding a new required check

When a new job becomes required:

1. Add the job (and its stub sibling if it's expensive) to the right
   workflow.
2. Update `.github/branch-protection.json` `required_status_checks.contexts`
   with the new name.
3. Apply via `gh api -X PUT
   repos/mgoodric/security-atlas/branches/main/protection
   --input .github/branch-protection.json`.

If the new check is always-on (security/format/etc.), no stub is
needed.

## Verification

A docs-only PR (e.g. a `_STATUS.md` reconcile) should:

- Have the `Detect changed paths` job run in ~5 s.
- Have all six `* stub` jobs run in < 30 s and post pass under the
  required-check name.
- Have `pre-commit · all hooks` run as normal (~30 s).
- Have CodeQL run as normal (~3–5 min).
- Have GitGuardian run as normal (~15 s).
- Total billable runner minutes: ~2 (down from ~10 before slice 061).

A code PR should be unchanged from the pre-slice-061 baseline — same
jobs, same durations.
