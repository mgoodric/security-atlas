# 703 — Main-canary: run a single representative leg on docs-only pushes

**Cluster:** CI / Infra
**Estimate:** S
**Type:** JUDGMENT
**Status:** `ready`
**Priority:** P3
**Spillover from:** slice 693 (pipeline-efficiency audit — merge-gate/canary investigation).

## Narrative

`tests-integration-main-canary` (push-to-`main` only, uncancellable per the slice-474 fix)
re-runs the FULL 4-leg integration shard matrix with `-race`, UNCONDITIONALLY — including on
docs-only / `chore(status)` merges, which this project produces frequently. Its purpose is
defense-in-depth: guarantee a COMPLETED shard run exists on every `main` SHA regardless of what
the last merge touched (closing the second slice-474 masking mechanism). That intent is sound;
the cost is a full `-race` integration matrix per docs merge.

Middle ground that preserves the guarantee at ~1/4 the cost: on docs-only `main` pushes (where
`changes.code != 'true'`), run a SINGLE representative leg instead of all four. That still
produces "a completed shard run on this main SHA" while not re-verifying four parallel
service-stack bring-ups for code that did not change. Code-touching `main` pushes keep the full
4-leg matrix.

This is a JUDGMENT call: the canary author deliberately chose unconditional full execution. Do
NOT silently weaken the guarantee — either implement the single-leg-on-docs variant with the
masking-window analysis written out, or WONTFIX it. The full matrix on code pushes is
non-negotiable.

## Acceptance criteria

- [ ] **AC-1.** On `main` pushes where `changes.code != 'true'`, the canary runs ONE
      representative shard leg (not all four).
- [ ] **AC-2.** On `main` pushes where `changes.code == 'true'`, the canary runs the FULL 4-leg
      `-race` matrix (unchanged).
- [ ] **AC-3.** The canary remains uncancellable (`cancel-in-progress: false`, slice-474) and
      push-to-main only.
- [ ] **AC-4.** The PR body writes out the masking-window analysis showing the single-leg
      docs-only path does not reopen the slice-474 hole.

## Anti-criteria

- Does NOT reduce coverage on code-touching `main` pushes.
- Does NOT make the canary cancellable or move it onto the PR path.
- Does NOT proceed without the masking-window analysis (AC-4); WONTFIX is acceptable.

## Dependencies

- Independent. Shares the `scripts/run-integration-shard.sh` single-source-of-truth with the
  PR-time shard.

## Notes

Source: slice 693 audit Finding 8A. The conservative alternative to a path filter — preserve
"a completed shard ran on this SHA" at lower cost rather than skipping entirely.
</content>
