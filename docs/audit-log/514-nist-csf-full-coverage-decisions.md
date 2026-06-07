# Slice 514 — NIST CSF 2.0 full Subcategory coverage: JUDGMENT decisions log

**Slice:** 514 — extend the NIST CSF 2.0 crosswalk from slice-480's curated 35-Subcategory subset to FULL coverage (106 Subcategories).
**Type:** JUDGMENT (crosswalk-mapping accuracy is a subjective control call).
**Parent dependency:** slice 480 (first thin CSF slice) — merged; built on slice 438 (generic crosswalk loader) and slice 447 (PCI, 3rd framework).
**Author:** agent (no human sign-off gate; per the JUDGMENT slice convention the agent makes the mapping calls and records them here for post-deployment review).

- detection_tier_actual: integration
- detection_tier_target: integration

> One class of bug was caught during the build, at the correct tier. The
> slice-480 unit test `TestLoad_ShippedCSFCrosswalkParses` asserted the row
> count fell in `[30,40]` (the curated-subset range). Expanding the data file
> to 106 rows would have failed that assertion — a stale count assertion, not a
> data bug. It surfaced at the **unit** tier (the pure-Go loader test) exactly
> where a row-count claim should be caught (not integration, not production),
> and was corrected in the SAME PR by retargeting the assertion to full
> coverage and adding `TestLoad_CSFFullSubcategoryCoverage` (the exact 106-row
> count plus the per-Function distribution). `actual == target == integration`
> for the import-resolution proofs (every anchor must resolve against the seeded
> catalog); the count-assertion correction was a unit-tier catch. Neither is a
> coverage-tier gap nor an integration-enrolment gap — the package was already
> enrolled (slice 480).

This log records the subjective build-time calls for the 71 newly-added
Subcategory → SCF-anchor edges, the re-mapping of the five slice-480
low-confidence (D7) rows, the anchor-palette constraint that bounded every
mapping, and the residual low-confidence flags. It is the durable artifact a
maintainer (or an auditor scanning the catalog) reads to understand why each new
edge is what it is.

---

## D1 — Loaded through the slice-438 generic loader (no CSF-specific code)

Full coverage ships purely as data (`data/crosswalks/nist-csf-2.0.yaml`) plus
the updated/added unit tests. It imports through `internal/api/soc2import`
exactly as before: the generic `requirement_code:` YAML key, the same
`soc2import.Load` + `soc2import.Import` entry points, the same
anchor-existence + STRM + strength validation. **No loader code was added or
changed** (P0-514-1). The extension is 71 more `requirements` rows + 71 more
`mappings` rows on the existing shape.

**Confidence:** HIGH. Verified by the integration suite, which imports all 106
requirements + 106 edges and proves idempotency; the existing SOC 2 / ISO / PCI
/ HIPAA suites pass unmodified.

## D2 — Full coverage = all 106 Subcategories (AC-1, AC-5)

NIST CSF 2.0 (NIST CSWP 29, Feb 2024) defines 106 Subcategories across six
Functions and 22 Categories. The file now covers all of them:

| Function  | Categories (Subcategory count)                         | Total   |
| --------- | ------------------------------------------------------ | ------- |
| GOVERN    | GV.OC(5) GV.RM(7) GV.RR(4) GV.PO(2) GV.OV(3) GV.SC(10) | 31      |
| IDENTIFY  | ID.AM(7) ID.RA(10) ID.IM(4)                            | 21      |
| PROTECT   | PR.AA(6) PR.AT(2) PR.DS(4) PR.PS(6) PR.IR(4)           | 22      |
| DETECT    | DE.CM(5) DE.AE(6)                                      | 11      |
| RESPOND   | RS.MA(5) RS.AN(4) RS.CO(2) RS.MI(2)                    | 13      |
| RECOVER   | RC.RP(6) RC.CO(2)                                      | 8       |
| **Total** |                                                        | **106** |

(ID.AM has no `-06`; PR.DS uses `-01/-02/-10/-11`; DE.CM uses `-01/-02/-03/-06/-09`;
DE.AE uses `-02/-03/-04/-06/-07/-08`; RS.AN uses `-03/-06/-07/-08` — these gaps
are CSF 2.0's own numbering, not omissions.) The exact count + per-Function
distribution are asserted by `TestLoad_CSFFullSubcategoryCoverage` (AC-5).

**Confidence:** HIGH on the enumeration (matched against the CSF 2.0 Core).

## D3 — The binding anchor-palette constraint (P0-514-4 — load-bearing)

Every CSF edge MUST resolve against the SCF catalog the import seeds. The
integration suite seeds the **53-anchor sample fixture**
(`migrations/fixtures/scf-sample.json` via `scfseed.EnsureSCFCatalog`), so
**every one of the 106 anchors referenced is one of those 53** — a mapping to a
non-existent anchor rolls back the whole import (proven by
`TestCSFImport_RejectsEdgeToNonexistentAnchor`).

This bounded every mapping call and is the source of most of the residual
low-confidence flags (D6): the slice-480 D7 revisit note anticipated that "the
real SCF catalog the operator imports (slice 006) likely has finer-grained
anchors" for strategy-governance, recovery-communication, and forensic
analysis. **Those finer-grained anchors do not exist in the 53-anchor test
fixture**, so this slice maps to the closest _covering_ anchor within the
palette and documents the residual gap, rather than referencing an anchor the
import cannot resolve. When the operator imports the full SCF catalog, these
rows are the top of the re-map list (carried forward below).

**Confidence:** HIGH on the constraint; the low-confidence rows are flagged.

## D4 — STRM type + strength distribution (the JUDGMENT surface)

Across all 106 rows:

- `equal` (13): the SCF anchor and the CSF outcome describe the same control
  concept (e.g. PR.AA-01 → IAC-01, PR.AA-06 → PES-04, RS.MA-01 → IRO-04).
- `subset_of` (52): the SCF anchor fully covers the CSF outcome and is broader
  (e.g. GV.OC-03 → CPL-01, ID.RA-08 → VPM-04, PR.PS-04 → AAA-01).
- `intersects_with` (41): partial overlap with an explicit residual gap.

Strengths run [0.55, 0.9]. CSF Subcategories are **outcome-oriented** (a result,
not a control mechanism), so many map `subset_of` a mechanism-specific SCF
anchor (the slice-480 D4 observation, now at scale). The new IRO-04-heavy
RESPOND block reflects the sample fixture having a single broad
Incident-Response-Plan anchor: the granular RS.MA / RS.AN / RS.MI Subcategories
(triage, categorize, escalate, contain, eradicate, magnitude) all `subset_of`
IRO-04 because the fixture has no per-phase IR anchor. Likewise the RECOVER
block leans on BCD-02 / BCD-11 (Business Continuity Plan / Backup Testing) for
the same reason.

**New equal-strength calls worth noting:**

- **PR.AA-06 → PES-04** (`equal`/0.85) — physical access management maps cleanly
  to SCF Physical Access Control.
- **PR.DS-11 → BCD-09** (`equal`/0.85) — data backups map directly to SCF Backups.
- **RC.RP-03 → BCD-11** retained from 480 (`equal`/0.85) — backup-integrity
  verification.

**Confidence:** HIGH on the STRM _type_ per row; MEDIUM on the _strength
scalars_ (0.05 gradations are calibrated by feel, consistent with the
slice-438/447/480/481 rubric, not by measurement).

## D5 — Re-mapping the slice-480 D7 low-confidence rows (AC-2)

Slice 480's D7 flagged five ≤0.65 rows for re-mapping "against the full SCF
catalog the operator imports." Because the **test fixture** is still the
53-anchor sample (D3), no finer-grained anchor exists there to lift these to
`subset_of`. The resolution for each (all stay within the 53-anchor palette):

| 480 D7 row | 480 mapping   | 514 resolution                                                                                                                                                                                                                                                                         |
| ---------- | ------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| GV.OV-01   | CPL-03 / 0.6  | **Retained** CPL-03 / 0.6 — Internal Audit Function is the closest strategy-outcome-review anchor in the palette; a strategy-governance anchor remains absent. Documented gap.                                                                                                         |
| RC.CO-03   | BCD-02 / 0.6  | **Retained** BCD-02 / 0.6 — no recovery-communications anchor in the palette; closest covering anchor. RC.CO-04 (new) similarly maps IRO-09 / 0.6 (external-comms facet).                                                                                                              |
| RS.AN-03   | IRO-04 / 0.65 | **Retained** IRO-04 / 0.65 — no forensic-analysis anchor in the palette. The new RS.AN-06/07 (investigation-record integrity, data preservation) map AAA-01 / AAA-10 (audit/log-retention), the nearest integrity-preservation anchors, also flagged.                                  |
| RC.RP-04   | BCD-02 / 0.65 | **Retained** BCD-02 / 0.65 — post-incident-norms is a thin facet of the BCP anchor; no finer anchor available.                                                                                                                                                                         |
| DE.AE-02   | MON-08 / 0.7  | **Retained** MON-08 / 0.7 — the new DE.AE-03 (correlation) maps AAA-12 (Audit Log Review, multi-source analysis), a _better_ fit for the correlation facet than MON-08, lifting that specific outcome. DE.AE-07 (threat-intel integration) maps MON-08 / 0.6 (no threat-intel anchor). |

The honest call: the **re-map is anchor-palette-bound**, not strength-inflated.
Inventing a higher strength without a better anchor would misrepresent the
coverage. The five rows are re-confirmed as the closest covering anchor and the
gap is documented; the genuine lift waits on the operator importing the full
SCF catalog (carried forward, item 1 below). This is the defensible JUDGMENT
call for a test fixture that does not yet carry the finer-grained anchors.

**Confidence:** HIGH on the re-confirmation rationale; LOW on the strengths (by
design — these are the flagged rows).

## D6 — Residual low-confidence mappings flagged for spot-check (28 rows ≤0.65)

The full-coverage extension surfaces more outcome-oriented Subcategories whose
best fit in the 53-anchor palette is an `intersects_with` with an explicit gap.
The lowest (0.55–0.60) are the spot-check priority:

| CSF Subcategory                | Anchor          | Strength  | Gap (palette limitation)                                                                                     |
| ------------------------------ | --------------- | --------- | ------------------------------------------------------------------------------------------------------------ |
| PR.DS-10                       | CRY-01          | 0.55      | Data-**in-use** protection (confidential compute) has no fixture anchor.                                     |
| ID.RA-02                       | MON-08          | 0.60      | Cyber-threat-**intelligence** ingestion has no dedicated anchor.                                             |
| DE.AE-07                       | MON-08          | 0.60      | Threat-intel integration into analysis — same gap.                                                           |
| PR.IR-02 / DE.CM-02            | PES-04          | 0.60      | **Environmental**-threat protection / physical-environment monitoring — only Physical Access Control exists. |
| DE.CM-06                       | TPM-04          | 0.60      | Continuous **external-provider** activity monitoring — no provider-monitoring anchor.                        |
| RS.AN-07                       | AAA-10          | 0.60      | Forensic data **preservation** / provenance — no chain-of-custody anchor.                                    |
| GV.OC-05 / PR.IR-04 / RC.CO-03 | BCD-02          | 0.60      | Org-dependency analysis / capacity planning / recovery-comms — thin facets of the broad BCP anchor.          |
| GV.RM-07                       | RSK-01          | 0.60      | **Upside** (opportunity) risk has no fixture anchor.                                                         |
| ID.IM-02/03                    | VPM-04 / MON-01 | 0.60      | Improvement-from-tests / improvement-from-operations — no continuous-improvement anchor.                     |
| GV.OV-01/03, ID.IM-01          | CPL-03          | 0.60–0.65 | Strategy-outcome review / performance evaluation — Internal Audit is the nearest.                            |
| RC.CO-04                       | IRO-09          | 0.60      | Approved **public** recovery messaging — Incident Reporting is the nearest external-comms anchor.            |

A further set of 0.65 rows (GV.OC-04, GV.RR-03, GV.OV-02, GV.SC-08, GV.SC-10,
ID.AM-03, PR.AA-04, DE.AE-04, RS.AN-03, RS.AN-06, RC.RP-04) carry a smaller but
explicit residual gap. **28 rows total at ≤0.65.** All would likely lift to
`subset_of` once the operator's full SCF catalog provides finer-grained anchors
(threat-intelligence, environmental controls, confidential-compute, forensic
preservation, continuous-improvement, recovery-communications, supply-chain
incident-coordination, upside-risk).

**Confidence:** LOW (by design — these are the flagged rows).

## D7 — Invariants preserved (P0-514-2, P0-514-3)

- **Invariant #7** (P0-514-2): every one of the 106 edges targets an SCF anchor
  (`SCF:XXX-NN`), never another framework's requirement. Asserted by
  `TestLoad_CSFEveryMappingTargetsAnSCFAnchor` and proven at the DB layer by the
  GOVERN-no-analog test (31 GOVERN edges all traverse SCF anchors, zero
  requirement→requirement edges).
- **Invariant #1** (P0-514-3): no per-framework duplicate controls were created.
  The new PR.AA-04 row additionally routes through the shared IAC-01 anchor;
  `TestCSFImport_SharedAnchorSatisfiesFourFrameworks_Invariant1` confirms IAC-01
  remains a single `scf_anchors` row satisfying SOC 2 + ISO + PCI + CSF.

**Confidence:** HIGH.

## D8 — Licensing posture unchanged (P0-514-5)

CSF 2.0 is a U.S.-government public-domain work, but the file retains the
438/467/480 source-attribution discipline: identifiers + short factual titles +
**original agent-authored** one-line descriptions; no reproduction of the CSF
Informative References or Implementation Examples; `source_attribution:
community_draft` at the crosswalk level. The new 71 `body` strings are
original one-line summaries, not copied CSF text.

**Confidence:** HIGH.

---

## Revisit once in use

1. **Re-map the 28 ≤0.65 rows (D5, D6)** once the operator imports the full SCF
   catalog (not the 53-anchor sample). Finer-grained anchors for
   threat-intelligence (ID.RA-02, DE.AE-07), environmental controls (PR.IR-02,
   DE.CM-02), confidential compute / data-in-use (PR.DS-10), forensic
   preservation (RS.AN-06/07), continuous improvement (ID.IM-01/02/03),
   recovery communications (RC.CO-03/04), and upside risk (GV.RM-07) very likely
   exist and would raise these to `subset_of`. Flip `source_attribution` to
   `scf_official` on rows that match a publisher-official mapping.
2. **Re-calibrate strength scalars (D4)** once a publisher-official NIST CSF 2.0
   → SCF crosswalk is available.
3. **CSF Profile / Tier assessment** — the maturity construct (Tiers 1–4 +
   Current/Target Profiles) remains OUT of catalog scope; tracked as slice 515.

## Confidence summary

| Decision                                     | Confidence                     |
| -------------------------------------------- | ------------------------------ |
| D1 — generic loader, no CSF code             | HIGH                           |
| D2 — full 106-Subcategory enumeration        | HIGH                           |
| D3 — anchor-palette constraint               | HIGH                           |
| D4 — STRM type + strength                    | HIGH type / MEDIUM strength    |
| D5 — slice-480 D7 re-map                     | HIGH rationale / LOW strengths |
| D6 — residual low-confidence flags (28 rows) | LOW (by design)                |
| D7 — invariants #1 + #7 preserved            | HIGH                           |
| D8 — licensing posture                       | HIGH                           |
