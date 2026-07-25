# 514 — NIST CSF 2.0 full Subcategory coverage

**Cluster:** Catalog
**Estimate:** M (1-2d)
**Type:** JUDGMENT (crosswalk-mapping accuracy is a subjective control call)
**Status:** `blocked` (depends on #480 — first thin CSF slice — merged first)

## Narrative

Slice 480 shipped the FIRST thin NIST CSF 2.0 vertical slice: a curated
high-signal subset of 35 Subcategories spanning all six Functions, on the
slice-438 generic crosswalk loader. CSF 2.0 has ~106 Subcategories; this slice
completes coverage of the remaining ~71 so a SaaS operator can run a full CSF
2.0 self-assessment out of security-atlas, not just the high-signal subset.

Like 480, this is **pure data + tests + a decisions log** — no loader change
(the slice-438 importer is framework-agnostic and already imports the CSF
crosswalk by `(framework_slug, framework_version)`). The work is (a) extending
`data/crosswalks/nist-csf-2.0.yaml` with the remaining Subcategories, each
mapped to one SCF anchor via an explicit STRM-typed edge, and (b) the
mapping-accuracy decisions log for the new rows.

**Dependency note:** several slice-480 low-confidence rows (decisions log D7 —
GV.OV-01, RC.CO-03, RS.AN-03, RC.RP-04, DE.AE-02) should be re-mapped against
the full SCF catalog the operator imports (slice 006), which has finer-grained
anchors than the 53-anchor sample fixture. That re-mapping is in scope here.

## Threat model

Inherits the slice-438 / slice-480 catalog-loader threat model verbatim: the
crosswalk loader is an operator/maintainer catalog-write path run via
`atlas-cli`; the read path serves shared, non-tenant-confidential reference
data through RLS-aware handlers. Adding more CSF rows adds no new surface beyond
a larger data file.

- **S — Spoofing.** No new endpoint; reuses the 438/480 import auth boundary.
- **T — Tampering.** More operator-supplied YAML rows become
  `framework_requirements` + `fw_to_scf_edges`. Mitigated by the existing 438
  validation: explicit `relationship_type` + `strength` per row, anchor-existence
  check, duplicate-code rejection, `(slug, version)` namespacing.
- **R — Repudiation.** Import emits the structured import-summary log; the
  decisions log records the new mapping rationale.
- **I — Information disclosure.** Catalog reference data only; the `/anchors`
  read does not widen the payload with tenant state (slice-104 `?include=state`
  opt-in unchanged).
- **D — Denial of service.** Larger crosswalk file, still an offline CLI import;
  the read path stays a bounded single-requirement fan-out.
- **E — Elevation of privilege.** Reuses the catalog-write boundary; no new role.

## Acceptance criteria

- [ ] **AC-1.** `data/crosswalks/nist-csf-2.0.yaml` is extended to full CSF 2.0
      Subcategory coverage (~106), each Subcategory mapped to one SCF anchor via
      an explicit STRM-typed edge, on the slice-438 generic loader (no
      CSF-specific loader code).
- [ ] **AC-2.** The slice-480 low-confidence rows (decisions log D7) are
      re-mapped against the full SCF catalog where finer-grained anchors exist;
      strengths and STRM types updated accordingly.
- [ ] **AC-3.** Import is idempotent; re-import of the extended file is a no-op.
- [ ] **AC-4.** The slice-480 four-framework graph proof + GOVERN no-analog
      proof still pass unmodified (the extension does not regress the thin slice).
- [ ] **AC-5.** Pure-Go unit branch asserts the full-coverage row count and
      retained all-six-Function coverage.
- [ ] **AC-6.** Decisions log (`docs/audit-log/514-*.md`) records the new
      mapping calls + confidence + the resolved D7 revisit items.
- [ ] **AC-7.** Changelog entry.

## Anti-criteria (P0 — block merge)

- **P0-514-1.** Does NOT add a CSF-specific loader.
- **P0-514-2.** Does NOT create requirement → requirement edges (invariant #7).
- **P0-514-3.** Does NOT duplicate controls per framework (invariant #1).
- **P0-514-4.** Does NOT import an edge to a non-existent SCF anchor.
- **P0-514-5.** Does NOT copy verbatim copyrighted text (CSF 2.0 is public
  domain, but the `community_draft` source-attribution discipline applies).

## Dependencies

- **#480** (first thin CSF 2.0 slice) — parent; merge first.
- **#006** (SCF catalog importer) — CSF edges target SCF anchors.
