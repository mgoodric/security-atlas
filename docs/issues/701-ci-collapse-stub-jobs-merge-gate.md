# 701 — Collapse the ~21 stub jobs behind a promoted merge-gate

**Cluster:** CI / Infra
**Estimate:** M (high-care)
**Type:** JUDGMENT
**Status:** `ready`
**Priority:** P2
**Spillover from:** slice 693 (pipeline-efficiency audit — the ONE finding with real
merge-safety risk).

## Narrative

There are 21 `-stub` jobs, each spinning up a full runner (with harden-runner) just to echo a
skip so that path-filtered required checks still report green. On every docs-only PR that is
~21 runners (~5 runner-minutes) and ~600 lines of ci.yml. The `merge-gate` job (`if: always()`)
already provides the "skipped == success" semantics the stubs were invented for — it accepts
`skipped` legs ONLY when `changes.code != 'true'` and otherwise fails closed (correctly closing
the slice-474 class of hole).

The clean shape: drop the stubs; gate the real jobs with job-level `if:` (already present);
make `CI · merge-gate` the SINGLE required status check. Branch protection then requires only
merge-gate + the unconditional guards (actions-pin-check, cache-path-guard, openapi-drift-check,
etc.).

**This is the one change with genuine merge-safety risk — it must be its own carefully reviewed
slice, never bundled.** Two hard preconditions:

1. `CI · merge-gate` must FIRST be promoted to a required check in
   `.github/branch-protection.json` (today it runs advisory-only).
2. Every name currently in `required_status_checks.contexts` must be represented in
   `merge-gate`'s `needs:`. The audit found merge-gate's `needs:` currently OMITS
   `oscal-bridge`, `fuzz`, `frontend-vitest`, `frontend-lint`, and `govulncheck`. Collapsing
   the stubs before closing those gaps would reopen exactly the slice-474 coverage hole.

## Acceptance criteria

- [ ] **AC-1.** Enumerate every name in `required_status_checks.contexts` and prove each is in
      `merge-gate`'s `needs:` (add the missing ones: oscal-bridge, fuzz, frontend-vitest,
      frontend-lint, govulncheck — and re-verify the full set).
- [ ] **AC-2.** `merge-gate`'s `require()` assertion list stays in sync with its `needs:`
      (consider driving it off `toJSON(needs)` to remove the three-list-sync hazard).
- [ ] **AC-3.** `CI · merge-gate` is promoted to a REQUIRED check in
      `.github/branch-protection.json` and has several green runs first.
- [ ] **AC-4.** The per-job names are removed from `required_status_checks.contexts`, keeping
      merge-gate + the unconditional guards.
- [ ] **AC-5.** All 21 `-stub` jobs are deleted.
- [ ] **AC-6.** Prove fail-closed on a code PR: a forced failure of any real leg fails the
      merge-gate (the skipped-only-ok-when-no-code semantics are preserved).
- [ ] **AC-7.** Prove green on a docs-only PR (real legs skipped, merge-gate passes).

## Anti-criteria

- Does NOT change the merge bar: a code PR with a failing leg must STILL be blocked.
- Does NOT collapse stubs before merge-gate `needs:` covers every required name (AC-1).
- Does NOT bundle with any other slice — branch-protection semantics change in isolation.
- Does NOT remove the unconditional security guards from required checks.

## Dependencies

- Hard-blocks on the merge-gate `needs:` completeness work (AC-1) and the branch-protection
  promotion (AC-3). Sequence: fix needs → promote merge-gate → soak → remove per-job contexts
  → delete stubs.

## Notes

Source: slice 693 audit Finding 5.2 — explicitly flagged as the only recommendation with real
stability risk. Highest payoff (~5 runner-min/docs PR + ~600 LOC) but must be slow and isolated.
</content>
