# Slice 032 — Quarterly board pack — decisions log

Slice type: **AFK**. Per the slice-development workflow, the subjective
build-time calls below were made by Claude and recorded here rather than
blocking the merge on a human sign-off. The maintainer iterates
post-deployment. None of these touch the constitutional AI-assist boundary —
that is about how the _shipped product behaves at runtime_; this is about
_how the slice was built_.

The grill-with-docs investigation for this slice was completed before
implementation began; the six decisions below are the settled output of that
grill, recorded here for the audit trail.

---

## Decisions made

### D1 — `board_packs` immutability: single table + `status` column + RLS-predicated UPDATE + trigger

The quarterly pack has a draft → published lifecycle, unlike the slice-031
monthly brief (append-only, frozen at generation). Pattern chosen: a **single
`board_packs` table with a `status` column** (`draft` | `published`) and a
**stable id across the lifecycle** — NOT the `policies` new-row-on-publish
chain.

Immutability of a published pack is enforced by **two mechanisms,
defense-in-depth** — matching the slice-028 `audit_periods` freeze precedent
(RLS predicate paired with a guard):

1. The `tenant_update` RLS policy is `USING (current_tenant_matches(tenant_id)
AND status = 'draft')`. Once `status` flips to `published`, the policy's
   USING clause no longer matches the row, so `atlas_app` has no SQL UPDATE
   path to it.
2. A `BEFORE UPDATE` row trigger `board_packs_block_published_update` RAISEs
   if `OLD.status = 'published'`. With RLS in place this is belt-and-suspenders
   for `atlas_app`, but it holds the invariant for a BYPASSRLS role
   (`atlas_migrate`) and survives a future RLS-policy regression. This mirrors
   the slice-007 `framework_scopes_bounce_on_predicate_change` trigger
   precedent — an UPDATE guard expressed as a BEFORE UPDATE trigger.

The stable id means every artifact reference (PDF URL, Markdown download,
board-meeting minute) keeps resolving across the draft → publish transition.

**Confidence: high.** Directly applies two established codebase precedents
(audit_periods freeze RLS + framework_scopes trigger). The grill confirmed no
constitutional conflict.

### D2 — `content` shape: full structured pack in one JSONB column

The entire structured pack — every section, including each section's
`templated_text` / `override_text` / `approved` flag, plus the section's
structured data — is serialized into a single `content` JSONB column. One
row, atomic UPDATEs. While `draft`, the content is mutated in place; at
`publish` it is frozen.

This mirrors slice-031 `board_briefs.content` exactly. The JSONB shape can
evolve without a migration while older packs keep their original content. A
per-section-row table was rejected: it would turn every section edit into a
multi-row transaction and the publish gate into a join, for no gain — the
pack is always read and written whole.

**Confidence: high.** Established slice-031 pattern; the section envelope is
a clean, uniform shape across generated and operator-entered sections.

### D3 — operational-metrics / vendor / phishing sections are operator-entered, seeded empty

No v1 data source exists for operational metrics (phishing pass rate, P1
patch median, incident count, vendor reviews on time): the training connector
is a v2 item and the vulnerability-scanner connector is not built. Rather
than fabricate coverage — a CLAUDE.md anti-pattern ("AI-generated … without
human approval"; "continuous monitoring that's actually 24-hour polling") —
the `operational_metrics` section is **operator-entered**. The generator
seeds it with empty structured fields and a **templated placeholder
narrative** that explicitly names the section as operator-entered. The
operator types in the quarter's numbers and overrides the narrative before
approving the section.

The mockup's "vendor risk burndown" (§06) is folded into the operational
metrics section as the `vendor_reviews_on_time` / `vendor_reviews_total`
operator fields — the issue's own acceptance-criteria list places "vendor
reviews on time" under operational metrics.

**Confidence: high.** Fabricating data the platform cannot observe is an
explicit anti-pattern; operator-entered with an honest placeholder is the
only correct call.

### D4 — AC-8 findings source: board-package-owned `ListFailingEvaluationsForPack`, as-of `period_end`

The open-findings section reads failing control evaluations directly from
`control_evaluations`, bounded by the pack's `period_end` horizon, via a
**board-package-owned query** `ListFailingEvaluationsForPack`. It is
deliberately NOT routed through slice 030's `ListFailingEvaluationsAsOf` in
`oscal_export.sql`: slice 030's aggregator is **AuditPeriod-bound** (pinned to
a `frozen_at` horizon), whereas the board pack is a **calendar-quarter
artifact** pinned to `period_end`. The two have the same data semantics — a
failing control evaluation IS a finding for v1, there is no separate findings
table — but distinct, conflict-free query surfaces. The board-pack query also
keeps slice 032 from depending on slice 030's Python `oscal-bridge` for what
is a pure SQL read.

The `period_end` (a DATE) is widened to the end of that day
(23:59:59.999999 UTC) as the `control_evaluations.evaluated_at` (TIMESTAMPTZ)
upper bound, computed in Go — never a bare-placeholder expression that would
trip pgx type inference (SQLSTATE 42P08).

**Confidence: high.** Same semantics as the ratified slice-030 decision;
board-pack-owned keeps the slices conflict-free, which is required for the
parallel-batch workflow.

### D5 — coverage trend / investment-vs-coverage delta: operator baseline + operator spend

The platform has no historical posture store (slice-031 decision D2
precedent), so a true period-over-period coverage trend cannot be computed.
Instead:

- The `coverage_trend` section carries the program coverage at quarter end
  (computed live) and a `baseline_coverage_pct` **operator field**. The
  coverage delta = current coverage − baseline. The operator sets the
  baseline to the prior-quarter coverage to make the delta meaningful; until
  they do, the baseline is 0 and the templated narrative says so.
- The `investment` section accepts an operator `spend_usd` figure. The
  cost-per-coverage-point = `spend / max(delta, 1)` — the denominator is
  floored at 1 so a zero or negative delta still yields a finite, honest
  figure ("this spend bought at most this much per point").

`coverage_trend` and `investment` are coupled: editing the baseline
recomputes the delta on both, and editing the spend recomputes
cost-per-point. Both recomputes happen in Go (`recomputeDerived`).

**Confidence: medium.** This is the honest shape given no posture-history
store, but it leans on operator discipline (entering a correct baseline). It
should be revisited when a posture-history store lands — at that point the
baseline can be auto-populated from the prior quarter's stored posture and
the operator field becomes an override rather than the only source.

### D6 — publish gate: fixed enumerated section-key set, reject if any unapproved

The set of section keys is a **fixed, enumerated package constant**
(`SectionKeys`: posture, top_risks, coverage_trend, open_findings,
operational_metrics, investment, asks — the seven sections of the mockup).
`publish` iterates exactly this set and rejects the publish (HTTP 409,
`ErrPackNotReady`) if any section's `approved` flag is false. The
asks-of-the-board section is approvable like any other — it is not exempt.

A fixed set (rather than "approve whatever sections happen to exist") makes
the gate deterministic: a pack missing a fixed section fails the gate, so a
generation bug that drops a section cannot produce a publishable pack.

**Confidence: high.** The publish gate is the constitutional control here
(an audit-binding artifact requires one-click human approval — the per-section
approvals plus the `published_by` requirement are that approval); a fixed,
enumerated gate is the auditable shape.

---

## Revisit once in use

- **D5 (medium confidence)** — revisit when a posture-history store lands.
  The `baseline_coverage_pct` operator field becomes an auto-populated value
  (prior-quarter stored posture) with the operator field demoted to an
  override. The cost-per-coverage-point formula stays.
- **D3** — when the training connector (v2) and a vulnerability-scanner
  connector ship, the `operational_metrics` section's
  `phishing_pass_rate_pct` / `p1_patch_median_days` / incident fields can
  graduate from operator-entered to generated. The section envelope does not
  change — only the generator's seeding logic.
- **D4** — if a first-class findings table is ever introduced (it is not in
  the v1 plan — a failing evaluation IS a finding), `ListFailingEvaluationsForPack`
  would read that table instead. The wire shape (`Finding`) is already a
  superset-friendly envelope.

---

## Confidence summary

| Decision                                                     | Confidence | Revisit trigger                         |
| ------------------------------------------------------------ | ---------- | --------------------------------------- |
| D1 — single table + status + RLS-predicated UPDATE + trigger | high       | —                                       |
| D2 — full structured pack in one JSONB column                | high       | —                                       |
| D3 — operator-entered ops/vendor/phishing sections           | high       | training + vuln-scanner connectors ship |
| D4 — board-package-owned failing-evaluations read            | high       | first-class findings table introduced   |
| D5 — operator baseline + operator spend for the delta        | medium     | posture-history store lands             |
| D6 — fixed enumerated section-key publish gate               | high       | —                                       |
