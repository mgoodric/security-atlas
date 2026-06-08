# 631 — CI guard: block merge on a RED / skipped-but-needed required integration shard — decisions log

- detection_tier_actual: production
- detection_tier_target: ci_merge_gate

The slice-474 regression was a deterministic integration FAILURE that
surfaced at the **production** tier — the first Go PR (slices 615/620, batch 225) to run `Go · integration (shard A)` to completion since 474 merged
re-caught the accumulated breakage, ~3 weeks late (the `CI path-filter as
gap-multiplier` pattern). It SHOULD have been caught at the merge-gate tier:
the merge of slice 474 should have been structurally impossible while its
required shard was RED. This slice adds that tier (`CI · merge-gate`), so the
regression class — "merged with a required shard not-green" — is now blocked
before merge rather than detected after. The companion unit-tier fix (the
host-clock-independent round-trip guard) landed in the parent slice 633; this
slice closes the _process_ hole that let a RED shard merge at all. `target`
above is the gate this slice introduces (no existing tier value matched the
new merge-gate surface).

## The masking root-cause class (AC-4 — why a green `main` did not imply a green shard)

The merge-safety hole has TWO compounding masking mechanisms, both rooted in
the interaction between (i) `dorny/paths-filter`, (ii) the workflow-level
`concurrency` group with `cancel-in-progress: true`, and (iii) GitHub branch
protection's inconsistent treatment of a required check that ended
`skipped`/`cancelled`/`neutral` (as opposed to one that ran and reported RED).

1. **Path-filter SKIP.** `.github/workflows/ci.yml`'s `changes` job sets a
   `code` boolean via `dorny/paths-filter`. The expensive jobs (incl. the
   `tests-integration-shard` matrix and the `tests-integration` roll-up) are
   gated `if: needs.changes.outputs.code == 'true'`, with a mutually-exclusive
   slice-061 **stub-twin** under the IDENTICAL required-check name that posts
   green when `code != 'true'`. This is a deliberate, correct cost
   optimization: a docs/status-only PR resolves the required check in <30s
   instead of paying the ~10-min matrix. The side effect: on a docs/status-only
   commit to `main`, the real shard legs are SKIPPED — so no _completed_ shard
   run exists for that commit. After slice 474 merged RED, the run of
   docs/status-only `main` commits meant the shard legs never re-ran to
   completion on `main`.

2. **Concurrency CANCEL.** The workflow-level
   `concurrency: { group: ci-${{ github.ref }}, cancel-in-progress: true }`
   means a new push to `main` CANCELS the in-flight run of the previous `main`
   push (same `github.ref`). In a busy merge window, a `main` run could be
   cancelled before its ~10-min shard leg completed — leaving the leg
   `cancelled`, never RED-and-completed.

Net: between (1) and (2), no _completed_ shard-A run on `main` ever re-caught
the slice-474 failure. The reviewer who saw a "green `main`" was seeing either
a docs-only stub-green or a cancelled-before-completion run — neither is
evidence the shard PASSED. A green `main` did NOT imply a green shard.

The branch-protection layer compounded it: the required-contexts list carries
the roll-up name `Go · integration (Postgres RLS)` but NOT the per-leg
`Go · integration (shard ...)` names. A required check that is `skipped`
(stub-green) or `cancelled` is treated by branch protection differently from
one that is `pending` or RED — and a skipped/cancelled required check did not
reliably block the merge button. That is the structural hole slice 474 fell
through.

## Decisions made

### D1 — Mechanism (a) aggregator as the primary block-before-merge gate; plus a narrow piece of (c)

**This is the load-bearing JUDGMENT call.** The spec offered three mechanisms:

- **(a)** a required `merge-gate` aggregator that `needs:` every leg, runs
  `if: always()`, and fails closed when a leg was needed-but-not-green;
- **(b)** a paths-filter correctness fix + treat a skipped required check as
  failing;
- **(c)** a post-merge `main` canary that re-runs the full matrix
  unconditionally and uncancellably.

**Chosen: (a) as primary + a narrow (c) as defense-in-depth.** Rationale:

- **(b) cannot be done from inside a workflow.** "Treat a skipped required
  check as failing" is a _branch-protection_ behavior GitHub does not expose as
  a per-check toggle; and a paths-filter "correctness fix" that forced the
  shard leg to run on every Go PR is already the _status quo_ (Go PRs DO run
  the legs) — the hole was never "the leg didn't run on a Go PR", it was "a
  skipped/cancelled required check didn't block". (b) addresses the wrong link
  in the chain.

- **(a) collapses the ambiguity into one deterministic signal.** The
  aggregator is a single job whose `result` is `success` ⇔ every required leg
  succeeded (on a Go PR) OR the PR is legitimately no-Go. Branch protection can
  require exactly ONE name (`CI · merge-gate`) and that name is RED whenever a
  leg failed/was-cancelled/was-skipped-but-needed. This turns the
  skipped-vs-cancelled-vs-failed ambiguity that bit slice 474 into a binary
  `success`-or-RED. It BLOCKS before merge — the strongest posture.

- **(c) is the orthogonal after-merge net for masking mechanism (2).** Even
  with (a) gating PRs, a `main` run can still be concurrency-cancelled. The
  canary re-runs the full matrix on `main` UNCONDITIONALLY (no path filter) and
  UNCANCELLABLY (own concurrency group, `cancel-in-progress: false`), so a
  regression that somehow slips the gate — or a docs-only `main` push that
  would otherwise skip the shards — gets a completed shard run on the next
  `main` push rather than weeks later. It is NOT a required check (it only
  exists on `main` pushes, never on PRs) and never sits on the PR critical
  path. **Confidence: high.**

### D2 — The fail-closed logic (exact semantics)

The `merge-gate` job:

- `needs:` `[changes, build-go, tests-integration-shard, tests-integration,
lint-go, sqlc-drift, proto, build-frontend, frontend-playwright,
lint-python]` — every required, path-filterable leg, plus the path-filter
  `changes` job.
- `if: always()` — runs EVEN when an upstream leg failed/was-cancelled/was-
  skipped. Without `always()`, a failed `needs:` would SKIP the aggregator, and
  a skipped required check is the exact hole being closed.
- Reads `needs.changes.outputs.code` to branch:
  - `code != 'true'` (docs/status-only): the real legs are legitimately
    `skipped` (stub-twins post the green checks) → PASS (AC-2; preserves the
    slice-061 cost optimization; this is the ONLY branch where a `skipped` leg
    is acceptable).
  - `code == 'true'` (Go-affecting PR): EVERY leg must be exactly `success`.
    Any `failure` / `cancelled` / `skipped` fails the gate CLOSED (AC-1, AC-3).
    `skipped`-while-Go-changes-present is the precise slice-474 hole.
- For the **matrix** `tests-integration-shard`, `needs.<job>.result` is the
  AGGREGATE across legs A/B1/B2/B3: `success` only if all legs succeeded;
  `failure` if any leg failed; `cancelled` if any was cancelled. So one RED
  shard ⇒ aggregate `failure` ⇒ gate RED. This is the per-shard signal that
  was absent at the branch-protection layer.

**Why this cannot wedge the merge queue (the critical caution).** A normal
GREEN Go PR has every leg `success`, so the gate is GREEN. A docs-only PR has
`code == 'false'`, so the gate short-circuits GREEN. The ONLY way the gate is
RED is a real failed/cancelled/skipped-but-needed leg — which is exactly when
the merge SHOULD be blocked. The aggregator adds NO new failure mode for a
clean PR; it cannot make the required gate impossible to satisfy for a normal
green PR. The shell logic is a plain `for`-equivalent of explicit `require`
calls with `set -euo pipefail` and a single `exit 1` on any non-`success` leg
under `code == 'true'`. **Confidence: high.**

### D3 — Additive only; no existing required job renamed or removed

Branch protection references required jobs BY NAME (`Go · integration
(Postgres RLS)`, `Go · build + test`, etc.). Renaming or restructuring any of
them would silently drop the corresponding required-context (the name would no
longer be reported), re-opening a hole. So this slice ONLY ADDS two jobs
(`merge-gate`, `tests-integration-main-canary`) and touches no existing job.
The existing `tests-integration` fan-in already re-asserts
`needs.tests-integration-shard.result != 'success'` ⇒ RED (slice 417 AC-11);
the merge-gate is a SECOND, branch-protection-required line of defense that
also covers the skipped/cancelled cases the fan-in's own
`code == 'true'`-gating could itself be skipped on. **Confidence: high.**

### D4 — The canary uses its own per-leg `concurrency` group, `cancel-in-progress: false`

The workflow-level `concurrency: { group: ci-${{ github.ref }},
cancel-in-progress: true }` is what cancelled in-flight `main` runs (masking
mechanism 2). A job-level `concurrency` block OVERRIDES the workflow-level
group for that job, so the canary opts out of the cancel-in-progress behavior.
Keyed `ci-main-canary-${{ github.sha }}-${{ matrix.leg }}` so (i) it is per-
commit (a later `main` push starts its own canary, does not cancel the prior
commit's) and (ii) per-leg (the four legs of one push do not cancel each
other). **Confidence: high.**

### D5 — Reuse `scripts/run-integration-shard.sh` for the canary; no second package list

The canary mirrors the PR-time `tests-integration-shard` bring-up (Postgres
service container + MinIO + NATS + cosign + roles + forward migrations) and
invokes the SAME `scripts/run-integration-shard.sh` against the SAME slice-417
`scripts/integration-shards.txt` manifest. There is therefore no second
package-to-leg list to drift; the slice-417 shard manifest stays canonical.
The canary deliberately OMITS the Leg-A-only extras (watermark, migration
round-trip, order-independence guard, shard-coverage guard) — those are PR-time
/ clean-main guards owned by slice 417/461/393, not the canary's job; the
canary's sole purpose is "did the shard's tests pass to completion on `main`".
**Confidence: high.**

## Branch-protection hand-off (REQUIRED — cannot be done from a PR)

A PR cannot change live branch protection. For the `merge-gate` to actually
BLOCK (rather than merely run + report advisory), the maintainer must:

1. Add the context name `CI · merge-gate` to
   `.github/branch-protection.json`'s `required_status_checks.contexts`.
2. Apply it to live `main` branch protection:
   `bash scripts/apply-branch-protection.sh`.

Until step 2 runs, `merge-gate` runs on every PR and reports green/red but is
NOT merge-blocking. This slice intentionally does NOT edit
`.github/branch-protection.json` in a way that would require a soak — the
existing required jobs are unchanged; adding `CI · merge-gate` is the
maintainer's one-line promotion after observing a few green runs (mirrors the
slice 116 / 128 / 140 / 159 promotion convention recorded in that file).

Optionally (recommended once the aggregator has soaked): the maintainer may
then NARROW the required-contexts list to `CI · merge-gate` + the unconditional
guards (`pre-commit · all hooks`, `actions-pin-check`, `openapi-drift-check`,
`Analyze (go)`, `Analyze (javascript-typescript)`, `GitGuardian Security
Checks`), since the aggregator subsumes the per-leg path-filterable checks.
That narrowing is a SEPARATE maintainer decision (it changes which names show
on the PR checks UI) and is explicitly OUT of scope for this slice — left as a
note, not done here.

## What shipped

- `.github/workflows/ci.yml` — two NEW jobs appended (no existing job touched):
  - `merge-gate` (`name: CI · merge-gate`): the fail-closed aggregator
    (mechanism a). `if: always()`, `needs:` the nine required path-filterable
    legs + `changes`; a single shell step that PASSES on no-Go PRs and on
    all-green Go PRs, and fails CLOSED on any failed/cancelled/skipped-but-
    needed leg.
  - `tests-integration-main-canary` (`name: Go · integration canary (main,
shard <leg>)`): the uncancellable, unconditional `main`-only full-matrix
    canary (mechanism c). Own `concurrency` group with
    `cancel-in-progress: false`. Reuses `scripts/run-integration-shard.sh`.
- `CHANGELOG.md` — `[Unreleased]/Changed` `ci:` bullet.

## Verification

- `pre-commit run --files .github/workflows/ci.yml` — all hooks PASS, incl.
  `actionlint (slice 158)` (which runs actionlint with its `-shellcheck`
  integration over the embedded `run:` scripts) and `check yaml`.
- `bash scripts/check-action-pins.sh` — 169 actions pinned across 8 files, no
  tag-pinned action (every `uses:` in the new jobs reuses an already-pinned
  SHA: harden-runner, checkout, setup-go, cosign-installer; no new action
  reference introduced — slice 128 `actions-pin-check` invariant unregressed).
- Reasoned through the `needs` / `always()` / `result` semantics (D2): a clean
  green Go PR ⇒ every leg `success` ⇒ gate GREEN; a docs-only PR ⇒
  `code == 'false'` ⇒ gate short-circuits GREEN; a RED/cancelled/skipped-but-
  needed leg ⇒ gate RED. No formulation makes the gate impossible to satisfy
  for a normal green PR (the critical caution).

## Residual risk

- The gate is ADVISORY until the maintainer adds `CI · merge-gate` to live
  branch protection (the hand-off above). This is the inherent limit of a
  PR-only change; surfaced explicitly so it is not forgotten.
- A maintainer-bypass merge (admin override) still bypasses the gate, as it
  bypasses all required checks — out of scope (this slice strengthens the
  default path, not the explicit-override path).
