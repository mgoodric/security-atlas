# Slice 447 — PCI DSS v4.0 crosswalk: JUDGMENT decisions log

**Slice:** 447 — PCI DSS v4.0 crosswalk loader (3rd framework; CDE scope-reduction lever)
**Type:** JUDGMENT (crosswalk-mapping accuracy + CDE-intersection example)
**Parent dependency:** slice 438 (generic crosswalk loader) — merged.
**Author:** agent (no human sign-off gate; per the JUDGMENT slice convention the
agent makes the mapping calls and records them here for post-deployment review).

This log records the subjective build-time calls: the curated subset, the
per-requirement PCI → SCF anchor mappings, the low-confidence flags, and the
CDE-intersection example shape. It is the durable artifact a maintainer (or an
auditor scanning the catalog) reads to understand why each edge is what it is.

---

## D1 — Loaded through the slice-438 generic loader (no PCI-specific code)

PCI DSS v4.0 ships purely as data (`data/crosswalks/pci-dss-4.0.yaml`) plus
tests. It imports through `internal/api/soc2import` exactly as ISO 27001 does:
the generic `requirement_code:` YAML key, the same `soc2import.Load` +
`soc2import.Import` entry points, the same anchor-existence + STRM + strength
validation. **No new loader code was added** (P0-447-1). This is the whole
point of slice 438 — the third framework proves the loader generalizes again.

**Confidence:** HIGH. Verified by `TestLoad_ShippedPCICrosswalkParses` and the
integration suite, which call the identical functions the ISO/SOC 2 suites do.

## D2 — Licensing posture: identifiers + titles + original descriptions only

PCI DSS v4.0 is copyrighted by the PCI Security Standards Council. The YAML
references only:

- requirement **identifiers** (e.g. `8.3.1`, `1.3.1`) — factual references, not
  protected expression;
- short **titles** — paraphrased/factual labels;
- an **original agent-authored** one-line `body` for each requirement.

It reproduces **no verbatim text** of the PCI DSS standard. SCF anchors are
imported separately by the operator (slice 006); this file ships only the
PCI → SCF **edge** data, so no pre-built SCF catalog is bundled here
(P0-447-7). This mirrors the slice-438 ISO posture exactly.

**Confidence:** HIGH on the posture. The maintainer should still confirm the
project's overall standards-redistribution stance (open question in CLAUDE.md:
"SCF redistribution terms (legal review)") before a public release that bundles
any framework crosswalk; PCI identifiers/titles are lower-risk than the SCF
catalog text itself.

## D3 — Curated subset: 31 requirements across all 12 principal requirements

PCI DSS v4.0 has ~300+ defined-approach sub-requirements. Full coverage is a
follow-on slice (see Spillover below). For this first thin PCI vertical slice I
curated **31 high-signal requirements**, at least two per principal requirement
(Req 1–12), biased toward:

1. **The SOC 2 / ISO overlap zone** — so a shared SCF anchor demonstrates
   invariant #1 across three frameworks (the load-bearing AC-6 proof).
2. **PCI-distinctive controls** — stored-PAN encryption (3.5.1), CDE inbound
   restriction (1.3.1), network segmentation (1.4.1), MFA into the CDE (8.4.1) —
   which show where the narrow CDE scope diverges from a broad SOC 2 boundary.

**Confidence:** HIGH that the subset is representative; MEDIUM that it is the
_optimal_ subset — a PCI QSA reviewing post-deployment may reprioritize which
sub-requirements earn a row.

## D4 — Per-requirement PCI → SCF anchor mappings

Every edge is **requirement → SCF anchor**, never requirement → requirement
(invariant #7, P0-447-2). All anchors resolve against the slice-006 sample
fixture (verified: all 26 distinct anchors used exist in
`migrations/fixtures/scf-sample.json`). Strength rubric is identical to the
SOC 2 + ISO crosswalks for cross-framework consistency.

Three-framework shared anchors (the invariant-#1 surface), high confidence:

| PCI req               | SCF anchor | also satisfies               | rel / strength |
| --------------------- | ---------- | ---------------------------- | -------------- |
| 8.2.1 unique IDs      | **IAC-01** | SOC 2 CC6.1 + ISO A.5.15     | equal / 0.9    |
| 8.4.1 MFA into CDE    | IAC-06     | SOC 2 CC6.1-MFA + ISO A.8.5  | equal / 0.9    |
| 7.2.1 need-to-know    | IAC-07     | SOC 2 CC6.2/6.3 + ISO A.5.18 | equal / 0.9    |
| 4.2.1 PAN in transit  | CRY-08     | SOC 2 CC6.7 + ISO A.8.24     | equal / 0.9    |
| 5.2.1 anti-malware    | END-07     | SOC 2 CC6.8 + ISO A.8.7      | equal / 0.9    |
| 6.3.1 vuln mgmt       | VPM-01     | SOC 2 CC7.1 + ISO A.8.8      | equal / 0.9    |
| 10.2.1 audit logs     | AAA-01     | SOC 2 CC7.2 + ISO A.8.15     | equal / 0.9    |
| 9.2.1 physical entry  | PES-04     | SOC 2 CC6.4 + ISO A.7.2      | equal / 0.9    |
| 12.1.1 infosec policy | GOV-01     | SOC 2 CC1.1 + ISO A.5.1      | equal / 0.9    |
| 12.6.1 awareness      | HRS-04     | SOC 2 CC1.4 + ISO A.6.3      | equal / 0.9    |
| 12.8.1 third-party    | TPM-01     | SOC 2 CC9.2 + ISO A.5.19     | equal / 0.9    |
| 12.10.1 IR plan       | IRO-04     | SOC 2 CC7.3/7.4 + ISO A.5.24 | equal / 0.9    |
| 1.3.1 CDE inbound     | NET-04     | SOC 2 CC6.6 + ISO A.8.20     | equal / 0.9    |

**Confidence:** HIGH for the `equal`/0.8-0.9 rows above. These are the same
anchor families the SOC 2 + ISO crosswalks already use for the analogous
controls, so the three-framework alignment is internally consistent.

## D5 — Low-confidence mappings flagged for maintainer review

Flagged for spot-check priority (strength ≤ 0.6, or a known gap in the sample
SCF anchor set):

| PCI req                              | anchor | strength         | why flagged                                                                                                  |
| ------------------------------------ | ------ | ---------------- | ------------------------------------------------------------------------------------------------------------ |
| 3.2.1 minimize stored account data   | DCH-01 | 0.6 (intersects) | PCI retention-minimization is narrower/CDE-specific than Data Classification & Handling; partial overlap.    |
| 6.4.1 protect public-facing web apps | SEA-01 | 0.6 (intersects) | WAF / app-layer review — sample SCF set lacks a dedicated WAF anchor; loose fit. **LOW CONFIDENCE.**         |
| 11.4.1 penetration testing           | VPM-04 | 0.5 (intersects) | sample SCF set lacks a dedicated pen-test anchor; pen-test partially overlaps patch/VPM. **LOW CONFIDENCE.** |

These three are the explicit candidates for a maintainer (ideally a QSA) to
re-anchor once the full SCF catalog (which has dedicated VPM/SEA sub-anchors for
scanning, pen-testing, and WAF) is imported. The sample fixture's coarser anchor
set is the limiting factor, not the mapping logic — identical to the ISO
crosswalk's A.5.7 threat-intel and A.8.11 data-masking low-confidence rows.

## D6 — CDE-intersection example (invariant #5, AC-4/AC-5)

The worked example proves `effective_scope(control, framework) =
applicability_expr ∩ framework_scope.predicate` (canvas §5.5) using the existing
slice-018 machinery (`frameworkscope.Canonicalize` + `frameworkscope.EffectiveScope`)
— no new scope code, no bypass of the approval lifecycle (P0-447-4).

**The chosen contrast (the load-bearing AC-5 call):** one control that is
applicable **org-wide** (`applicability_expr = true`) — the access-control /
authentication control mapping to IAC-01, the same three-framework shared anchor.

- **CDE predicate (PCI):** `prod ∧ data_classification=restricted` — the cells
  that store/process/transmit cardholder data.
- **SOC 2 predicate:** `true` (no narrowing — the broad system boundary).

Result: the SAME control resolves to **2 CDE cells under PCI** vs **all 4 cells
under SOC 2**. The marketing-prod and internal-dev cells are in SOC 2's scope but
NOT in PCI's CDE. That per-framework divergence for one control is invariant #5
demonstrated for the framework where it matters most.

**Confidence:** HIGH. The example is a deterministic pure-Go assertion
(`internal/frameworkscope/pci_cde_test.go`) over the real intersection
primitive. Modeling the CDE as `prod ∧ restricted` is a deliberate
simplification for the example; a real tenant's CDE predicate would enumerate
specific business units / cloud accounts — that richer per-tenant predicate
authoring UX is the deferred FrameworkScope-ownership workflow (Spillover).

## D7 — SOC 2 + ISO ingestion untouched (additive change only)

The change is purely additive: a new YAML file + new test files + a coverage
floor lift. No edit to `loader.go`, `import.go`, the SOC 2 crosswalk, the ISO
crosswalk, or the scope/frameworkscope packages. `TestPCIImport_DoesNotDisturbSOC2orISO`
asserts the SOC 2 and ISO edge counts are byte-stable across a PCI import
(P0-447-6).

**Confidence:** HIGH. Verified by the full soc2import + frameworkscope
integration suites passing unchanged.

## D8 — Coverage floor ratchet (shared-surface this batch)

`internal/api/soc2import` floor lifted **75 → 78** in
`cmd/scripts/coverage-thresholds.json`. Measured merged (unit + integration)
coverage is 80.2%; the documented methodology is `floor(measured - 2pp)` =
`floor(78.2)` = 78. The new PCI loader + integration tests contribute to this
package's coverage (they exercise the same import path the ISO tests do, plus
new pure-Go branches in `pci_loader_test.go`). The ratchet is monotonic up.

---

## Detection-tier classification (slice 353 Q-13)

- **detection_tier_actual:** `none` — no bug surfaced during the slice. All ACs
  passed on first integration run; the only iteration was environment setup
  (granting the local atlas_app role catalog-table access to mirror CI's
  atlas_migrate write path).
- **detection_tier_target:** `none`.

## Revisit once in use

1. **Re-anchor the 3 low-confidence rows (D5)** — 3.2.1, 6.4.1, 11.4.1 — once the
   full SCF catalog with dedicated scanning / pen-test / WAF anchors is imported.
2. **Expand the curated subset toward full PCI v4.0 coverage** (follow-on slice).
3. **Per-tenant CDE predicate authoring UX** — the example uses a synthetic
   `prod ∧ restricted` predicate; the real FrameworkScope-ownership workflow for
   authoring/approving a tenant's CDE predicate is deferred (see Spillover).
4. **QSA review of the equal/high-confidence mappings (D4)** — internally
   consistent, but a PCI assessor's eyes would raise confidence from agent-draft
   to reviewed.

## Spillover surfaced (NOT fixed here)

- **PCI CDE FrameworkScope ownership workflow** — authoring + approving a real
  per-tenant CDE predicate through the slice-018 draft→review→approved→activated
  lifecycle, with a CDE scope-reduction reporting surface. This slice proves the
  _intersection rule_ with a synthetic predicate; the _predicate-authoring UX_ is
  out of scope (slice 447 narrative explicitly defers it). The slice doc names
  this as a follow-on; no new slice doc was filed because the parent doc (447)
  and the canvas §10.3 phase-3 deferral already capture it. If the maintainer
  wants it tracked as its own grabbable slice, file it citing parent 447.
- **PCI SAQ workflow** — phase-3 deferred (canvas §10.3, P0-447-5).
- **Full PCI v4.0 requirement coverage** — follow-on (D3).
