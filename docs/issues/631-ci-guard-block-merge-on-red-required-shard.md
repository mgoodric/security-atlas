# 631 — CI guard: block merge when a required integration shard is RED (and document the path-filter/concurrency masking)

**Cluster:** Infra (CI / merge-safety)
**Estimate:** S (0.5-1d — CI workflow + branch-protection reasoning, no product code)
**Type:** AFK
**Status:** `ready`
**Parent:** #633 (this is slice 633's AC-6, spun out to keep the P0 integrity
fix focused). Cite: `docs/audit-log/633-ingest-verify-hash-roundtrip-fix-decisions.md` D6.

## Narrative

Slice 474 (`ea3541fb`, PR #1142) merged with its load-bearing required check
`Go · integration (shard A)` (and the roll-up `Go · integration (Postgres
RLS)`) **RED** — a 10m21s real FAIL, not a cancellation. The slice's own proof
test, `TestEvidenceVerify_ProductionRecordValidates_474`, was failing at merge
time. The regression then stayed hidden on `main` for ~3 weeks because every
subsequent main commit either:

1. **Skipped** the shard legs via the `dorny/paths-filter` path filter
   (docs/status-only commits don't touch Go paths), or
2. Had its main run **cancelled** by the next merge's concurrency group before
   the shard leg completed.

So no _completed_ shard-A run on `main` ever re-caught it until slices 615/620
(batch 225) became the first Go PRs to run the filtered path to completion.
This is the `CI path-filter as gap-multiplier` pattern (project memory):
the first PR to exercise a filtered path to completion surfaces the accumulated
breakage.

The merge-safety hole is: **a required check can be RED at merge time and the
merge still completes.** Branch protection's "require status checks to pass"
only enforces checks that _ran_; a path-filtered job that was _skipped_ (and
therefore reports neutral/success-equivalent) does not block, and GitHub's
"require checks" treats a not-run required check inconsistently across the
skipped-vs-pending distinction.

## The fix (to design + decide in this slice)

Make it structurally impossible to merge a Go-affecting PR while its required
integration shard is RED or was never actually run. Candidate mechanisms (pick
one + document the trade-off in the decisions log):

- **(a) Required "merge-gate" aggregator job** that `needs:` every required
  shard and is itself the single required check in branch protection. When a
  shard is skipped by the path filter, the aggregator must distinguish
  "legitimately not needed (no Go changes)" from "needed but skipped" and fail
  closed in the latter case. This is the
  `actions/branch-protection`/`always()`-aggregator pattern.
- **(b) `dorny/paths-filter` correctness fix** so a PR that touches Go paths
  cannot have the shard leg skipped, paired with a branch-protection setting
  that treats a skipped _required_ check as failing.
- **(c) A post-merge canary** on `main` that runs the full shard matrix
  unconditionally (no path filter, no cancel-in-progress for this leg) so a
  regression that slips through is caught on the _next_ main push rather than
  weeks later. Weaker than (a)/(b) — detect-after-merge, not block-before —
  but cheap and orthogonal.

The strongest answer is likely (a) + a narrow piece of (c): block-before-merge
via an aggregator, plus an uncancellable main-branch shard run as
defense-in-depth.

## Scope discipline

- DOES NOT touch product code or the evidence pipeline (that was slice 633).
- DOES NOT relax any existing test gate — it _strengthens_ the merge bar.
- DOES NOT disable the path filter wholesale (that would 3-4× CI cost on
  docs-only PRs); it makes the filter _fail-closed_ for Go-affecting PRs.
- DOES document the path-filter + concurrency-group masking in the decisions
  log as the root-cause class (so the next reviewer understands why a green
  `main` did not imply a green shard).

## Why now

The masking cost just materialized as a P0 merge-queue wedge (slice 633): a
deterministic integration failure sat undetected on `main` for ~3 weeks and
then blocked _every_ Go PR until fixed. The guard's value is exactly the
3-week detection latency it removes.

## Dependencies

- #633 (the integrity fix that unblocked the queue) — merged/landing. This
  slice prevents the _class_ of "merged with a required shard RED" recurring.

## Acceptance criteria

- **AC-1** A PR that touches Go source and whose integration shard FAILS cannot
  be merged (branch-protection blocks it) — demonstrated with a deliberately
  red shard on a throwaway PR or a documented dry-run.
- **AC-2** A docs/status-only PR (no Go changes) still merges without paying
  the full shard matrix (the path-filter optimization is preserved for the
  no-Go case).
- **AC-3** A Go-affecting PR cannot have its required shard _skipped_ and still
  merge (the fail-closed case — this is the exact hole slice 474 fell through).
- **AC-4** The decisions log documents the path-filter-as-gap-multiplier +
  concurrency-cancellation masking as the root-cause class.

## Skill mix

- GitHub Actions (`needs:`, `if:`, `always()`, concurrency groups),
  branch-protection / required-checks semantics, `dorny/paths-filter`
  behavior, CI-cost reasoning.
