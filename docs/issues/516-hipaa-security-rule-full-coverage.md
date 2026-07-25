# 516 — HIPAA Security Rule full coverage (incl. §164.314 + §164.316)

**Cluster:** Catalog
**Estimate:** M (1-2d)
**Type:** JUDGMENT (crosswalk-mapping accuracy is a subjective control call)
**Status:** `blocked` (depends on #481 — first thin HIPAA slice — merged first)

## Narrative

Slice 481 shipped the FIRST thin HIPAA Security Rule vertical slice: a curated
high-signal subset of 32 standards + implementation specifications spanning all
three safeguard categories (Administrative §164.308, Physical §164.310,
Technical §164.312), on the slice-438 generic crosswalk loader. The Security
Rule has ~50+ standards + implementation specifications and additionally
includes the **organizational requirements (§164.314)** and the **policies and
documentation requirements (§164.316)** that slice 481 deliberately left out of
the first subset. This slice completes coverage so a HIPAA covered entity or
business associate can run a full Security Rule self-assessment out of
security-atlas, not just the high-signal subset.

Like 481, this is **pure data + tests + a decisions log** — no loader change
(the slice-438 importer is framework-agnostic and already imports the HIPAA
crosswalk by `(framework_slug, framework_version)`). The work is (a) extending
`data/crosswalks/hipaa-security-rule.yaml` with the remaining standards/specs
including §164.314 + §164.316, each mapped to one SCF anchor via an explicit
STRM-typed edge, and (b) the mapping-accuracy decisions log for the new rows.

**Catalog, not workflow.** This remains catalog edges only. It does NOT add the
covered-entity workflow (that is slice 517). The §164.308 administrative
safeguards are added as catalog requirement nodes, NOT as process flows.

**Dependency note:** the slice-481 low-confidence rows (decisions log D7 —
164.310(b), 164.312(c)(1), 164.308(a)(8)) should be re-mapped against the full
SCF catalog the operator imports (slice 006), which has finer-grained anchors
(e.g. a dedicated data-integrity anchor) than the 53-anchor sample fixture. That
re-mapping is in scope here.

## Threat model

Inherits the slice-438 / slice-481 catalog-loader threat model verbatim: the
crosswalk loader is an operator/maintainer catalog-write path run via
`atlas-cli`; the read path serves shared, non-tenant-confidential reference data
through RLS-aware handlers. Adding more HIPAA rows adds no new surface beyond a
larger data file.

- **S — Spoofing.** No new endpoint; reuses the 438/481 import auth boundary.
- **T — Tampering.** More operator-supplied YAML rows become
  `framework_requirements` + `fw_to_scf_edges`. Mitigated by the existing 438
  validation: explicit `relationship_type` + `strength` per row, anchor-existence
  check, duplicate-code rejection, `(slug, version)` namespacing.
- **R — Repudiation.** Import emits the structured import-summary log; the
  decisions log records the new mapping rationale.
- **I — Information disclosure.** Catalog reference data only; the `/anchors`
  read does not widen the payload with tenant state. HIPAA's regulatory weight
  makes the slice-481 AC-5 no-tenant-field assertion mandatory for the new rows
  too — extend it to cover the §164.314/§164.316 read path.
- **D — Denial of service.** Larger crosswalk file, still an offline CLI import;
  the read path stays a bounded single-requirement fan-out.
- **E — Elevation of privilege.** Reuses the catalog-write boundary; no new role.

## Acceptance criteria

- [ ] **AC-1.** `data/crosswalks/hipaa-security-rule.yaml` extended to full
      Security Rule coverage incl. §164.314 (organizational) + §164.316
      (documentation), each requirement → one SCF anchor via an explicit
      STRM-typed edge; every anchor resolves against the seeded SCF catalog.
- [ ] **AC-2.** The slice-481 ≤0.65 low-confidence rows (D7) re-mapped against
      finer-grained anchors where available; decisions log records the lift.
- [ ] **AC-3.** Integration round-trip (real PG) + the AC-5 no-tenant-field
      assertion extended to the new requirement rows.
- [ ] **AC-4.** Existing SOC 2 / ISO / PCI / CSF / HIPAA-subset tests pass
      unmodified.
- [ ] **AC-5.** Decisions log + changelog.

## Dependencies

- **#481** (first thin HIPAA slice) — must merge first.
- **#438** (generic crosswalk loader) — merged.
- **#006** (SCF catalog importer) — merged.

## Anti-criteria (P0)

- Does NOT add a HIPAA-specific loader (use 438's generic importer).
- Does NOT create requirement → requirement edges (invariant #7).
- Does NOT ship the covered-entity workflow (that is slice 517).
- The `/anchors` read does NOT leak tenant-scoped data (HIPAA confidentiality bar).
