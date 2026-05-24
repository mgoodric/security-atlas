# 256 — Coverage column on /controls/{id} · decisions log

**Slice:** `docs/issues/256-control-detail-coverage-column-weighted.md`
**Branch:** `frontend/256-control-detail-coverage-column`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-24
**Type:** JUDGMENT

The slice spec calls out two explicit JUDGMENT decisions (D1, D2) and
several implementation shape choices. Decisions recorded inline below
so the maintainer iterates post-deployment rather than blocking the
merge on a sign-off gate (per `Plans/prompts/04-per-slice-template.md`
"Slice types").

---

## Decisions made

### D1 — `null` coverage when the control has zero effectiveness data

**Decision:** When `eval.Effectiveness.TotalCount == 0`, every
in-scope row reports `coverage: null`. We do NOT prorate, and we do
NOT emit `coverage: 0`.

**Options considered:**

| Option                                                                                | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                   |
| ------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) **Prorate** — use whatever `pass_rate` the rolled-up evaluations produce.         | Partial-window prorate is fine WHEN `TotalCount > 0` (already supported — `eval.Effectiveness` computes `pass_count / total_count` over whatever evaluations exist in the 30-day window). But when `TotalCount == 0` there is no rate to prorate; (a) reduces to "emit 0" or "emit null", which is just D1's choice in disguise. We document the prorate behavior as inherited from the engine and pick the zero-data rule. |
| (b) **`null`** — _chosen_.                                                            | AC-2 is explicit: "row with zero effectiveness data returns `null` (not `0` — distinguish 'no data' from 'perfectly failing')." Anti-criterion P0-256-1 also requires no client-side fabrication; the spec wants the renderer to honor the backend's null. (b) is the only option that aligns with both contracts.                                                                                                          |
| (c) Add a `coverage_window_days` sub-field so the renderer can show "(7d window)".    | Rejected for v0 — adds wire-shape surface without a renderer hook in this slice. The renderer presently has no place to surface the sub-line; adding the field without using it would be dead weight. File as a follow-up once a real UX need surfaces (e.g., "operator can't tell if a 0.94 is over 5d or 30d").                                                                                                           |
| (d) Add `coverage: 0` and let the UI distinguish via the `effectiveness.total_count`. | Rejected — forces the renderer to consult a second piece of data to decide whether 0 means "failing" or "no data." Constitutional AI-honesty cuts against shipping a number that needs out-of-band context to interpret.                                                                                                                                                                                                    |

**In-scope, partial-window data (TotalCount > 0 but < what a full 30
days would produce):** the engine's `PassRate` is `PassCount /
TotalCount` over whatever rows exist, so a 7-day-old control with 5
pass and 1 fail reports `coverage = strength × (5/6) ≈ strength ×
0.833`. That IS prorate, inherited from slice 012. No special handling
here. If a future UX surface needs to distinguish "10-day window"
from "30-day window" visually, that surfaces as a v1 follow-up that
adds the sub-field per option (c).

**Confidence:** **high.** AC-2 is explicit; option (a) is not actually
in conflict with option (b) at the chosen boundary.

### D2 — Chevron is rendered but non-interactive (with explanatory tooltip)

**Decision:** Each row renders a chevron affordance (visible
right-most cell, `data-testid="coverage-row-chevron"`,
`aria-disabled="true"`, with a tooltip "Per-requirement inspector
lands in a follow-up slice"). The chevron is NOT a link, NOT a
button, and clicking the row is a no-op. The per-requirement
drill-down destination ships in a separate slice.

**Options considered:**

| Option                                                                                                  | Why rejected / why chosen                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| ------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| (a) Ship `/controls/[id]/mappings/[edge_id]` as a new route in this slice.                              | Out of scope — that's a full mapping-inspector slice with its own API surface, query plumbing, and tests. Would 2-3× the slice size and pull it off the "1d" estimate.                                                                                                                                                                                                                                                                                                                                                                  |
| (b) Tab-jump to a mappings tab if #254 has landed.                                                      | Slice 254 isn't on `main` (verified at session start — the Mappings tab on `/controls/{id}` would be the destination, but its presence is a dependency the slice spec admits is conditional). Avoiding a dependency on an unmerged sibling.                                                                                                                                                                                                                                                                                             |
| (c) **Render the chevron with an explanatory tooltip and `aria-disabled`; non-interactive** — _chosen_. | Anti-criterion P0-256-4 explicitly admits this: "render the chevron visually but make the row non-clickable with a tooltip ('per-requirement view lands in slice NNN')." The slice spec's own recommendation. Keeps the slice scoped and surfaces the affordance honestly without shipping a dead-link 404 (the slice 178 anti-pattern). The tooltip names the next step rather than burying it. `aria-disabled` (not `aria-hidden`) so the affordance remains in the accessibility tree but assistive tech reads it as not yet active. |
| (d) Don't render the chevron at all.                                                                    | Rejected — AC-4 says "the chevron is visible affordance; the destination can be a placeholder if necessary." The visible-affordance commitment is load-bearing for the mockup parity (mockup line 193 + every other row).                                                                                                                                                                                                                                                                                                               |

**Follow-up slice:** the per-requirement inspector (per-edge
drill-down) is the natural next slice; it should land before this
chevron's tooltip can be retired. Filing as a follow-up is left to
the maintainer (no spillover slice filed in this PR — the JUDGMENT
template's preference is to flag, not pre-commit, follow-on scope).

**Confidence:** **high.** Spec explicitly admits option (c) as the
small-slice path.

### D3 — Backend computes coverage; frontend renders the backend's value verbatim

**Decision:** The Go handler at
`internal/api/ucfcoverage/handlers.go::ControlCoverage` computes
coverage server-side per row (`strength × eff.PassRate` when in scope
AND `eff.TotalCount > 0`; null otherwise) and emits it as a
`coverage` field on every row. The TypeScript renderer at
`web/components/control/coverage-table.tsx` reads that field verbatim
and never recomputes — even as a "missing data" fallback.

**Rationale:** Slice 041's stated reason for deferring the column was
"fabricating it [client-side] here would risk a number that disagrees
with the backend." Slice 256 resolves that tension by making the
number a backend field. The frontend's only job is presentation:
clamp to [0, 1] for display defense-in-depth, render two decimals,
render "n/a" for null. The pure helpers (`formatCoverage` /
`coverageBarPercent` at `web/components/control/coverage.ts`) are
vitest-covered per AC-6.

**Anti-criterion P0-256-1 verification:** searched the frontend tree
for any path that multiplies `strength` by another field — none
exists. The only float arithmetic on `CoverageRequirement` in the
codebase is now in the pure helpers, which take coverage as a number
or null and clamp/format it. They do not multiply.

**Confidence:** **high.** This is the spec's stated resolution path
(slice header, lines 38-42).

### D4 — Wire-shape stability: `coverage` field always emitted (numeric or `null`), never absent

**Decision:** The Go struct tag is `json:"coverage"` (no
`omitempty`), so every response row carries a `coverage` key whose
value is either a number or `null`. The TS type is
`coverage: number | null` (not `number | null | undefined`).

**Rationale:** TypeScript strict-null-checks treats "key absent" and
"key explicit null" identically at the type level, but at the wire
level callers in other languages (and the OSCAL bridge) see a real
difference. The slice 250-series wire-shape audit (slice 253
specifically) showed that "field absent" is a slow-rolling
compatibility tax. Always emit. The integration test
`TestControlCoverage_Slice256_CoverageKeyAlwaysEmitted` asserts the
raw JSON contains the key, not just that the decoded struct's pointer
is nil — protecting against `omitempty` drift in a future refactor.

**Side effect:** `/v1/anchors/{id}/requirements` (which doesn't
compute coverage) also now emits `"coverage": null` on every row.
This is a benign superset of the previous shape (no caller depended
on the field's absence); the field's meaning on that endpoint is "not
computed at this surface — see /controls/{id}/coverage for the
weighted value." The existing slice-007 backwards-compat test
(`TestAnchorRequirements_BySCFID`) still passes because it never
asserts the absence of any key.

**Confidence:** **high.** No-omitempty is the conservative wire
contract.

### D5 — Two-stage handler constructor (`New` + `AttachCoverage`)

**Decision:** `ucfcoverage.New(pool)` returns a Handler that emits
the slice-008 shape WITHOUT the per-row coverage field. To wire the
slice-256 behavior, callers (`httpserver.go`) chain
`.AttachCoverage(engine, scopeStore, fwScopeStore)`. When the
attachment hasn't happened, `applyCoverage` is a no-op and the rows
ship without `coverage` populated (the wire still emits the `null`
default).

**Why not require the dependencies in `New`?** Three integration
tests in the existing slice-008 suite (`TestRequirementCoverage_*`,
`TestAnchorRequirements_*`) don't need eval/scope/framework_scope
plumbing. Forcing the dependencies into `New` would force every test

- every dev-mode server (no-NATS, no-evidence variants) to wire
  three more stores. The two-stage pattern is the same shape slice 013
  used to graft an optional ingest pipeline onto the evidence handler.
  The integration test suite continues to spin up the full
  `api.Server` via `setupHTTPServer`, which does call
  `AttachCoverage`, so the AC-1/AC-2 contracts ARE exercised
  end-to-end.

**Confidence:** **medium-high.** Slight smell ("the slice-008
constructor now emits a deliberately-incomplete shape"), but the
alternative (force every caller to wire three more stores) is worse.
Documented in the `Handler` doc-comment so a future reader doesn't
trip on the absent field.

### D6 — `null` per-fv resolution when a malformed framework_version_id appears on a catalog row

**Decision:** In `applyCoverage`, if `uuid.Parse(req.FrameworkVersionID)`
fails (which would indicate a slice-007 catalog corruption — the
catalog should never ship a non-UUID), the row is silently treated as
out-of-scope (coverage null) rather than 500-ing the entire response.

**Rationale:** A single bad catalog row should not break the page.
Logging this case would be valuable; in this slice, the choice is the
honest-degradation path. Filed as a soft follow-up: instrument
`applyCoverage` with a structured-log warn when a uuid parse fails.

**Confidence:** **medium.** Defensive choice; the alternative (500
the whole response) is less defensible but more conspicuous.

---

## Constitutional invariants honored

- **Invariant 1 — One control, N framework satisfactions.** The
  Coverage column visualizes this directly: the same control's
  posture, evaluated per-framework, weighted by STRM strength × 30d
  effectiveness × FrameworkScope.
- **Invariant 5 — FrameworkScope intersects with applicability.** The
  per-fv in-scope determination uses
  `frameworkscope.EffectiveScope(applicability, predicate)` — the
  same primitive the slice-018 `/effective-scope` handler uses.
  Out-of-scope rows report `coverage: null`, never `0`.
- **Invariant 2 — Ingestion and evaluation separated.** Coverage is
  computed at request time from already-persisted evaluations; the
  handler is a pure read.
- **UI-honesty.** The page renders the backend's computed coverage
  verbatim. Out-of-scope is `null → "n/a"`; no client-side fabrication.

## Anti-criteria honored

- **P0-256-1.** No client-side fabrication. The frontend never
  multiplies `strength` by anything; the only float arithmetic on
  `CoverageRequirement` is in the pure render helpers, which take
  `coverage` as input.
- **P0-256-2.** JSON precision preserved. `*float64` on the wire; the
  renderer rounds to two decimals at the cell, not at the wire.
- **P0-256-3.** Existing Strength column is preserved (two decimals).
  The user sees both numbers (strength and coverage) side-by-side.
- **P0-256-4.** No 404 destination. The chevron is non-interactive
  with an explanatory tooltip; the per-requirement inspector ships in
  a follow-up slice (filed as a soft follow-up for the maintainer to
  prioritize).

---

## Test surfaces

- **Go integration** (`internal/api/ucfcoverage/integration_test.go`):
  four new `TestControlCoverage_Slice256_*` tests cover (a) in-scope
  numeric, (b) out-of-scope null, (c) zero-effectiveness null, (d)
  wire-shape key-always-emitted. Runs under `-tags=integration`.
- **Frontend vitest** (`web/components/control/coverage.test.ts`):
  AC-6 surface — `formatCoverage` and `coverageBarPercent` pure
  helpers, 8 cases including NaN defense-in-depth and clamping.
- **Playwright e2e** (`web/e2e/control-detail.spec.ts`): new
  `slice 256: coverage column renders both numeric and n/a rows`
  test. Assertions commented pending slice-082 seed harness, matching
  the surrounding tests' convention.

## Skill mix exercised

1. Go + sqlc — extended the existing ucfcoverage Handler;
   two-stage `New` + `AttachCoverage` constructor.
2. PostgreSQL — no schema changes; relied on slice 012's existing
   `control_evaluations` rollup + slice 018's `framework_scopes`
   activation lookup.
3. shadcn/ui table cells + Tailwind — added Coverage column +
   chevron cell + footer prose.
4. UI-honesty discipline — null vs 0; non-interactive chevron.
5. JUDGMENT-slice decisions log — this file.
