# 312 — Coverage audit (round 3) + targeted lift of new gaps

**Cluster:** Quality
**Estimate:** 1-2d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Round-3 follow-on to slice 279's coverage audit. Slice 279 measured
the codebase in May 2026 against a 70% aspirational target; rounds 1

- 2 (slices 281-311) drained the spillover queue, lifting 31 packages
  from <70% merged into the 70-95% range and establishing two reusable
  patterns (CI-integration-list enrollment for HTTP handlers; slice
  305's seam refactor with `fn-var + narrow interface` for cmd/\*
  binaries).

The codebase has moved forward since the slice 279 audit:

- New packages added by slices 209-311 (slice 209's `LocalLogin`,
  slice 142's `super_admins`, slice 187's `tokensign`, etc.) may
  not be in `cmd/scripts/coverage-thresholds.json` yet.
- Existing packages may have grown new uncovered surface as features
  shipped.
- Some packages that floor-69-passed in round 2 may now have drifted
  to 65-68% measured (still inside the 2pp buffer but eroding).

**Disposition:** audit + lift

**Notes:** mirrors the shape of slice 279 — produce a comprehensive
audit doc, pick 3-5 highest-leverage lifts to land in this slice, and
file the long tail as per-package spillover slices (next slot would
be 313+).

## What ships in this slice

1. **New audit doc** `docs/coverage-audit-2026-05-round-3.md` (or
   refresh of `docs/coverage-audit-2026-05.md`) enumerating EVERY Go
   package across the monorepo (`internal/`, `cmd/`, `pkg/`,
   `connectors/`, `sdk/go/` if applicable) with:
   - Current floor in `coverage-thresholds.json` (or `n/a` if
     untracked)
   - Measured unit-only coverage (`go test -cover`)
   - Measured merged coverage (`gocovmerge` of unit + integration
     CI profile if package is in the integration list)
   - Disposition: `at-target` / `unit-add` / `seam-refactor-needed`
     / `exempt-leaning`
   - Spillover slot if filed (for the long tail)
2. **5 highest-leverage lifts** landed directly in this slice's PR
   (same pattern as slice 279's lift-now set):
   - Picked by: largest absolute uncovered LOC × production-criticality
   - Slice 279's "5 lifts in one PR" precedent
   - Floor ratchets monotonically up per slice 069
3. **Per-package spillover slices** for the rest of the long tail
   (each at the appropriate next available slot, all `ready` if deps
   are merged, `not-ready` if blocked).
4. **Refresh of `cmd/scripts/coverage-thresholds.json`** — add any
   new untracked packages with a sensible initial floor (likely 0
   for new code that hasn't been tested yet; engineers must lift
   themselves in subsequent slices).

## Acceptance criteria

- [ ] **AC-1.** Audit doc lists every Go package in the monorepo
      with measured unit + merged coverage.
- [ ] **AC-2.** 5 lift targets are picked using the highest-leverage
      criterion (NOT alphabetical; NOT first-found); rationale
      documented in the audit doc.
- [ ] **AC-3.** All 5 lifts land in THIS PR with tests + floor
      ratchets per slice 069's contract.
- [ ] **AC-4.** Spillover slices are filed for the long tail (one
      per package OR grouped by tier if many small packages share a
      pattern); each spillover slice cites slice 312 as parent.
- [ ] **AC-5.** Any newly-discovered Go package not in
      `coverage-thresholds.json` is added with a sensible floor.
- [ ] **AC-6.** Audit doc identifies any packages whose merged
      coverage has eroded since slice 279 (i.e. floor X-2 now sits
      at <X due to new uncovered code), and includes those in the
      lift-or-spillover decision.
- [ ] **AC-7.** Decisions log at `docs/audit-log/312-coverage-audit-round-3-decisions.md`
      captures D-level decisions (which 5 to lift, which packages
      are now `exempt-leaning`, any methodology changes).
- [ ] **AC-8.** Honest report: if no packages need lifts, the audit
      doc says so explicitly and slice closes as `merged` with just
      the audit doc + decisions log + thresholds.json refresh (no
      forced lifts).

## Constitutional invariants honored

- Testing discipline (CLAUDE.md): floor + tests in same PR;
  monotonic ratchet.
- Slice 069 methodology: `max(0, floor(measured - 2pp))` for floor
  setting.
- Slice 279 precedent: audit + lift + spillover-as-slice for the
  long tail.
- Slice 305 pattern: cmd/\* packages that need a seam refactor get
  their own slice; don't inline.

## Dependencies

- **#279** (round 1 audit) — `merged`.
- **#305** (seam refactor pattern) — `merged`.
- **#306-311** (round 2 drain) — all `merged`.

## Anti-criteria (P0 — block merge)

- **P0-312-1.** Does NOT lower any existing floor in
  `coverage-thresholds.json`. Ratchet is monotonic; if measured
  drops below current floor, that's a bug — fix the missing tests
  or file a spillover, do NOT lower the floor.
- **P0-312-2.** Does NOT lift packages with measured already ≥ 70%
  merged ("vanity ratchet"). The 5 lift targets must have a real
  gap to close. Slice 279's hard rule still applies.
- **P0-312-3.** Does NOT inline seam refactors for cmd/_ packages.
  If a cmd/_ package needs slice 305's pattern, file a spillover
  slice with that scope; do not mix audit + multi-package seam
  refactors in one PR.
- **P0-312-4.** Does NOT modify `_INDEX.md` — orchestrator's
  surface.
- **P0-312-5.** Does NOT file >10 spillover slices from a single
  audit pass. If the long tail exceeds 10, group by tier or by
  shared pattern (e.g. "all connector cmd packages" → one
  spillover slice instead of N).
- **P0-312-6.** Audit doc MUST distinguish between unit-only
  coverage and merged coverage (the integration `-coverpkg=./...`
  matters; reporting just unit-only would re-create slice 279's
  initial blindspot).

## Skill mix

- Slice 279 (`docs/issues/279-coverage-audit-and-targeted-lift.md`)
  as the primary exemplar
- `docs/coverage-audit-2026-05.md` to refresh / supersede
- Slice 305 + 308 + 309 (seam refactor pattern) for cmd/\* lift
  candidates
- Slices 291 / 290 / 293 / 297 / 310 (CI-integration-list enrollment
  pattern) for HTTP handler lift candidates

## Notes for the implementing agent

**Measurement methodology** (mirror slice 279 exactly):

```bash
# Unit profile
go test -coverpkg=./... -coverprofile=unit.cov ./...

# Integration profile (against running Postgres + the package list
# in .github/workflows/ci.yml's tests-integration job)
go test -tags=integration -p 1 -coverpkg=./... -coverprofile=integration.cov \
  $(awk '/tests-integration:/,/Upload integration/' .github/workflows/ci.yml \
   | grep -oE './internal/[^ ]+|./connectors/[^ ]+|./pkg/[^ ]+' | sort -u)

# Merged via gocovmerge
gocovmerge unit.cov integration.cov > merged.cov

# Per-package report
go tool cover -func=merged.cov | awk '{print $1, $NF}' | sed 's|/[^/]*\.go:[0-9]*:||' \
  | sort -u | column -t
```

The audit doc should:

1. Enumerate every Go package via `go list ./...`
2. Cross-reference with `coverage-thresholds.json` floors
3. Measure merged % per package
4. Apply slice 279's tier-classification table:
   - `at-target` (merged ≥ 70%): no work; floor may ratchet up
     by ≤2pp if measured has grown
   - `unit-add` (merged < 70%, no integration test exists in CI):
     write unit tests
   - `seam-refactor-needed` (cmd/\* or other untestable-as-is):
     file a slice-305-style spillover
   - `exempt-leaning` (per slice 279's policy: cmd/atlas* + tiny
     `cmd\_*.go` glue files; documented as such)

**Pick the 5 lifts** by:

- Largest production-critical uncovered surface (auth + RLS +
  evidence + audit-log are highest-criticality)
- Highest measured-floor delta (packages where floor << measured
  due to past lift wins; ratchet floors up to capture progress)
- Smallest seam-refactor-or-CI-enrollment cost (slice 290 / 291 /
  297 / 310 enrollment is "near-zero" effort)

**Spillover slices**:

- Each gets a doc at `docs/issues/<NNN>-coverage-<pkg>.md`
- Each cites parent slice 312
- Each goes into the canonical Status table as `ready` (deps already
  merged for round-3 work)
- Group similar small packages if >10 long-tail items emerge

**Honest closure**:

- If round-3 finds the codebase is already healthy (most packages
  ≥70% merged, floors only need small ratchets), the slice closes
  with just the audit doc + decisions log + thresholds.json refresh.
  No forced lifts.
