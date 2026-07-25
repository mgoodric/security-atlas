# 747 — Rebalance the Go integration shards to cut PR wall-clock (time-to-live)

**Cluster:** CI / Infra
**Estimate:** M
**Type:** JUDGMENT
**Priority:** P1 (maintainer-directed: time-to-live is the top CI priority)
**Status:** `ready`

## Narrative

The code-PR critical path is **dominated by the `Go · integration` shards**, not
by anything the 694-703 billing campaign targets. Measured on a representative
code-PR run (2026-06-13):

| Leg | Packages | Wall-clock                                                             |
| --- | -------- | ---------------------------------------------------------------------- |
| A   | 23       | **800s** — carries the serial SCF-catalog shared seed (slice 417 P0-2) |
| B3  | 33       | **784s** — the most packages                                           |
| B2  | 29       | 387s                                                                   |
| B1  | 29       | 333s                                                                   |

Total PR wall-clock ≈ **879s (~14.6m)**; the next-longest non-shard job is
`Go · build + test` at 446s, with everything else (playwright 382s, self-host
~190s, trivy 104s) finishing well before the shards. **The PR cannot merge until
the slowest shard finishes**, so `max(A, B3) ≈ 800s` + the ~79s merge-gate tail
sets time-to-live.

The imbalance is the lever. B3 is package-count-bound (33 pkg, no special seed) —
shedding packages onto the idle B1/B2 (which finish 400s early) directly lowers
it. A is harder: its 800s is likely **SCF-seed-dominated** (the catalog
import/seed that P0-2 pins to Leg A), not package-count-bound (it has the FEWEST
packages yet runs longest). So the achievable floor for A is roughly "SCF seed +
its SCF-dependent tests"; A's NON-SCF-dependent packages can move out to lighter
legs to stop them stacking on top of the seed.

**Goal:** bring the slowest shard from ~800s toward ~500s, cutting PR wall-clock
to ~9-10m (then `build-go` at 446s becomes the next pole — a separate follow-on).

## Approach (JUDGMENT — measure first, then rebalance)

1. **Measure where A's 800s goes** — seed/import vs. package execution. The run's
   step timings (or a local `-tags=integration -p 1` run of Leg A with per-step
   timing) reveal whether A is seed-bound (a fixed floor) or package-bound. This
   determines how much A can drop and whether moving its non-SCF packages out
   helps.
2. **Rebalance `scripts/integration-shards.txt`** so the legs are TIME-balanced
   (not count-balanced — A proves count ≠ time):
   - Shed ~8-12 packages from B3 onto B1/B2 (and/or a new leg) until B-leg times
     converge (~450-550s each).
   - Move A's non-SCF-dependent packages out of A onto the B legs, leaving A as
     {SCF shared-seed cluster + the tests that genuinely need it} per P0-2.
3. **Consider adding a 5th leg (`B4`)** if 4 legs can't get the slowest under
   ~550s: add `B4` to the ci.yml matrix (`leg: [A, B1, B2, B3]` → `+ B4`) and
   give it a share of B3/A's shed packages. Adding a leg is free wall-clock
   (more parallel runners) — the trade-off is more runner-minutes (billing),
   which the maintainer does not value now.
4. **Verify on this PR's own CI run** — the integration shards run on the 747 PR,
   so read the NEW per-leg times from the PR's run and ITERATE the assignment if
   the slowest leg is still materially above target. Record the before/after
   per-leg table in the PR body.

## Acceptance criteria

- [ ] **AC-1.** `scripts/integration-shards.txt` is rebalanced so the slowest
      leg's wall-clock on the PR's own CI run is materially reduced vs. the ~800s
      baseline (target ≤ ~550s; record the achieved number — if A is proven
      seed-bound at a higher floor, document that floor and balance the B legs to
      it rather than below it).
- [ ] **AC-2.** Slice-417 invariants preserved: **P0-1** the UNION of all legs ==
      the integration-enrolled set EXACTLY (`scripts/check-integration-shard-coverage.sh`
      green); **P0-2** the SCF shared-seed cluster (`scf_anchors` + schema-registry
      catalog rows) stays in Leg A; **P0-4** every leg runs `-p 1` internally.
- [ ] **AC-3.** If a 5th leg is added, the ci.yml `matrix.leg` list, the
      shard-coverage check, and any leg-count assumptions
      (`docs/ci/integration-shards.md`, the shard runner script) are updated
      consistently; the new leg is NOT a new required-status-checks context
      (the required context is the aggregate `Go · integration (Postgres RLS)`,
      not the per-leg matrix jobs — CONFIRM this before adding a leg).
- [ ] **AC-4.** All integration shards stay GREEN (no test moved to a leg whose
      DB/seed it cannot satisfy — a package that needs the SCF catalog must stay
      on Leg A; a package that races a parallel leg's shared rows must not move
      to a parallel leg). The `Go · integration (Postgres RLS)` required context
      resolves green.
- [ ] **AC-5.** PR body records the before/after per-leg wall-clock table and the
      resulting critical-path reduction.

## Anti-criteria (P0)

- Does NOT relax `-p 1` within any leg (P0-4) or the no-retry policy.
- Does NOT move an SCF-catalog-dependent test off Leg A (P0-2) — that reintroduces
  the cross-binary `scf_anchors` race slice 417 fixed.
- Does NOT leave any package unassigned or double-assigned (P0-1).
- Does NOT rename the aggregate `Go · integration (Postgres RLS)` required context.

## Dependencies

- **#417** (`merged`) — built the shard mechanism + the invariants this slice
  rebalances within. Read `docs/ci/integration-shards.md` + the
  `scripts/integration-shards.txt` header first.

## Notes

Surfaced 2026-06-13 when the maintainer prioritized time-to-live over billing and
a critical-path measurement showed the 694-703 campaign targets only parallel /
non-critical jobs. The rest of 694-703 are deferred (billing-only) until
development slows. After this slice, `Go · build + test` (446s) is the next
wall-clock pole — a candidate follow-on (test caching / parallelism).
