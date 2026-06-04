# ADR 0014 — Multidimensional scope and FrameworkScope intersection

**Status:** Accepted — **retrospective** record of two founding invariants
(CLAUDE.md architecture invariants #4 and #5). The decision was made and
shipped long before this ADR; this record reconstructs the trade-off context
and the rejected alternative after the fact. It does NOT re-open the question.

**Date:** 2026-06-04

**Records:** CLAUDE.md architecture invariants **#4** ("Scope is
multidimensional, not a tree. Scope cells = tuples over (BU × env × geo ×
cloud × data_class × product). Controls have `applicability_expr`.") and **#5**
("FrameworkScope intersects with control applicability … `effective_scope(control,
framework) = applicability_expr ∩ framework_scope.predicate`.").

**Grouping rationale:** #4 and #5 are recorded in a single ADR because #5 is
meaningless without #4 — the intersection in #5 operates on the
multidimensional cell space #4 defines. They are two halves of one scope model;
splitting them would force a reader to read both anyway. (This grouping is a
JUDGMENT call recorded in the slice decisions log; #6 RLS, #2 ledger, and #1
UCF each get their own ADR because each stands alone.)

**Canvas:** [`Plans/canvas/05-scopes.md`](../../Plans/canvas/05-scopes.md)
§5.1–5.5.

**Implementation reference (cited, not restated):**
[`internal/scope/`](../../internal/scope/) (scope + framework-scope) and
**ADR-0001** (the FrameworkScope predicate lifecycle workflow).

---

## Context

Where a control applies is not a single hierarchy. A real security program asks
questions like "what is broken in `prod` × `restricted` × `EU` on AWS?" — a
_cell_ defined by several independent axes at once, not a node in one tree.
Scope cuts across business unit, environment, geography, cloud account, data
classification, and product simultaneously (canvas §5.1).

A second, sharper reality (canvas §5.5): **the set of systems "in scope" differs
per framework.** A PCI Cardholder Data Environment is not the same set as the
HIPAA covered systems, which is not the same as the SOC 2 system boundary. The
same control may apply across a broad swath of an org's cells (engineering
reality), yet its evidence should only _count_ toward PCI within the CDE subset.
Conflating "where the control runs" with "where it counts for this framework"
produces wrong coverage numbers and, worse, audit-period scope drift — the
class of error where "the auditor approved scope X but you've been operating
under scope Y."

The questions this record answers: **how is scope shaped (tree vs. tuple
space), and how does per-framework scope combine with a control's own
applicability?**

## Decision

**Model scope as a multidimensional tuple space, not a tree, and define a
framework's effective scope for a control as the intersection of the control's
applicability with the framework's scope predicate** (canvas §5.1–5.5).

**Scope is a tuple space (invariant #4):**

- A **scope cell** is a tuple over the dimensions `(BU × env × geo × cloud ×
data_class × product)` — the coordinates of one slice of the org's universe.
- Each control carries an **`applicability_expr`**: a boolean expression over
  scope dimensions (e.g. `environment IN ('prod','staging') AND
data_classification IN ('restricted','confidential') AND
cloud_account.provider = 'aws'`). Given the org's universe of cells, the
  engine computes the **applicability set** — the cells where the control must
  be evaluated. A control's overall pass requires pass in _every_ applicable
  cell (canvas §5.1–5.2).
- Some dimensions are hierarchical (BU, geography) and support
  inheritance/override (canvas §5.3) — but the _scope itself_ is the tuple
  space, with hierarchy as a property of individual axes, not the organizing
  structure. Scope is "not a tree."

**FrameworkScope intersects with applicability (invariant #5):**

- A `FrameworkScope` row carries a **`predicate`** — a boolean over the same
  scope dimensions, in the same DSL as `applicability_expr` — describing which
  cells count "in scope" for a given framework version (canvas §5.5).
- The two combine by intersection:

  ```
  effective_scope(control, framework) = control.applicability_expr ∩ framework_scope.predicate
  ```

  Coverage for a framework requirement is the weighted strength × effectiveness
  aggregated **only over cells in `effective_scope`** — not over every cell
  where the control is applied (canvas §5.5). This is what keeps PCI coverage
  computed over the CDE, HIPAA over covered systems, and SOC 2 over the system
  boundary, from one shared control definition.

The two predicates have deliberately distinct meanings: `applicability_expr` is
_where the control IS applied_ (engineering reality); the framework-scope
predicate gates _where its evidence COUNTS_ for a framework (audit reality). The
intersection is the bridge.

## Consequences

**Positive:**

- The dashboard can roll up by control across cells, by cell across controls,
  and by framework requirement — because a cell is a queryable tuple, not a path
  in one tree (canvas §5.2).
- One control definition serves every framework correctly: its evidence counts
  toward each framework only within that framework's scope, so PCI / HIPAA /
  SOC 2 coverage numbers are each computed over the right cell subset without
  duplicating the control (composes with invariant #1, ADR-0013).
- Aggressive PCI/HIPAA scope reduction — the dominant control lever — is
  expressible as a narrow framework-scope predicate without touching the
  control's engineering applicability.
- Audit-period scope drift is defensible: the framework-scope predicate is a
  first-class, lifecycle-governed, approver-signed artifact (ADR-0001), so
  "what did the auditor approve as in-scope?" has a stable, hashed answer.

**Negative / accepted trade-offs:**

- **Two predicates per coverage question.** Computing framework coverage means
  evaluating `applicability_expr ∩ framework_scope.predicate` per cell, not a
  single membership test. More compute and more conceptual surface than a flat
  "is this control in this framework?" flag. Accepted: it is the only shape that
  models PCI-CDE-≠-HIPAA-covered-≠-SOC2-boundary correctly.
- **The cell space can be large.** A tuple over six dimensions has a
  combinatorial cell count; evaluation iterates the applicable subset. Accepted:
  controls' `applicability_expr` prunes the space to the cells that matter, and
  per-cell state is what makes "what's broken in `prod` × `restricted` × `EU`?"
  answerable.
- **Two predicates can be confused.** A contributor might write a control's
  applicability where a framework predicate belongs, or vice versa. Mitigated by
  the shared DSL plus the FrameworkScope lifecycle (ADR-0001) keeping the
  framework predicate in its own approver-gated workflow, distinct from the
  control's applicability.
- **Framework-scope predicates need governance.** A silently-broadened predicate
  after approval is a control failure, not a hygiene issue — which is exactly
  why ADR-0001 makes any predicate edit bounce the row back to `draft` and
  re-require approval.

## Alternatives considered (rejected — recorded retrospectively)

- **Scope-as-a-tree (a single hierarchy: e.g. org → BU → environment →
  system).** Rejected for invariant #4. A tree forces every scoping question
  through one parent/child axis and cannot natively express "`prod` ×
  `restricted` × `EU` × AWS" — a cell defined by several independent axes at
  once. Cross-cutting questions ("everything restricted-data across all BUs in
  the EU") become awkward subtree-unions in a tree but are a direct predicate in
  a tuple space. This is the concrete alternative invariant #4 rejects ("not a
  tree").
- **A single global scope shared by all frameworks (one "in-scope" set per
  org).** Rejected for invariant #5. It cannot distinguish the PCI CDE from the
  HIPAA covered systems from the SOC 2 boundary, so coverage would be computed
  over the wrong cell set for at least two of any three frameworks. The
  per-framework `predicate` intersected with applicability is what keeps each
  framework's coverage honest.
- **Per-framework duplicated controls scoped individually** (give each
  framework its own copy of the control with its own scope). Rejected — it
  re-introduces the per-framework-duplication anti-pattern (invariant #1,
  ADR-0013) to solve a scoping problem that intersection solves without
  duplication. The intersection lets one control serve N frameworks each at its
  own effective scope.
- **Storing `effective_scope` as a materialized per-(control, framework)
  set rather than computing it from the two predicates.** Rejected as a default:
  a stored set drifts the moment either the control's applicability or the
  framework predicate changes, re-introducing exactly the approval-vs-reality
  drift invariant #5 + ADR-0001 exist to prevent. `effective_scope` is derived
  on demand from the two governed predicates; the predicates are the source of
  truth, the intersection is computed.

## Related decisions

- **ADR-0001** (FrameworkScope predicate lifecycle workflow) is the governance
  half of invariant #5: it defines the `draft → review → approved → activated`
  lifecycle, the approval evidence shape, and the re-approval-on-edit rule that
  keeps the framework predicate honest. This ADR records _why scope is shaped
  this way_; ADR-0001 records _how a framework predicate is governed_.
- Composes with **ADR-0013** (UCF graph, one control N satisfactions): the graph
  answers "which requirements does this control reach"; this scope model answers
  "in which cells does its evidence count for each of those frameworks."
- Composes with **ADR-0011** (RLS): scope cells and FrameworkScope predicates
  are tenant-scoped rows under the same RLS boundary.
