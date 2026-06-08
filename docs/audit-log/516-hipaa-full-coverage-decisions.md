# Slice 516 — HIPAA Security Rule full coverage: JUDGMENT decisions log

**Slice:** 516 — extend the HIPAA Security Rule crosswalk from slice-481's curated 31-row subset to FULL 45 CFR Part 164 Subpart C coverage, including §164.314 (Organizational Requirements) and §164.316 (Policies / Procedures / Documentation).
**Type:** JUDGMENT (crosswalk-mapping accuracy is a subjective control call).
**Parent dependency:** slice 481 (first thin HIPAA slice) — merged; built on slice 438 (generic crosswalk loader) and slice 006 (SCF catalog importer).
**Author:** agent (no human sign-off gate; per the JUDGMENT slice convention the agent makes the mapping calls and records them here for post-deployment review).

- detection_tier_actual: unit
- detection_tier_target: unit

> One class of bug was caught during the build, at the correct tier. The
> slice-481 unit test `TestLoad_ShippedHIPAACrosswalkParses` asserted the row
> count fell in `[25,35]` (the curated-subset range), and
> `TestLoad_HIPAASpansAllThreeSafeguardCategories` had a `default` arm that
> `t.Fatalf`-ed on any code outside §164.308/310/312 — so adding the §164.314 +
> §164.316 rows (and growing the file to 67) would have failed both at the
> **unit** tier. That is exactly where a row-count/section-shape claim should be
> caught (not integration, not production). Corrected in the SAME PR by
> retargeting the count assertion to full coverage, renaming the section test to
> `TestLoad_HIPAASpansAllSecurityRuleSections` (now allowing §164.314 + §164.316),
> and adding `TestLoad_HIPAAFullCoverage` (the exact 67-row count + per-section
> distribution). The import-resolution proofs (`actual == target == integration`)
> passed against a real Postgres on the first run after the data was complete.
> Neither is a coverage-tier gap nor an integration-enrolment gap — the package
> was already enrolled (slice 007/481).

This log records the subjective build-time calls for the 36 newly-added
requirement → SCF-anchor edges (31 → 67), the re-mapping of the slice-481
low-confidence (D7) rows, the anchor-palette constraint that bounded every
mapping, and the 21 residual low-confidence flags. It is the durable artifact a
maintainer (or an auditor scanning the catalog) reads to understand why each new
edge is what it is.

---

## D1 — Loaded through the slice-438 generic loader (no HIPAA-specific code)

Full coverage ships purely as data
(`data/crosswalks/hipaa-security-rule.yaml`) plus the updated/added unit tests.
It imports through `internal/api/soc2import` exactly as before: the generic
`requirement_code:` YAML key, the same `soc2import.Load` + `soc2import.Import`
entry points, the same anchor-existence + STRM + strength validation. **No
loader code was added or changed** (anti-criterion P0). The extension is 36 more
`requirements` rows + 36 more `mappings` rows on the existing shape. This mirrors
slice 514's CSF extension verbatim.

**Confidence:** HIGH. Verified by the integration suite, which imports all 67
requirements + 67 edges and proves idempotency; the existing SOC 2 / ISO / PCI /
CSF suites pass unmodified.

## D2 — Full coverage = 67 standards + implementation specifications (AC-1)

The HIPAA Security Rule (45 CFR Part 164, Subpart C) is structured as standards,
each with zero or more Required (R) / Addressable (A) implementation
specifications, across five sections. The file now covers all of them at the
standard + implementation-specification grain:

| Section   | Title                                         | Count  |
| --------- | --------------------------------------------- | ------ |
| §164.308  | Administrative safeguards                     | 30     |
| §164.310  | Physical safeguards                           | 12     |
| §164.312  | Technical safeguards                          | 12     |
| §164.314  | Organizational requirements (NEW)             | 8      |
| §164.316  | Policies, procedures, and documentation (NEW) | 5      |
| **Total** |                                               | **67** |

Slice 481 shipped 31 of these (all three safeguard categories, no §164.314 /
§164.316). Slice 516 adds the remaining 36. The grain decision (count each
standard AND each implementation specification as its own catalog node, not just
the standards) matches slice 481's existing grain — e.g. §164.308(a)(1)(i) is the
standard and §164.308(a)(1)(ii)(A)..(D) are its four implementation
specifications, each a separate row. `TestLoad_HIPAAFullCoverage` pins the exact
67 + the per-section distribution so a future row add/drop fails loudly.

**Required-vs-Addressable** (45 CFR §164.306(d)) stays a YAML per-row comment fact
only — NO structured loader field (slice 481 D6, anti-criterion preserved). The
covered-entity required-vs-addressable DECISION workflow remains the deferred
slice 517.

## D3 — Five-framework shared-anchor proof preserved (AC-4)

The load-bearing invariant-#1 proof is unchanged: HIPAA §164.312(a)(1) → IAC-01
is still in the file, and `TestHIPAAImport_SharedAnchorSatisfiesFiveFrameworks_Invariant1`
still proves the single IAC-01 anchor row satisfies SOC 2 CC6.1, ISO A.5.15, PCI
8.2.1, CSF PR.AA-01, AND HIPAA §164.312(a)(1) through one anchor row — no
per-framework duplicate control. Every new edge is requirement → SCF anchor,
never requirement → requirement (invariant #7); the new §164.314 organizational
rows do NOT create HIPAA-to-HIPAA edges (they resolve to TPM-\* anchors).

## D4 — Anchor-palette constraint (514's hard lesson, applied)

The integration suite seeds only the 53-anchor sample fixture
(`migrations/fixtures/scf-sample.json`). EVERY HIPAA edge must target an anchor
that EXISTS in that fixture or the import rolls the whole transaction back
(`TestHIPAAImport_RejectsEdgeToNonexistentAnchor` proves this). All 30 distinct
anchors referenced by the 67 edges resolve against the fixture — verified by a
grep/script reporting zero missing. Where the _ideal_ finer anchor lives only in
the operator's full SCF catalog (slice 006) and not the sample fixture, the row
maps to the closest _covering_ anchor in the palette and the residual gap is
documented honestly here rather than inflating strength or referencing an anchor
the import cannot resolve. This is the same honest, palette-bound call slice 514
made for CSF.

## D5 — Slice-481 D7 low-confidence rows re-mapped / re-confirmed (AC-2)

Slice 481's D7 flagged three ≤0.65 rows for re-mapping against finer-grained
anchors. The narrative asked these be re-mapped against the operator's full SCF
catalog (which has finer anchors than the sample fixture). The palette constraint
(D4) binds the _test fixture_ to 53 anchors, so the call is per-row:

| Row                         | 481 mapping                               | 516 mapping                                      | Outcome                                                                                                                                                                                                                                                                                                                                               |
| --------------------------- | ----------------------------------------- | ------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| §164.308(a)(8) Evaluation   | CPL-03 (Internal Audit) @ 0.65 intersects | **CPL-01 (Compliance Management) @ 0.75 subset** | **LIFTED.** "Periodic technical AND nontechnical evaluation of how well policies/procedures meet the Security Rule" is the self-assessment facet of Compliance Management — a closer _covering_ anchor than Internal Audit, whose scope is narrower than the nontechnical-evaluation requirement. A genuine improvement available WITHIN the palette. |
| §164.312(c)(1) Integrity    | DCH-01 @ 0.65 intersects                  | DCH-01 @ 0.65 intersects (re-confirmed)          | **HELD — palette-bound.** A dedicated data-integrity anchor is STILL absent from the 53-anchor sample fixture; DCH-01 remains the closest covering anchor. The rationale now explicitly flags this row to be re-pointed at the operator's finer data-integrity anchor once the full SCF catalog (slice 006) is imported. Honest non-inflation.        |
| §164.310(b) Workstation Use | PES-04 @ 0.6 intersects                   | PES-04 @ 0.6 intersects (re-confirmed)           | **HELD — palette-bound.** No workstation-use-policy anchor finer than Physical Access Control exists in the sample fixture; the use-policy facet genuinely straddles physical access and config policy. No palette improvement available.                                                                                                             |

So 1 of 3 D7 rows genuinely lifts within the palette; the other 2 are
re-confirmed with the residual gap documented (not silently left, not inflated).

## D6 — Regulatory-weight confidentiality assertion preserved (AC-3 / AC-5)

HIPAA governs ePHI, so the catalog `/anchors` read MUST carry NO tenant-scoped
field. `TestHIPAAImport_AnchorsReadCarriesNoTenantScopedData` (column-exhaustive
allow-list + banned-substring check) stays green and unmodified — no new
tenant-scoped column was added to the read. The new §164.314 / §164.316 rows flow
through the identical catalog read projection (SCF anchor identity + STRM edge
facts only), so the assertion covers them by construction.

## D7 — Section-to-SCF-family mapping rationale (the new sections)

- **§164.314 Organizational requirements → Third-Party Management (TPM-\*).** The
  organizational requirements are contracting / satisfactory-assurance
  obligations (business-associate contracts, subcontractor flow-down,
  group-health-plan / plan-sponsor arrangements). In the SCF palette these are
  expressed as third-party management (TPM-01) and third-party risk assessment
  (TPM-04). The group-health-plan / plan-sponsor rows are the weakest fit
  (0.55) because that relationship is a HIPAA-specific arrangement with no
  dedicated anchor; they map to TPM-01 as the closest covering anchor.
- **§164.316 Policies/Procedures/Documentation → Governance (GOV-\*) + Compliance
  Management (CPL-\*) + Data Retention (DCH-\*).** §164.316(a) policies/procedures
  → GOV-01; §164.316(b)(1) documentation → CPL-01; §164.316(b)(2)(i) six-year
  retention → DCH-03 (data retention); availability → CPL-01; updates → GOV-01.

## D8 — Licensing posture unchanged (community_draft discipline retained)

HIPAA is a US-government public-domain work (45 CFR Part 164), but the
`community_draft` source-attribution discipline still applies (slice 481 D2). The
new rows reference CFR section identifiers + short factual titles paired with
original agent-authored one-line `body` descriptions; no verbatim regulatory text
beyond identifiers/short titles is copied. `source_attribution: community_draft`
is unchanged at the crosswalk level.

---

## Residual low-confidence flags (21 rows ≤ 0.65) — spot-check priority

Each is mapped to the closest _covering_ anchor in the 53-anchor sample palette;
where a finer anchor exists only in the operator's full SCF catalog the rationale
says so. These are the rows a maintainer should re-point first once the full
catalog is imported.

| Requirement                                 | Anchor | Strength | Why low                                                  |
| ------------------------------------------- | ------ | -------- | -------------------------------------------------------- |
| §164.308(a)(1)(ii)(C) Sanction Policy       | HRS-01 | 0.60     | No dedicated sanctions anchor; HR-security is broader    |
| §164.308(a)(4)(ii)(A) Isolate Clearinghouse | DCH-01 | 0.55     | No clearinghouse-isolation anchor                        |
| §164.308(a)(5)(ii)(D) Password Management   | IAC-01 | 0.60     | No dedicated authenticator/password anchor               |
| §164.308(a)(7)(ii)(E) Criticality Analysis  | BCD-02 | 0.60     | No business-impact-analysis anchor                       |
| §164.310(a)(2)(i) Contingency Operations    | BCD-02 | 0.60     | Straddles continuity + physical access                   |
| §164.310(a)(2)(iv) Maintenance Records      | PES-04 | 0.55     | No physical-maintenance-records anchor                   |
| §164.310(b) Workstation Use                 | PES-04 | 0.60     | 481 D7 — no use-policy anchor finer than physical access |
| §164.310(d)(2)(iii) Accountability          | AST-01 | 0.65     | Chain-of-custody narrower than asset-mgmt policy         |
| §164.312(a)(2)(ii) Emergency Access         | IAC-21 | 0.60     | No break-glass anchor; privileged-acct-mgmt closest      |
| §164.312(a)(2)(iii) Automatic Logoff        | IAC-01 | 0.60     | No session-management anchor                             |
| §164.312(c)(1) Integrity                    | DCH-01 | 0.65     | 481 D7 — no data-integrity anchor in fixture             |
| §164.312(c)(2) Authenticate ePHI            | DCH-01 | 0.60     | No cryptographic-integrity anchor                        |
| §164.312(e)(2)(i) Transmission Integrity    | NET-04 | 0.60     | No transmission-integrity anchor                         |
| §164.314(a)(2)(ii) Other Arrangements       | TPM-01 | 0.65     | Alternative-instrument facet narrower                    |
| §164.314(b)(1) Group Health Plans           | TPM-01 | 0.55     | HIPAA-specific plan-sponsor arrangement, no anchor       |
| §164.314(b)(2)(i) Plan Safeguards           | TPM-01 | 0.55     | Plan-sponsor obligation, no anchor                       |
| §164.314(b)(2)(ii) Adequate Separation      | TPM-01 | 0.55     | Plan/sponsor segregation, no anchor                      |
| §164.314(b)(2)(iii) Agents Safeguard        | TPM-04 | 0.60     | Flow-down-to-agents facet                                |
| §164.316(b)(2)(i) Time Limit                | DCH-03 | 0.60     | Compliance-doc-retention narrower than data-retention    |
| §164.316(b)(2)(ii) Availability             | CPL-01 | 0.60     | Document-availability narrower than compliance-mgmt      |
| §164.316(b)(2)(iii) Updates                 | GOV-01 | 0.60     | Periodic-policy-review narrower than governance          |

STRM distribution across the 67 edges: 16 `equal`, 24 `subset_of`, 27
`intersects_with`. Strength range [0.55, 0.90].

## Out of scope (anti-criteria honored)

- NO HIPAA-specific loader (slice-438 generic importer only).
- NO requirement → requirement edges (invariant #7).
- NO covered-entity workflow / BAA tracking / required-vs-addressable decision
  flow / breach risk-assessment (§164.402) / HIPAA FrameworkScope ePHI example —
  all the deferred phase-3 slice 517 (canvas §10.3). This is CATALOG edges only.
- NO tenant-scoped data added to the `/anchors` read (HIPAA confidentiality bar).
