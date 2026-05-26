# 309 — Coverage lift (round 2) — `connectors/okta/cmd/atlas-okta` doRun to 70%+

**Cluster:** Quality
**Estimate:** 1-2d (requires seam refactor per slice 305 pattern)
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 302 (`connectors/okta/cmd/atlas-okta` 20.7% →
64.9%, 5pp short of 70%). The remaining gap sits in `doRun`'s
post-`oktaauth.Resolve` body — the Pull + push loop calls behind
`oktapolicy` / `oktaapps` / `oktausers` clients that can't be
unit-covered without a seam refactor.

Slice 305 established the seam refactor pattern for the AWS connector.
Slice 308 applies it to github cmd. This slice applies it to okta cmd
with the same shape.

**Disposition:** seam refactor + unit-add

## What ships in this slice

1. **Minimal seam refactor** in
   `connectors/okta/cmd/atlas-okta/cmd_run.go`:
   - Package-level fn-vars for the okta SDK call sites
     (`oktapolicy.List`, `oktaapps.List`, `oktausers.List` —
     check actual import shape).
   - Narrow interface(s) for the SDK clients the loop calls into
     (analogue of slice 305's `sdkPushClient`).
2. **New `cmd_run_seam_test.go`** driving doRun against fakes.
3. **Floor ratchet** in `cmd/scripts/coverage-thresholds.json` from
   62 to `floor(measured - 2pp)` (monotonically ↑).

## Acceptance criteria

- [ ] **AC-1.** Merged coverage of `connectors/okta/cmd/atlas-okta`
      lifts to ≥ 70%.
- [ ] **AC-2.** Tests exercise real doRun branches (policy list /
      apps list / users list happy + error wraps).
- [ ] **AC-3.** Seam refactor is minimal: only the fn-vars + interfaces
      needed to drive the tests.
- [ ] **AC-4.** Floor ratchets to `floor(measured - 2pp)`.

## Constitutional invariants honored

- Testing discipline (CLAUDE.md): floor + tests in same PR.
- Slice 069 ratchet methodology.
- Slice 305 seam pattern.

## Dependencies

- **#302** (okta cmd round 1) — merged at `f1918618`.
- **#305** (aws-connector seam pattern) — merged at `b9868ede`.

## Anti-criteria (P0 — block merge)

- **P0-309-1.** Does NOT broaden the seam refactor beyond test needs.
- **P0-309-2.** Does NOT lower any existing floor.
- **P0-309-3.** Does NOT modify `_STATUS.md` from inside the slice.
- **P0-309-4.** Does NOT use vendor-prefixed tokens (`okta_*` if Okta
  uses one); use neutral `test-*` strings.
- **P0-309-5.** Does NOT break slice 045's Okta connector integration
  test.

## Skill mix

- Slice 305 seam pattern (mandatory reading)
- Slice 302 test file (mandatory reading)
- Slice 308 (github cmd round 2) — sibling slice in same batch; if it
  merges first, copy its exact pattern

## Notes for the implementing agent

This is the third application of slice 305's pattern. By now the
pattern should be near-mechanical. If slice 308 (github) lands first,
read its diff as a same-batch sibling exemplar.
