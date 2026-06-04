# 420 — Fix the flake-budget "flake" definition to capture rerun-passed timing flakes

**Cluster:** Quality
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `ready` (deps merged — slice 352 flake-budget + counter on main)

## Narrative

**WHY.** `docs/flake-budget.md` (lines 42-59) defines a flake narrowly as a failure where a
**re-run of the same `head_sha`** (no code change) later produced `success` for that same
job, AND requires `>=2 distinct run_attempts` for that SHA (`scripts/flake-counter.sh`,
the "group by head_sha; find flakes" pass at ~line 285-298). Slice 352's 90-day baseline
read **0 flakes across all four surfaces over 2364 ci.yml runs** — yet the integration
scheduler test (`TestRun_FiresInlineSweepAndExitsOnCancel`,
`internal/metrics/scheduler/integration_test.go:260`) demonstrably flaked ~6× across recent
sessions, including the incident that **blocked slice 346's docs-only PR #788** and the
recurring blocks the maintainer cleared by rerun. The strict v1 definition **misses the
dominant real flake**: the dashboard reports "0" while merges are in fact blocked weekly by a
timing-sensitive goroutine race. A flake budget that reads 0 against a surface that flakes
weekly is a broken signal — it tells the maintainer "nothing to investigate" while the
investigation trigger never fires.

The gap is mechanical: the counter only counts a flake when GitHub records ≥2 `run_attempt`s
on one `head_sha` with failure-then-success. The scheduler flake is cleared in ways the v1
detector does not see — e.g. a fresh push (new SHA) after a red run, a "re-run failed jobs"
that GitHub sometimes records without the attempt-transition the counter keys on, or a rerun
on a branch the 90-day baseline walk did not classify onto the surface. `flake-budget.md`
itself (lines 56-59) flags the weaker "fail on A, pass on A+1" signal as _deliberately
out-of-scope for v1_ — which is exactly the signal the scheduler flake trips.

**WHAT.** Extend `scripts/flake-counter.sh` detection to ALSO count **rerun-cleared
failures on the integration surface** — i.e. broaden the integration-surface flake signal so
a failure that is cleared by a re-run (including the attempt-transition cases the v1 group-by
misses) is counted, and re-baseline the dashboard so the scheduler flake (and its kin) show
up. The broadening is **scoped to the integration surface** (where the dominant real flake
lives and where the "no retry, investigate every flake" Q-16 policy makes a true-count most
valuable); the unit/vitest/Playwright surface definitions are left as slice 352 set them
(Playwright already has its own `retries: 1` semantics). Update `docs/flake-budget.md`'s
"Definition of flake" section to describe the integration-surface broadening, and update
`scripts/flake-counter_test.sh` to assert the new detection catches a rerun-cleared
integration failure.

**SCOPE DISCIPLINE.** Per slice 352's own anti-criteria, this slice does **NOT relax any
merge-block bar** — every flake still blocks the merge it occurs on; the budget is a _signal_,
not a _gate_. It does NOT add `-retry` to the integration tier (Q-16 unchanged). It does NOT
fix the scheduler flake itself (that is the separate scheduler-flake-fix work this slice
pairs with) — it makes the dashboard _see_ it. It does NOT re-tune the budget _thresholds_
(rates/trigger numbers in the table) — only the detection of what counts.

## Threat model

STRIDE pass. The flake-budget signal is an observability control: a _wrong_ signal is itself
the threat. A definition that under-counts (today's state) hides a real availability problem;
a definition that over-counts (the risk this slice must avoid) cries wolf and erodes trust in
the trigger. Both are real Information-disclosure-of-system-state failures.

**S — Spoofing.** No endpoint/identity. The counter reads the GitHub Actions API with the
existing `GITHUB_TOKEN`. No new auth surface.

**T — Tampering.** The counter does not modify product code, never `t.Skip`s, never
quarantines (slice 352 P0). This slice MUST preserve that — P0-2 forbids the broadened
detection from gaining any auto-mutation power. It still only _counts and reports_.

**R — Repudiation.** Improves the audit trail: the dashboard will now record the integration
flakes it currently silently drops, giving the maintainer a real history. No regression.

**I — Information disclosure (signal integrity — the load-bearing cluster).**

- **I-1 (under-count hides a real flake — the bug being fixed).** Today's 0-reading on a
  weekly-flaking surface is a false "all clear." The broadened integration detection is the
  fix. Mitigation = the slice itself + AC-3 (re-baseline shows the scheduler flake).
- **I-2 (over-count: counting fix-forwards as flakes).** The deliberately-excluded "fail on A,
  pass on A+1" signal (flake-budget.md:56-59) conflates a genuine fix-forward (the A+1 commit
  _fixed_ the test) with a flake (the test is non-deterministic). If the broadening naively
  counts every A→A+1 success as a flake, the dashboard fills with false positives and the
  trigger fires on real fixes. Mitigation: P0-3 — the broadened signal MUST distinguish a
  rerun-cleared failure (same logical test, no relevant code change) from a code-fix-forward;
  AC-4 asserts a fixed-by-A+1 case is NOT counted as a flake. This is the JUDGMENT call and
  the reason the slice is JUDGMENT, not AFK.
- **I-3 (surface mis-classification).** The broadening must stay scoped to the integration
  surface; mis-attributing a lint/sqlc failure to the integration surface pollutes the rate.
  Mitigation: P0-4 — the broadened detection keys on the exact integration job name
  (`Go · integration (Postgres RLS)`), matching slice 352's surface-to-job map.

**D — Denial of service.** The counter is a weekly cron + workflow_dispatch; broadening the
detection adds bounded API walks (the same chunked `workflow_runs` pagination slice 352's D7
already handles). No new unbounded input. The counter still exits non-zero only on tool
failure, never on a surface crossing threshold (slice 352 contract preserved).

**E — Elevation of privilege.** No role boundary. The counter's `flake-investigation`
issue-filing still uses the existing token scope; it does not gain mutation power (P0-2).

**Verdict: has-mitigations.** The core is an observability-integrity fix: I-1 (under-count,
being fixed) vs I-2 (over-count, must be avoided). The fix-forward-vs-flake distinction
(P0-3 / AC-4) is the load-bearing JUDGMENT call.

## Acceptance criteria

- [ ] **AC-1.** `scripts/flake-counter.sh` detection is extended so a rerun-cleared failure on
      the integration surface (`Go · integration (Postgres RLS)`) is counted as a flake,
      including the attempt-transition cases the current group-by-`head_sha` pass misses.
- [ ] **AC-2.** `docs/flake-budget.md`'s "Definition of flake" section is updated to describe
      the integration-surface broadening, with the rationale (the scheduler-flake under-count
      that motivated it) and the explicit note that the broadening is integration-scoped.
- [ ] **AC-3.** A re-baseline run (the 90-day `workflow_dispatch` rebuild) over the same window
      slice 352 measured now SHOWS a non-zero integration flake count attributable to the
      scheduler flake (or, if the window no longer contains it, the decisions log documents the
      window's contents and the synthetic-fixture proof from AC-5 stands in).
- [ ] **AC-4.** `scripts/flake-counter_test.sh` gains an assertion that a code-FIX-FORWARD
      (failure on A, success on A+1 BECAUSE the test was fixed) is NOT counted as a flake — the
      over-count guard (I-2) is mechanically proven.
- [ ] **AC-5.** `scripts/flake-counter_test.sh` gains an assertion that a rerun-cleared
      integration failure (the scheduler-flake shape) IS counted under the new detection — the
      under-count fix (I-1) is mechanically proven.
- [ ] **AC-6.** The unit / vitest / Playwright surface definitions are UNCHANGED — the
      broadening is integration-only; the other three surfaces keep slice 352's v1 definition.
- [ ] **AC-7.** All existing `scripts/flake-counter_test.sh` assertions still pass (the
      broadening is additive, not a rewrite that breaks slice 352's 17 assertions).
- [ ] **AC-8.** The counter remains report-only: no `t.Skip`, no quarantine, no product-code
      mutation; it still exits non-zero only on tool failure, never on threshold-crossing.
- [ ] **AC-9.** Decisions log at `docs/audit-log/420-flake-definition-decisions.md` records the
      flake-vs-fix-forward distinction rule chosen, the re-baseline result, and a revisit list
      (whether to broaden the other three surfaces in a future slice).

## Constitutional invariants honored

- Testing discipline / Q-16 (CLAUDE.md "Integration tier retry policy"): "no retry, investigate
  every flake" is REINFORCED — a true flake count makes "investigate now?" mechanical, which is
  exactly Q-16's intent. No retry is added.
- Flake-budget contract (slice 352 / CLAUDE.md "Flake budget"): the budget remains a signal,
  not a gate; every flake still blocks its merge. The merge-block bar is untouched.
- v1 "diligence the diligence tool": an honest flake dashboard is itself a diligence artifact —
  a 0-reading on a flaking surface would fail that standard.

## Canvas references

- CLAUDE.md "Flake budget" + "Integration tier retry policy (Q-16)".
- `docs/flake-budget.md` (lines 42-59 — the v1 definition + the deliberately-deferred weaker
  signal this slice partially adopts for the integration surface) + lines 119-126 (the
  slice-346/PR-788 scheduler-flake incident that is the canonical missed flake).

## Dependencies

- **#352** — `merged` (flake budget + `scripts/flake-counter.sh` + `_test.sh` + the dashboard
  - the weekly cron — the surface this slice extends).
- **#333** — `merged` (Q-15/Q-16 source audit; the budget's design intent).
- **Scheduler-flake fix** — soft pair. This slice makes the dashboard SEE the scheduler flake;
  the separate fix makes it stop. Neither blocks the other; this slice does NOT fix the flake.

## Anti-criteria (P0 — block merge)

- **P0-1.** Does NOT relax ANY merge-block bar — every flake still blocks the merge it occurs
  on (slice 352 anti-criterion preserved verbatim). The budget stays a signal, not a gate.
- **P0-2 (security — T/E).** The counter stays report-only: no auto-quarantine, no `t.Skip`,
  no `test.skip`, no product-code mutation. The broadening adds counting, never mutation power.
- **P0-3 (security — I-2 — load-bearing JUDGMENT).** The broadened signal MUST distinguish a
  rerun-cleared flake from a code-fix-forward; a genuine A→A+1 fix MUST NOT be counted as a
  flake (proven by AC-4). No naive "any A→A+1 success = flake" rule.
- **P0-4 (security — I-3).** The broadening stays scoped to the EXACT integration job name
  (`Go · integration (Postgres RLS)`); it MUST NOT mis-attribute lint/sqlc/other failures to
  the integration surface.
- **P0-5.** Does NOT add `-retry` to the integration tier (Q-16 unchanged).
- **P0-6.** Does NOT re-tune the budget THRESHOLDS (rates / trigger numbers in the table) —
  only the detection of what counts. Threshold edits are a separate slice (slice 352 versioning
  rule: the budget table is slice-gated).
- **P0-7.** Does NOT fix the scheduler flake itself (separate slice) and does NOT auto-merge.

## Skill mix (3-5)

`observability-designer` · `ci-cd-pipeline-builder` · `tdd` (the `_test.sh` assertions) ·
`grill-with-docs` · `Security` (signal-integrity verification pass).

## Notes for the implementing agent

**Grill output (Phase 2):**

- _Terminology._ "Rerun-cleared failure" = a failure on the integration surface that a later
  re-run cleared without a relevant code change. "Fix-forward" = a failure on commit A that
  commit A+1 _fixed_ via real code. The whole slice hinges on telling these apart for the
  integration surface (P0-3). Slice 352's v1 definition counts only the unambiguous
  same-SHA-attempt-transition subset; this slice broadens to catch the scheduler-flake shape
  without swallowing fix-forwards.
- _Scope._ Integration surface ONLY. Do not touch the unit/vitest/Playwright definitions
  (AC-6) — Playwright already has `retries: 1` semantics and a different flake profile.
- _Already-built check._ `rg -l "flake.def|rerun.passed|flake.budget" docs/issues/` returns
  only slice 352 (the budget) and 333 (the audit) — no slice fixes the definition. This is it.

**Threat-model context (Phase 3).** This is an observability-integrity slice: the bug is a
false 0-reading (I-1, under-count); the trap to avoid is a false-positive flood (I-2,
over-count = counting fix-forwards). The fix-forward-vs-flake distinction is the JUDGMENT.
Pattern-match the distinction off `flake-budget.md`'s own lines 56-59, which already names the
weaker A→A+1 signal as the dangerous one — adopt it for the integration surface ONLY when you
can also tell a fix-forward apart (e.g. by checking whether the A+1 diff touched the failing
test's package, or by keying on the rerun-attempt transition where GitHub records it).

**Implementation note.** Re-read `scripts/flake-counter.sh` Pass A / Pass B (the cheap
per-sha count vs the per-attempt API fetch) before editing — the broadening most likely lives
in Pass B (the per-attempt fetch that already handles mixed-conclusion SHAs) plus a new,
carefully-gated A→A+1 case for the integration job name only. Use the
`internal/metrics/scheduler/integration_test.go` scheduler flake (named in flake-budget.md
lines 119-126) as the canonical fixture shape for AC-5. If the live 90-day window no longer
contains the incident, build a synthetic GitHub-API fixture in `_test.sh` rather than
depending on a moving window (AC-3 fallback).

**Provenance.** Filed 2026-06-03 in the CI-backlog batch (415-420). Directly addresses the
slice-352 90-day "0 flakes" reading that the slice-346/PR-788 scheduler-flake incident
contradicted. Pairs with the scheduler-flake fix (this slice makes the dashboard see it; the
fix makes it stop).
