# 279 — Coverage audit + targeted lift of 5 highest-leverage packages

**Cluster:** Quality
**Estimate:** 2-3d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

`cmd/scripts/coverage-thresholds.json` currently floors 76 Go packages.
**43 of those (57%) are below 70%**; several sit at literal 0%
(`cmd/atlas`, `cmd/atlas-cli`, `cmd/atlas-openapi`, `internal/api/metrics`,
`internal/catalog/metrics`, `internal/metrics/eval`,
`internal/metrics/scheduler`); others sit at 2-3%
(`internal/decision` at 2, `internal/artifact` at 3, `internal/audit/walkthrough` at 3).

The maintainer's observation: coverage has dropped substantially. The
target: 70% as an aspirational guide (not a hard floor — some packages
legitimately don't warrant it). The challenge: lifting 43 packages in
one slice is multi-week and brittle. The right shape is a foundational
slice that **audits + tiers + lifts the top 5** + files per-package
spillovers for the long tail.

A second load-bearing observation: many low-unit-coverage packages
have rich INTEGRATION coverage that the current `go test -cover` run
doesn't count. Re-measuring with `-coverpkg=./...` under the
integration build tag will likely show several "low" packages at
80%+ without writing a single new test. The audit must distinguish
"genuinely undertested" from "tested via integration; measurement
gap".

### What ships in this slice

**1. Coverage audit (`docs/coverage-audit-2026-05.md`)**

Walks every package below 70%. Each row records:

- Package path
- Current unit-only coverage
- Measured `go test -tags=integration -coverpkg=./...` coverage
- Disposition: `unit-add` / `count-integration` / `exempt`
- Notes (e.g., "cmd/main wrapper, no business logic — exempt at floor 5%")

The audit is the load-bearing artifact: it tells future contributors
which packages need NEW tests vs which need a MEASUREMENT change.

**2. Integration-aware coverage measurement (CI change)**

Extend `cmd/scripts/coverage-gate` and the `Go · build + test` CI
job to ALSO run `go test -tags=integration -coverpkg=./... -coverprofile=integration.cov`
and merge the profiles (`gocovmerge` or equivalent). The threshold
check reads the merged profile. This single change is expected to
move 10-15 packages from `below 70%` to `above 70%` without any test
additions.

**3. Five targeted package lifts**

After the integration-aware re-measurement, the audit identifies the
5 highest-leverage packages still below 70%. The engineer writes the
missing unit tests AND ratchets the floors in the SAME PR per the
existing `cmd/scripts/coverage-thresholds.json` discipline (no
threshold lifts without test additions; no test additions without
threshold lifts — the ratchet contract).

Provisional 5 (re-confirmed at audit time):

- `internal/decision` (2% — core business logic)
- `internal/risk` (10% — core domain)
- `internal/board` (20% — substantive surface)
- `internal/frameworkscope` (19% — RLS-relevant)
- `internal/eval` (14% — load-bearing)

**4. Per-package spillover slices for the long tail**

For each remaining below-70% package the audit flags as `unit-add`,
file a small spillover slice (`docs/issues/<NNN>-coverage-<pkg-slug>.md`)
with one AC: "lift `<pkg>` to 70%+ with new unit tests; raise floor in
same PR". These become loop-pickable batch fodder.

**5. Tiered-floor documentation (NOT enforcement)**

Add a `$tier_recommendations` block to `coverage-thresholds.json`
describing the per-role-tier targets the maintainer agreed to use as
GUIDANCE (not enforcement):

- API handlers, business logic, RLS-touching code: 70%+ target
- Connector packages: 70%+ target (slice 069 set most at 80%+
  already)
- CLI cmd packages (`cmd/atlas`, `cmd/atlas-*`): floor at measured;
  no 70% pressure (cobra glue + main entry points)
- Generated code (sqlc, protoc): exempt entirely (already in
  `excludes`)

The actual `thresholds` map continues to enforce per-package floors
ratcheted at `floor(measured - 2pp)` per slice 069's methodology.
The tier block is a maintainer-readable convention only.

### Scope discipline (deliberately OUT)

- **Frontend (vitest) coverage gate.** Filed as spillover slice
  (next available slot) per user-confirmed scope split. Setting up
  a vitest coverage gate is a foundational task in its own right
  (CLAUDE.md: "Frontend vitest ... No coverage gate yet; CI uploads
  coverage-summary.json as artifact to inform follow-up").
- **Lifting all 43 below-70 packages in one slice.** Strongly
  discouraged per /idea-to-slice scope-creep heuristic; mega-slice
  one engineer can't realistically own. Per-package spillovers from
  the audit are the path.
- **Raising hard floor across the board.** Per-package judgment per
  user's "70% is aspirational" framing.
- **Mocking out integration tests for unit speed.** The integration
  build tag already isolates the slow tests; this slice does NOT
  fork integration into per-package mock shadows.

## Threat model

| STRIDE                       | Threat                                                                               | Mitigation                                                                                                                                          |
| ---------------------------- | ------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | No new auth surface. Pure CI / test additions.                                       | n/a                                                                                                                                                 |
| **T** Tampering              | New tests could be written to pass without exercising the code (vacuous assertions). | Code review surface; every test must include at least one `expect`/`require` per branch covered. AC-7 + AC-8 codify.                                |
| **R** Repudiation            | No audit-log surface.                                                                | n/a                                                                                                                                                 |
| **I** Information disclosure | Coverage profiles + test fixtures could leak secrets or sensitive data.              | Test fixtures use neutral `test-*` strings per the existing slice 05 convention; no real tenant data; no real credentials.                          |
| **D** DoS                    | The merged-profile coverage run adds ~2-5 min to CI.                                 | Acceptable per slice 069's CI-budget; tracked in the decisions log. If unacceptable, the integration profile can run in parallel as a separate job. |
| **E** EoP                    | No new authz surface.                                                                | n/a                                                                                                                                                 |

**Verdict.** **no-mitigations-needed**. Pure CI / test additions
with zero new threat surface beyond what existing tests already
ship. STRIDE recorded for audit-trail discipline.

## Acceptance criteria

### Audit doc

- [ ] **AC-1.** `docs/coverage-audit-2026-05.md` ships with a per-
      package table covering every package currently below 70% in
      `coverage-thresholds.json`. Columns: package, unit-only %,
      integration-merged %, disposition (`unit-add` /
      `count-integration` / `exempt`), notes.
- [ ] **AC-2.** Audit doc identifies the 5 highest-leverage
      `unit-add` packages (post-integration-merge measurement) and
      flags them as this slice's lift targets.
- [ ] **AC-3.** Audit doc identifies the `count-integration`
      packages (those whose integration-merged % is already ≥ 70%
      without unit additions) and flags them for floor-only lifts
      in this slice.

### CI / measurement

- [ ] **AC-4.** `cmd/scripts/coverage-gate` (or the `Go · build +
test` workflow) is extended to run BOTH unit and integration
      coverage AND merge the profiles before threshold check. Use
      `gocovmerge` or equivalent.
- [ ] **AC-5.** CI run on this PR shows the merged-profile coverage
      is the new threshold check. The PR description records the
      before/after merged % for the 5 lift targets + the
      `count-integration` packages.

### Five targeted lifts (post-audit)

- [ ] **AC-6.** Each of the 5 lift targets (audit-confirmed) gets
      new unit tests that move its merged coverage to ≥ 70%.
- [ ] **AC-7.** Each new test exercises real branches with real
      assertions. No vacuous `expect(true).toBe(true)` patterns.
      Reviewer's eye check; reviewer can reject AC-7 on judgement.
- [ ] **AC-8.** Each test file's first comment block names the
      package's load-bearing functions + the branches the file is
      designed to cover. Future contributors can read this to
      understand what's pinned vs what was deliberately left.
- [ ] **AC-9.** `coverage-thresholds.json` ratchets the 5 lift
      targets to their new merged % minus 2pp (per slice 069 P0-A4
      methodology). NO threshold raised without new tests.

### `count-integration` floor lifts

- [ ] **AC-10.** Packages flagged `count-integration` in the audit
      get their floors raised to merged-actual minus 2pp. No new
      tests written (integration already covers them). The audit
      doc + this AC together document why the floor jumped.

### Tiered-floor documentation

- [ ] **AC-11.** `cmd/scripts/coverage-thresholds.json` ships a new
      top-level `$tier_recommendations` field documenting per-role-
      tier aspirational targets (API handlers 70%+, connectors 70%+,
      CLI cmd 30%+, generated code exempt). Documented as
      GUIDANCE — the `thresholds` map still enforces per-package
      floors.

### Spillover slices for long tail

- [ ] **AC-12.** Per-package spillover slices (one per
      `unit-add`-flagged package NOT covered by the 5 lift targets)
      filed at the next-available slots. Each carries a single
      narrative AC: "lift `<pkg>` to 70%+ with new unit tests;
      raise floor in same PR". Status `ready` if deps merged.

### Polish

- [ ] **AC-13.** CHANGELOG bullet under `### Changed` documenting
      the CI measurement change + the 5 lifted floors + the audit
      doc.
- [ ] **AC-14.** Decisions log at
      `docs/audit-log/279-coverage-audit-decisions.md` captures:
      D1 (5-package selection rationale), D2 (integration-merge tool
      choice — `gocovmerge` vs alternatives), D3 (any per-package
      disposition that surprised the engineer), D4 (CI runtime
      impact of the merged profile run).
- [ ] **AC-15.** `_STATUS.md` table row for slice 279 flips to
      `in-progress` at claim-stake and to `merged` at reconcile via
      the orchestrator's normal path (NOT touched by this slice's
      own commits — P0-279-3).

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md "Testing discipline" section).**
  This slice respects the ratchet — no floor raised without test
  added; no test added without floor raised. It also explicitly
  honors the integration-vs-unit split documented there.
- **Slice 069 methodology.** Floors ratchet at `max(0, floor(measured
  - 2pp))`. This slice does NOT lift floors above measured.
- **AI-assist boundary.** No LLM in the loop. Pure test engineering.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — Go test discipline + `just`
  runner.

## Dependencies

- **#069** (per-package coverage gate + thresholds) — `merged`.
  Defines the ratchet methodology + the threshold file shape this
  slice extends.
- All packages targeted for lift must already exist on main; per the
  audit's package list (which is generated against current main).

## Anti-criteria (P0 — block merge)

- **P0-279-1.** Does NOT raise any package floor without writing
  the unit tests that hit the new bar (per slice 069's ratchet
  contract).
- **P0-279-2.** Does NOT write tests that don't lift a floor
  (loose tests with no ratchet are also forbidden by the
  contract).
- **P0-279-3.** Does NOT modify `_STATUS.md` from inside this
  slice's own commits — orchestrator's surface.
- **P0-279-4.** Does NOT bundle frontend (vitest) coverage work
  into this slice. Filed as a spillover.
- **P0-279-5.** Does NOT lift more than 5-7 packages in the
  primary slice. The audit identifies the long tail; spillover
  slices handle them. (Hard cap to keep the PR reviewable.)
- **P0-279-6.** Does NOT add the `$tier_recommendations` block as
  an ENFORCED schema. It's documentation only; the `thresholds`
  map is the only enforced surface.
- **P0-279-7.** Does NOT lower any existing floor. Every change to
  `thresholds` is monotonically ↑ or unchanged.
- **P0-279-8.** Does NOT skip any package that's currently below
  70% from the audit doc. The audit MUST list every below-70
  package + a disposition; quietly dropping packages defeats the
  purpose.

## Skill mix (3-5)

1. Go testing — table-driven tests, sub-tests, real-Postgres
   integration test patterns (`//go:build integration`)
2. CI workflow editing — `cmd/scripts/coverage-gate` extension +
   `.github/workflows/ci.yml` job change
3. Coverage tooling — `gocovmerge` (or equivalent profile merge)
4. Test-design judgment — knowing when integration coverage suffices
   vs when a unit test adds real value

## Notes for the implementing agent

### Phase 2 grill output (self-grill, 4 user-confirmed decisions)

1. **Strategy = foundational audit + 5-package targeted lift.**
   NOT a per-package fan-out (too much orchestration); NOT a
   mega-slice (too risky).
2. **Integration coverage = INCLUDE in measurement** via
   `-coverpkg=./...` + profile merge. Several "low" packages will
   jump without writing new tests.
3. **Frontend (vitest) = OUT of scope.** Filed as spillover.
4. **Floor philosophy = per-package judgment with 70% aspirational
   guide.** Tiered-floor documentation block, no hard enforcement.

### Phase 3 threat model summary

Verdict: **no-mitigations-needed**. Pure test/CI engineering with
zero new threat surface.

### Implementation order (recommended)

1. **Run the audit first** — `go test -tags=integration -coverpkg=./... -coverprofile=integration.cov ./...`
   plus the existing unit profile; merge; tabulate. This is the
   load-bearing intelligence; everything else flows from it.
2. **Write the audit doc** — `docs/coverage-audit-2026-05.md`. The
   maintainer reviews this BEFORE the engineer writes any tests.
   If the audit reveals the 5 lift targets are different from the
   provisional set, document the override in D1.
3. **CI change next** — extend `coverage-gate` to read the merged
   profile. This is the smallest reversible change; ships
   independently if needed.
4. **5 lift targets** — pick by audit-confirmed leverage. Per
   package: design the tests, write them, ratchet the floor in the
   same commit. One commit per package keeps the diff bisectable.
5. **`count-integration` floor lifts** — pure threshold bumps; can
   land in one commit at the end.
6. **Spillover slices** — file as the last step. Don't open them as
   PRs; just file the spec files so the loop can pick them.

### gocovmerge alternative

If `gocovmerge` isn't already a dev dependency, alternatives:

- `go install github.com/wadey/gocovmerge@latest` (standard)
- `gocov` (heavier; converts to JSON)
- Hand-rolled awk merge (last resort; brittle)

Recommend `gocovmerge`; pin the version in `justfile`. Document in
D2.

### Provisional 5 lift targets (re-confirm post-audit)

These are first-cut candidates based on current unit-only %; the
audit may reshuffle:

- `internal/decision` (2% → core decision-engine logic)
- `internal/risk` (10% → risk-register core)
- `internal/board` (20% → board-pack composer)
- `internal/frameworkscope` (19% → RLS-touching framework scope)
- `internal/eval` (14% → control-eval engine — load-bearing per
  slice 012)

If integration-merge moves any of these above 70%, swap in the
next-highest-leverage `unit-add` package from the audit.

### Why CLI cmd packages stay low

`cmd/atlas`, `cmd/atlas-cli`, `cmd/atlas-openapi`, `cmd/aws-connector`
etc. are mostly cobra glue + main entry points. The cmdhttp + idem
sub-packages already sit at 98%; the cmd entry points themselves
are tested through smoke / integration paths, not via unit tests on
`func main()`. Honoring this convention: their floors stay where
they are; tiered guidance documents the rationale.

### Audit-doc table shape (for AC-1)

```markdown
| Package                  | Unit-only % | Integration-merged % | Disposition       | Notes                                                     |
| ------------------------ | ----------- | -------------------- | ----------------- | --------------------------------------------------------- |
| `internal/decision`      | 2           | 4                    | unit-add          | core decision-engine; needs real unit tests; lift target  |
| `internal/api/credstore` | 24          | 82                   | count-integration | already covered by slice 062 integration tests            |
| `cmd/atlas`              | 0           | 0                    | exempt            | main entry; tested via smoke + integration; floor stays 0 |
| ...                      | ...         | ...                  | ...               | ...                                                       |
```

Provenance: filed 2026-05-24 via `/idea-to-slice` from the
maintainer's observation that coverage had dropped substantially.
User-confirmed scope (4 decisions): foundational audit + 5-package
targeted lift; integration coverage included in measurement;
frontend out of scope; per-package judgment with 70% aspirational.
