# Slice 680 — Decisions log (JUDGMENT)

Data-quality + scoring clarity: audit-period labels, residual/severity
headers, new-risk pending state. Clusters audit items ATLAS-033,
ATLAS-038, ATLAS-029 (2026-06-10 demo-tenant audit, re-verified on `main`
build `2a3805b`).

- detection_tier_actual: manual_review
- detection_tier_target: manual_review

(The ATLAS-033 seed-label↔range contradiction is a data-presentation
defect, not a logic bug a unit/integration test would have failed on —
the seeder produced internally-consistent rows that were merely
mislabelled. The cheapest tier that catches "the label says Q3 but the
date range is Feb-May" is a human reading the rendered page, i.e. the
demo-tenant UI audit that surfaced it. The slice now ADDS a deterministic
guard at the integration tier — but the original detection was, and for a
mislabel-class defect realistically is, manual review.)

---

## Decisions made

### D1 — ATLAS-033 audit-period label: derive the quarter from the period's own start date

**Options considered.** (a) Keep the `Q{i%4+1}` index label and instead
re-order the loop so index aligns with date; (b) derive the quarter label
from the actual `period_start` month; (c) drop the quarter entirely and
label by date range only.

**Chosen: (b).** A pure `auditPeriodName(start)` helper computes the
calendar quarter of the period's own start (`Q1` = Jan-Mar … `Q4` =
Oct-Dec) and the start year. This makes the label↔range contradiction
**structurally impossible** — the label is a function of the range, not
of loop position, so any future change to the period-spacing math keeps
the label honest automatically. Option (a) would re-introduce the same
fragility the moment the spacing changes; option (c) loses the
quarter-shorthand auditors expect on a SOC 2 period.

**Rationale.** Pattern-matches the project's "make the invariant
structural, not incidental" posture. The helper is pure and table-tested,
plus a property test asserts the quarter suffix equals the calendar
quarter of the start across all 12 months.

**Confidence: high.**

### D2 — ATLAS-033 framework version: resolve a readable label on the LIST path (no schema change)

**Options considered.** (a) Render the truncated UUID but add a tooltip
explaining it's an id; (b) add a backend join that surfaces a readable
`<framework name> <version>` label on the list wire and render it; (c)
add a new framework-label endpoint the frontend calls per row; (d) store
a denormalized label column.

**Chosen: (b).** The data was **already present**: `frameworks.name`
joined with `framework_versions.version` gives "SCF 2025.2". A new
`ListAuditPeriodsWithFrameworkByTenant` sqlc query LEFT JOINs the two
catalog tables; the period domain carries a `FrameworkLabel`; the wire
gains `framework_label` (`omitempty`); the cell renders it with a
**fallback** to the truncated UUID (in mono) when the label is absent.

**Rationale.** Option (a) leaves the core complaint (the column reads as
a hash) unaddressed. Option (c) adds a per-row round-trip + a new endpoint
for a single string — over-engineered (anti-abstraction gate). Option (d)
is a schema change for data that already exists. The LIST-only join is the
minimum correct change: the Create/Get/Freeze wire shapes are untouched
(they don't join the catalog), and the `omitempty` + frontend fallback
keep an unresolved framework version honest rather than crashing.
`LEFT JOIN` (not `INNER`) so a period whose framework version no longer
resolves still appears in the list.

**Confidence: high.**

### D3 — ATLAS-038: residual vs severity are independent axes (NOT a bug); clarify headers

**The question the audit raised.** The same residual maps to different
severities across seeded risks (residual 0.36 → severities 12/16/20),
which reads as a scoring inconsistency.

**Finding: confirmed independent axes, not a bug.** Per canvas §6.2,
`Residual = inherent × (1 − control_effectiveness)`. The list's
**"Severity"** column is the **inherent** 5×5 scalar (likelihood ×
impact, BEFORE controls; computed by the risks handler as `severity`).
The **"Residual"** column is the **after-controls** normalized score
(`residual_score` JSONB, 0..1). They measure different things: two risks
with the same inherent severity legitimately carry different residuals
(different controls / effectiveness), and two risks with the same residual
legitimately carry different inherent severities. There is no scoring bug
to fix.

**Fix chosen: column-header copy + tooltip only.** Headers now read
**"Inherent severity"** and **"Residual (after controls)"**, each wrapped
in a native `title` tooltip (the repo's established tooltip idiom — there
is no shadcn `tooltip` primitive in-tree; `format.ts` already returns
tooltip strings consumed by `title=`). The scoring model is unchanged
(anti-criterion). This stays strictly within the open-question boundary:
it clarifies how the **existing** columns are labelled and does NOT touch
the risk-methodology-default open question (5×5 vs FAIR) in
`Plans/canvas/11-open-questions.md`.

**Confidence: high** (that they're independent axes — this is the
documented model). **medium** (that "Inherent severity" / "Residual
(after controls)" is the clearest possible wording — a real operator may
prefer "Inherent (5×5)" or a combined column; see Revisit).

### D4 — ATLAS-029: distinguish "pending evaluation" from a malformed score

**The shape on `main`.** A newly-created risk omits `residual_score` in
the form; the create path (`internal/risk/store.go` `defaultResidual`)
stores an empty `{}` JSONB, and `review_due_at` is omitted until the
evaluator backfills. The old cell rendered a bare "—" for both.

**Options considered.** (a) Render "Pending evaluation" only when the
residual is null/absent, and keep "—" for a present-but-malformed score;
(b) treat null / empty-object / partial-score all as "pending"; (c) add a
distinct "malformed" error state.

**Chosen: (b).** The classifier `residualState(score)` returns `"pending"`
for null / undefined / non-object / empty `{}` / a score missing a numeric
likelihood+impact, and `"scored"` only for a valid pair. There is
deliberately **no "malformed" state**: on `main` today the only way a risk
has a non-scorable residual is "not yet evaluated" (the `{}` default), so a
scary error state (option c) would be inventing a failure mode that does
not occur. A partial score is indistinguishable from not-yet-evaluated at
the UI tier and the honest read is the same — the evaluator has not
produced a usable residual.

**Rationale.** Keeps the affordance truthful without over-modelling.
`reviewDuePending(review_due_at)` mirrors this for the review-due column
(absent/empty → pending), since the evaluator backfills both together.

**Confidence: high** (the `{}`-is-pending mapping matches the create
path). **medium** (collapsing "partial" into "pending" — see Revisit).

### D5 — Test placement: pure-Go unit + integration guard + vitest + hermetic Playwright

Pure branches (`auditPeriodName`, `frameworkLabel`, `frameworkVersionLabel`,
`residualState`, `reviewDuePending`) get fast pure-Go / vitest unit tests
(the CLAUDE.md Q-2 convention). The demoseed integration test ADDS a
label↔range guard (reads every seeded period back, asserts the label
suffix matches the calendar quarter of `period_start`) — the ATLAS-033
regression net. The contract golden gains `framework_label`. The two new
Playwright specs are **hermetic** (route-mock the `/api/risks` and
`/api/audits` BFF GETs per the slice-594 shared-DB → hermetic-mock
lesson), so they assert wire-to-screen rendering without a seed
dependency.

**Confidence: high.**

---

## Revisit once in use

1. **Header wording (D3).** "Inherent severity" / "Residual (after
   controls)" is the clearest wording I could pattern-match, but a real
   solo-security-leader using the register daily may prefer different
   shorthand (e.g. "Inherent (5×5)" + "Residual 0-1", or a single
   combined "Inherent → Residual" column). Re-check with a real operator.
   The native `title` tooltips are a minimal affordance; if the project
   later adopts a shadcn `tooltip` primitive, these become hover cards.

2. **"Pending" vs "malformed" collapse (D4).** Today every non-scorable
   residual is the `{}` not-yet-evaluated case, so collapsing partial
   scores into "pending" is honest. If a future surface can persist a
   genuinely malformed `residual_score` (e.g. a bad import or a FAIR-shaped
   score with no L×I), the classifier should grow a distinct state so a
   real data error doesn't masquerade as "awaiting the evaluator." Revisit
   when an import path or a non-5×5 methodology lands.

3. **Framework-label format (D2).** The label is `"<name> <version>"`
   joined with a single space ("SCF 2025.2", "SOC 2 2017"). If the demo
   catalog later carries versions like "2022-Edition" or a date, confirm
   the rendered label still reads cleanly; consider a per-framework format
   if the version strings get noisy.

4. **Demo period ordering.** Periods now emit oldest-first (frozen =
   oldest). The `/audits` list applies its own `created_at DESC` ordering
   on top, so the visible order is newest-created-first. Confirm the
   intended default sort with a real auditor once the demo runs against a
   real SOC 2 timeline (slice 671's demo eval-run may shift the seeded
   dates).

---

## Confidence summary

| Decision                                             | Confidence                         |
| ---------------------------------------------------- | ---------------------------------- |
| D1 — derive quarter from start date                  | high                               |
| D2 — LIST-path framework label (no schema change)    | high                               |
| D3 — residual/severity independent axes; header copy | high (axes) / medium (wording)     |
| D4 — pending classifier; no malformed state          | high (mapping) / medium (collapse) |
| D5 — test placement                                  | high                               |
