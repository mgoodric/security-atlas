# 447 — PCI DSS v4.0 crosswalk loader (3rd framework; scope-reduction lever)

**Cluster:** Catalog
**Estimate:** M (1-2d)
**Type:** JUDGMENT (crosswalk-mapping accuracy + CDE-scope example)
**Status:** `not-ready` (gated on #438 — the generic crosswalk loader is unmerged)

## Narrative

Roadmap §10.2 names PCI DSS v4.0 in the framework expansion. PCI is the
framework where **FrameworkScope intersection (invariant #5)** is most
load-bearing: the PCI **Cardholder Data Environment (CDE)** is a deliberately
narrow scope, distinct from the SOC 2 system boundary, and **scope reduction is
the dominant lever** in a PCI program (OQ #19's resolution baked the
"reduce-aggressively" pattern into the FrameworkScope workflow precisely for
PCI). Adding PCI as the third framework both grows the catalog and exercises the
FrameworkScope machinery (slice 018) in its highest-value case.

This slice depends on slice **438**'s generic crosswalk loader: once 438
extracts the framework-agnostic importer (currently the slice-007 SOC 2 loader),
PCI is a data-plus-example slice. It ships **PCI DSS v4.0 → SCF crosswalk data**
(DRAFT YAML + spot-check audit log, same JUDGMENT pattern as 007/438) **plus a
worked FrameworkScope CDE-intersection example**: a concrete demonstration that
`effective_scope(control, PCI) = applicability_expr ∩ framework_scope.predicate`
narrows a control's applicability to the CDE — the invariant #5 proof for the
framework that needs it most.

**Scope discipline.** This is the **first thin PCI vertical slice**: one
framework version (PCI DSS v4.0), DRAFT crosswalk data on the generic 438
loader, and **one** worked CDE-intersection example proving invariant #5. It
does **not** attempt full PCI requirement coverage (curated high-signal subset),
does **not** ship a PCI SAQ workflow (canvas §10.3 phase-3 — explicitly
deferred), and does **not** ship the FrameworkScope ownership UX beyond using
the existing slice-018 machinery. **Follow-on slices:** full PCI v4.0 coverage;
PCI SAQ workflow (phase 3); CDE-scope-reduction reporting.

## Threat model (STRIDE)

Inherits slice 438's catalog-loader threat model (operator-supplied crosswalk
YAML → graph rows; tenant-scoped read path) plus the **FrameworkScope
intersection** surface, where a mis-scoped predicate could over- or
under-apply a control to the CDE.

**S — Spoofing.** No new authenticated endpoint — reuses the 438 generic
loader's CLI/admin auth + the existing `/anchors` read auth + the slice-018
FrameworkScope endpoints. No new ingress.

**T — Tampering.** PCI crosswalk YAML is operator input → graph rows; a
malformed FrameworkScope predicate could mis-scope the CDE.
**Mitigation:** the 438 loader's anchor-existence + code-namespacing validation
applies; the CDE-intersection example uses the slice-018 FrameworkScope
predicate path, which already validates predicate shape and carries the
draft→review→approved→activated lifecycle (OQ #19) — a predicate edit
invalidates approval and bounces to `draft` (no silent CDE drift).

**R — Repudiation.** Catalog import + FrameworkScope approval must be auditable.
**Mitigation:** the 438 import-summary log + the slice-018 FrameworkScope approval
record (approver, approved_at, predicate-diff hash) cover this; the spot-check
audit log is a durable JUDGMENT artifact.

**I — Information disclosure.** Same as 438 — the `/anchors` read returns
catalog-reference data only, not tenant control-implementation state; the CDE
predicate is tenant-scoped FrameworkScope data behind RLS.
**Mitigation:** reuse 438's read-path discipline + slice-018's RLS on
FrameworkScope predicates; no new field widens the payload.

**D — Denial of service.** Bounded crosswalk import (offline CLI) + bounded
per-requirement `/anchors` read + bounded effective-scope computation (one
control × one framework).
**Mitigation:** reuse 438's bounds; the effective-scope computation is
single-control, not an all-controls scan.

**E — Elevation of privilege.** Catalog-write + FrameworkScope-approve are
admin/auditor capabilities; this slice adds no new role.
**Mitigation:** reuse 438's catalog-write boundary + slice-018's auditor-approves-
predicate boundary (OQ #19); the CDE example does not bypass the FrameworkScope
approval lifecycle.

## Acceptance criteria

**Backend — PCI crosswalk data (on the 438 generic loader)**

- [ ] **AC-1.** A DRAFT PCI DSS v4.0 crosswalk YAML ships (curated high-signal
      subset) with `framework_slug: pcidss` + `framework_version: "4.0"` and
      STRM-typed edges to SCF anchors — loaded by the **438 generic loader**
      (no PCI-specific loader code).
- [ ] **AC-2.** Importing the PCI crosswalk creates `framework_requirements` +
      `fw_to_scf_edges` rows for PCI v4.0 without disturbing SOC 2 / ISO rows.
- [ ] **AC-3.** `GET /v1/requirements/{slug}/anchors` for a PCI requirement slug
      (e.g. `pcidss:4.0:1.1.1`) returns its SCF anchor(s) with STRM edge type.

**Backend — FrameworkScope CDE-intersection proof (invariant #5)**

- [ ] **AC-4.** A worked example demonstrates that the effective scope
      (`applicability_expr` intersected with the PCI `framework_scope.predicate`)
      narrows a control's applicability to the CDE, using the existing
      slice-018 FrameworkScope machinery.
- [ ] **AC-5.** An integration test asserts a control applicable org-wide is
      narrowed to the CDE under the PCI FrameworkScope predicate, and the same
      control's SOC 2 effective scope is **different** (the intersection differs
      per framework — invariant #5).
- [ ] **AC-6.** The **graph proof** from 438 extends: an SCF anchor shared
      across SOC 2 + ISO + PCI resolves to all three framework satisfactions
      through the single anchor (invariant #1, now three-framework).

**Tests**

- [ ] **AC-7.** Integration test (`//go:build integration`): PCI import →
      requirement-anchors read round-trip against real Postgres.
- [ ] **AC-8.** Integration test: the CDE-intersection effective-scope assertion
      (AC-5) against real Postgres + the slice-018 FrameworkScope path.

**Docs / JUDGMENT artifact**

- [ ] **AC-9.** A spot-check audit log
      (`docs/audit-log/447-pci-crosswalk-decisions.md`) records the
      mapping-accuracy + CDE-example JUDGMENT calls, confidence per cluster, and
      the "Revisit once in use" list.
- [ ] **AC-10.** A changelog entry.

## Constitutional invariants honored

- **#5 — FrameworkScope intersects with control applicability.** The CDE example
  proves the effective-scope intersection rule
  (`effective_scope = applicability_expr ∩ framework_scope.predicate`) for the
  framework where it matters most.
- **#1 — One control, N framework satisfactions.** Extends the 438 graph proof to
  three frameworks through one SCF anchor.
- **#7 — Mappings go requirement → SCF anchor, never requirement → requirement.**
- **#8 — OSCAL is the wire format, not the daily model.**

## Canvas references

- `Plans/canvas/05-scopes.md` §5.5 — FrameworkScope intersection (PCI CDE ≠ SOC
  2 system).
- `Plans/canvas/10-roadmap.md` §10.2 — PCI DSS v4.0 named.
- `Plans/canvas/11-open-questions.md` #19 — FrameworkScope ownership /
  reduce-aggressively (resolved; PCI is the load-bearing case).

## Dependencies

- **#438** (generic crosswalk loader + ISO data) — **NOT yet merged.** This
  slice loads PCI data through 438's framework-agnostic importer; it is
  `not-ready` until 438 merges (technical dependency: the implementation imports
  the generic loader 438 extracts).
- **#018** (FrameworkScope intersection + draft→review→approved→activated
  lifecycle) — `merged`. The CDE-intersection machinery.
- **#006** (SCF catalog importer) — `merged`. PCI edges target SCF anchors.

## Anti-criteria (P0 — block merge)

- **P0-447-1.** Does NOT add a PCI-specific loader — loads through 438's generic
  importer (the whole point of 438).
- **P0-447-2.** Does NOT create requirement → requirement edges (invariant #7).
- **P0-447-3.** Does NOT duplicate controls per framework (invariant #1).
- **P0-447-4.** Does NOT bypass the slice-018 FrameworkScope approval lifecycle
  for the CDE predicate (OQ #19).
- **P0-447-5.** Does NOT ship a PCI SAQ workflow — phase-3 deferred (canvas §10.3).
- **P0-447-6.** Does NOT regress SOC 2 or ISO ingestion.
- **P0-447-7.** Does NOT bundle pre-built SCF data (OQ #1).

## Skill mix (3-5)

`grill-with-docs` · `database-designer` (crosswalk import + FrameworkScope
predicate) · `tdd` (integration-first; CDE-intersection + three-framework graph
proof) · `security-review` (catalog-write + FrameworkScope RLS) · `simplify`.

## Notes for the implementing agent

- **Phase-2 grill output:** this slice is gated on 438 — do NOT start the PCI
  data work until 438's generic loader is merged; if 438 is still in flight when
  this is picked up, it stays `not-ready`. The PCI data + CDE example is the
  work; the loader is reused, not re-built.
- **JUDGMENT calls you own:** STRM edge types per PCI requirement, the curated
  subset, and the concrete CDE-intersection example (pick a control that is
  org-wide for SOC 2 but CDE-only for PCI — that contrast is the invariant #5
  proof). Record in the decisions log.
- AC-5 (effective scope differs per framework for the same control) is the
  load-bearing invariant-#5 demonstration — make the SOC-2-vs-PCI contrast
  explicit in the assertion.
- Detection-tier: `none` unless a bug surfaces.
