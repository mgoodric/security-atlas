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

## Tiers: `go` and `web` (#2099)

`code` answers "does this PR touch code at all". Two finer outputs answer
"which half of the code": a Go-only PR should not pay for the web jobs,
and a web-only PR should not pay for the Go integration shards. The cost
being cut is concurrent jobs, not minutes: the repo is capped at 40
concurrent jobs, and a full run is ~65 job instances.

| Output | Gates                                                                                                                                                                                                           |
| ------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `go`   | `build-go`, `tests-integration-shard`, `tests-integration`, `lint-go`, `fuzz`, `govulncheck`, `sqlc-drift`, `lint-python`, `oscal-bridge`, `assertion-density`                                                  |
| `web`  | `build-atlas` (only the Playwright jobs download its binary), `build-frontend`, `frontend-vitest`, `frontend-lint`, `frontend-playwright`, `frontend-playwright-prod-build`, `frontend-ui-honesty`, `npm-audit` |
| `code` | everything else that is path-filtered: `proto`, `test-self-host-bundle`, `helm-lint`, `phantom-deps`, `trivy-image`                                                                                             |

Each stub twin gates on the complement of its real job's output, so the
required check names, the twins and branch protection are unchanged.
Only which twin fires changes.

### The allowlists and the shared set

- **Go tier allowlist:** `**/*.go`, `go.mod`, `go.sum`, `go.work`,
  `go.work.sum`, `internal/**`, `cmd/**`, `pkg/**`, `connectors/**`, and
  Python (`**/*.py`, `oscal-bridge/**`, `pyproject.toml`, `uv.lock`,
  nested ones too), because integration legs B3/B5 use uv.
- **Web tier allowlist:** `web/**`, `package.json`, `package-lock.json`
  (root and nested), `pnpm-lock.yaml`.
- **Shared set** (both tiers run): `migrations/**`, `sql/**`, `atlas.hcl`,
  `sqlc.yaml`, `proto/**`, `gen/**`, `buf*.yaml`, `policies/**`,
  `schemas/**`, `fixtures/**`, `deploy/**`, Dockerfiles, compose files,
  `.github/**`, `justfile`, `scripts/**`, `.golangci.yml`,
  `.pre-commit-config.yaml`, `.nvmrc`, `codecov.yml`.

### The skip rule: every file, or nothing

A tier is skipped only when **every** changed file sits in the **other**
tier's allowlist and **no** file is in the shared set. One file outside
that, whether shared or unclassified (a `tools/` script, a new top-level
directory), runs both tiers. The rule fails toward running.

The `changes` job implements it with two extra `dorny/paths-filter` steps,
both `pull_request`-only:

1. `tiers`, with `predicate-quantifier: every`. Under `every` a file
   matches a filter only when it matches every pattern, so a filter of
   `**` followed by `!<allowlist>` patterns matches exactly the files
   outside that allowlist. `non_web` is true when some file is not
   web-only; `non_go` when some file is not Go-only.
2. `shared`, with the default `some` quantifier, listing the shared set.
   It overrides the allowlists, so a `scripts/x.py` or a
   `deploy/**/package.json` still runs both tiers.

The `resolve` step then sets `go` and `web`:

- `push` (to `main`) and `merge_group`: both tiers equal `code`, whatever
  the diff. `main` runs everything, so it backstops a misclassification.
- `pull_request` with `code=false`: both false (the docs-only path,
  unchanged).
- `pull_request` with `code=true`: `go=false` only if `non_web` is false
  and `shared` is false; `web=false` only if `non_go` is false and
  `shared` is false. An empty filter output reads as `true`. If both would
  come out false (every file matched both allowlists, e.g. a
  `package.json` under `connectors/`), both are forced true.

The slice 415 fail-closed handling of `code` runs first and is unchanged.
The job summary shows the raw filter values and the resolved tiers.

### The merge-gate reads tiers from the workflow

`CI · merge-gate` forgives a `skipped` leg only when that leg's own tier
resolved false. It reads each leg's tier from the leg's `if:` in
`.github/workflows/ci.yml` (sparse checkout of that one file), so there is
no second job-to-tier list to drift. A leg it cannot classify falls back
to `code`, the widest tier, so a parse failure can refuse a skip but
never grant one.

### The stub-twin guard

`scripts/check-stub-twins.sh` (CI job `stub-twin-check`, `just
check-stub-twins`, in merge-gate's `needs:`) parses `ci.yml` and fails
when a gated real job and its twin disagree:

| Finding          | Meaning                                                                       |
| ---------------- | ----------------------------------------------------------------------------- |
| `MISSING-TWIN`   | a real job with no other job of the same `name:`                              |
| `NOT-COMPLEMENT` | the twin's `if:` is not the complement on the same output (`code`/`go`/`web`) |
| `ORPHAN-STUB`    | a stub with no matching real job                                              |
| `DUPLICATE`      | more than two jobs share a gated `name:`                                      |
| `UNRECOGNISED`   | a job `if:` reads a `changes` tier output in a shape the guard cannot pair    |

The recognised shapes are `X == 'true'` / `X != 'true'`, the same with a
leading `always() &&` on the real job, and the `pull_request`-only pair
`github.event_name == 'pull_request' && X == 'true'` /
`github.event_name != 'pull_request' || X != 'true'`. Real jobs that
deliberately have no stub (`build-atlas`, `tests-integration-shard`) sit
on the script's `NO_TWIN` list with a reason; a stale entry fails.

When moving a job between tiers, change the real job's `if:` and its
twin's together. The guard refuses one without the other.

## The filter on `merge_group` events (slice 415)

The workflow also runs on `merge_group` (GitHub's merge queue). That event
has **no PR context**, so the filter cannot diff against a PR base — and a
wrong `code=false` there is worse than on a PR: the stub-twin satisfies a
required check and unbuilt code lands on `main`.

The `changes` job handles it in two layers:

1. **Explicit base.** `dorny/paths-filter` is given
   `base: ${{ github.event_name == 'merge_group' && github.event.merge_group.base_ref || '' }}`.
   On `pull_request`/`push` that is `''`, so nothing about the behaviour
   documented above changes. On `merge_group` it is `refs/heads/main` —
   the base the event itself carries — so the diff is the union of the
   changes the queue entry is about to land.
2. **Fail closed.** The job's `code` output comes from a `resolve` step,
   not straight from the filter. On `merge_group` it forces `code=true`
   whenever the event carried no `base_ref`, or the filter emitted no
   value. The guard's failure mode is spent CI minutes, never shipped
   untested code.

The docs-only fast path is preserved in the queue: a docs-only entry still
resolves `code=false` and its stub-twins post the required checks in under
a minute.

Full treatment — including why every required check needs a `merge_group:`
trigger, and the `cancel-in-progress` rule — in
[`MERGE_QUEUE.md`](MERGE_QUEUE.md).

## What always runs

Three categories never gate on `code`:

| Job                           | Why                                                      |
| ----------------------------- | -------------------------------------------------------- |
| `pre-commit · all hooks`      | Auto-formats markdown + yaml; we want it on docs PRs too |
| `Analyze (go)` (CodeQL)       | Security scan; never skipped, even on docs PRs           |
| `Analyze (javascript-…)`      | Same                                                     |
| `GitGuardian Security Checks` | Secret scan; runs as a GitHub App, not a workflow file   |

Cost optimization is fine for build/test/lint. Security scans and
the auto-formatter are not on the optimization budget.

### The one job that gates BREADTH, not existence

`tests-integration-main-canary` (slice 631's mechanism (c), push-to-`main`
only, `cancel-in-progress: false`) is a third shape. It ALWAYS runs on a `main`
push — its `if:` carries `always()` so a failed or cancelled `changes` job
cannot skip it — but since slice 703 the SIZE of its leg matrix reads
`changes.code`: one representative leg (A) when the output is exactly
`'false'`, the full leg list otherwise. There is no stub-twin, because the
canary is not a required check and posts no check on a PR.

The fail-safe direction is deliberate: the test is `== 'false'`, not
`!= 'true'`, so a missing output (changes job failed/cancelled) yields the FULL
matrix. Coverage narrows only on an affirmative docs-only signal, never on an
unknown one. The argument that a single leg on a docs-only `main` SHA does not
reopen the slice-474 masking hole is written out in
[`docs/audit-log/703-ci-main-canary-docs-only-single-leg-decisions.md`](../audit-log/703-ci-main-canary-docs-only-single-leg-decisions.md)
— in short: `scripts/**` and `.github/workflows/**` are both inside the `code`
filter, so a docs-only SHA's integration inputs are byte-identical to its
parent's, and induction over the `main` lineage anchors every docs-only SHA to
the last code-touching SHA's full uncancellable matrix run.

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

A code PR touching a shared or unclassified path should be unchanged from
the pre-slice-061 baseline — same jobs, same durations. A web-only PR
should show the Go-tier stubs posting `Go · build + test`,
`Go · integration (Postgres RLS)` and the other Go checks, with no
`Go · integration (shard …)` legs; a Go-only PR the mirror image for the
`Frontend · …` checks.
