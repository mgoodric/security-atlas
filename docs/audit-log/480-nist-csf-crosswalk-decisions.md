# Slice 480 — NIST CSF 2.0 crosswalk: JUDGMENT decisions log

**Slice:** 480 — NIST CSF 2.0 crosswalk loader (4th framework via the generic importer)
**Type:** JUDGMENT (crosswalk-mapping accuracy is a subjective control call)
**Parent dependency:** slice 438 (generic crosswalk loader) — merged; slice 447 (PCI, 3rd framework) — merged.
**Author:** agent (no human sign-off gate; per the JUDGMENT slice convention the
agent makes the mapping calls and records them here for post-deployment review).

- detection_tier_actual: integration
- detection_tier_target: integration

> One bug surfaced during the build and was caught at the correct tier. The
> first draft of the AC-5 GOVERN test asserted "zero SOC 2 requirements map to
> GOV-04", which is FALSE: SOC 2's Control-Environment criteria (CC1.x) legitimately
> map to the SCF governance anchors — that is invariant #1 working as designed.
> The over-strict assertion was caught by the integration tier (`go test
-tags=integration`), which is exactly where a graph-traversal claim about real
> rows should be caught (not unit, not production). The assertion was corrected to
> the defensible no-analog property (CSF GOVERN reaches governance coverage ONLY
> through SCF anchors — invariant #7 — and SOC 2 has no GOVERN _Function_; it does
> NOT claim the governance anchor is SOC-2-untouched). `actual == target == integration`,
> so this is neither a coverage-tier gap nor an integration-enrolment gap.

This log records the subjective build-time calls: the curated subset, the
per-Subcategory CSF → SCF anchor mappings, the STRM type and strength per row,
the low-confidence flags, and the GOVERN no-analog rationale. It is the durable
artifact a maintainer (or an auditor scanning the catalog) reads to understand
why each edge is what it is.

---

## D1 — Loaded through the slice-438 generic loader (no CSF-specific code)

NIST CSF 2.0 ships purely as data (`data/crosswalks/nist-csf-2.0.yaml`) plus
tests. It imports through `internal/api/soc2import` exactly as ISO 27001 and PCI
DSS do: the generic `requirement_code:` YAML key, the same `soc2import.Load` +
`soc2import.Import` entry points, the same anchor-existence + STRM + strength
validation. **No loader code was added or changed** (P0-480-1). This is the
whole point of slice 438 — the fourth framework proves the loader generalizes
yet again.

**Confidence:** HIGH. Verified by `TestLoad_ShippedCSFCrosswalkParses` and the
integration suite, which call the identical functions the ISO / PCI / SOC 2
suites do; the existing SOC 2 / ISO / PCI suites pass unmodified (AC-8).

## D2 — Licensing posture: identifiers + titles + original descriptions only

NIST CSF 2.0 (NIST CSWP 29, Feb 2024) is a work of the U.S. federal government
and is in the **public domain** — unlike ISO 27001 (copyrighted) and PCI DSS
(copyrighted). The licensing constraint is therefore weaker here. The slice
nonetheless follows the 438/467 source-attribution discipline for consistency
and for the operator's mental model:

- Subcategory **identifiers** (e.g. `PR.AA-01`, `GV.RR-02`) — factual references;
- short factual **titles**;
- an **original agent-authored** one-line `body` for each Subcategory (the CSF
  Implementation Examples and Informative References are NOT reproduced).

`source_attribution: community_draft` on every row (P0-480-8) — these are the
agent's independent draft mappings, not an NIST- or SCF-published official
crosswalk. SCF anchors are imported separately by the operator (slice 006);
this file ships only the CSF → SCF **edge** data, so no pre-built SCF catalog is
bundled (P0-480-7).

**Confidence:** HIGH.

## D3 — Curated subset: 35 Subcategories across all six Functions

CSF 2.0 has ~106 Subcategories. The slice narrative binds a curated high-signal
subset (~30-40) spanning **all six Functions**. The shipped subset is 35
Subcategories:

| Function | Subcategories shipped                                                                    | Count |
| -------- | ---------------------------------------------------------------------------------------- | ----- |
| GOVERN   | GV.OC-01, GV.RR-01, GV.RR-02, GV.PO-01, GV.RM-01, GV.SC-01, GV.SC-04, GV.OV-01           | 8     |
| IDENTIFY | ID.AM-01, ID.AM-02, ID.AM-07, ID.RA-01, ID.RA-04, ID.RA-05                               | 6     |
| PROTECT  | PR.AA-01, PR.AA-03, PR.AA-05, PR.AT-01, PR.DS-01, PR.DS-02, PR.PS-01, PR.PS-06, PR.IR-01 | 9     |
| DETECT   | DE.CM-01, DE.CM-03, DE.CM-09, DE.AE-02                                                   | 4     |
| RESPOND  | RS.MA-01, RS.MA-02, RS.CO-02, RS.AN-03                                                   | 4     |
| RECOVER  | RC.RP-01, RC.RP-03, RC.RP-04, RC.CO-03                                                   | 4     |

Selection bias is deliberate: (a) the **overlap zone** with SOC 2 / ISO / PCI
(identity, encryption, monitoring, incident response) so shared SCF anchors
demonstrate invariant #1 at four frameworks; and (b) **GOVERN is over-weighted**
(8 of 35) because it is CSF 2.0's headline addition and the no-analog
differentiator. GOVERN's 8 Subcategories reach the governance, risk, and
third-party SCF families (GOV-01, GOV-04, RSK-01, TPM-01, TPM-04, CPL-03).

**Confidence:** HIGH on Function coverage (asserted by
`TestLoad_CSFSpansAllSixFunctions`); MEDIUM on which specific Subcategories
within each Function are "highest signal" — a maintainer running real CSF
self-assessments will know which Subcategories customers actually ask about.

## D4 — STRM type + strength per mapping

Every edge carries an explicit `relationship_type` + `strength` (no silent
`equal/1.0`; the loader rejects a row that omits either). The distribution:

- `equal` (11): the SCF anchor and the CSF outcome describe the same control
  concept (e.g. PR.AA-01 → IAC-01, PR.AT-01 → HRS-04, RS.MA-01 → IRO-04).
- `subset_of` (14): the SCF anchor fully covers the CSF outcome and is broader
  (e.g. GV.OC-01 → GOV-01, ID.RA-01 → VPM-01, RC.RP-01 → BCD-02).
- `intersects_with` (10): partial overlap with an explicit residual gap
  (e.g. PR.AA-05 → IAC-21, DE.AE-02 → MON-08).

Strengths run [0.6, 0.9]. The rubric matches the ISO/PCI/SOC 2 crosswalks for
cross-framework consistency. CSF Subcategories are **outcome-oriented** (they
describe a result, not a control mechanism), so several map `subset_of` an SCF
anchor that is mechanism-specific: e.g. PR.DS-01 ("data-at-rest C/I/A") →
CRY-04 ("Encryption At Rest") at strength 0.7, because encryption is one
mechanism for the broader CSF outcome — flagged below.

**Confidence:** HIGH on the STRM _type_ per row (the equal/subset/intersects
call is well-determined by the outcome-vs-mechanism shape); MEDIUM on the
_strength scalars_ (the 0.05 gradations are calibrated by feel, not measurement).

## D5 — The four-framework shared anchor (AC-4): PR.AA-01 → IAC-01

PR.AA-01 ("Identities and credentials are managed") maps to IAC-01
("Identification & Authentication Policy") at `equal`/0.9. IAC-01 is the anchor
slices 438/447 already use for the cross-framework invariant-#1 proof: SOC 2
CC6.1, ISO A.5.15, and PCI 8.2.1 all resolve to it. Adding CSF PR.AA-01 → IAC-01
extends the proof from three frameworks to four — one SCF anchor, four framework
satisfactions, through the single shared anchor row, with NO per-framework
duplicated control and NO requirement → requirement edge.

**Confidence:** HIGH. Asserted by
`TestCSFImport_SharedAnchorSatisfiesFourFrameworks_Invariant1`.

## D6 — The GOVERN no-analog proof (AC-5): GV.RR-02 → GOV-04

GV.RR-02 ("security roles and responsibilities established") maps to GOV-04
("Information Security Roles & Responsibilities") at `equal`/0.9. The
load-bearing point: SOC 2's TSC has **no GOVERN Function** — there is no SOC 2
organizing structure that is the analog of GOVERN. The CSF GOVERN Subcategory
nonetheless reaches governance coverage cleanly through an SCF anchor.

**What is NOT claimed:** that GOV-04 is SOC-2-untouched. SOC 2's
Control-Environment criteria (CC1.x) DO map to the SCF governance anchors — and
that is invariant #1 working exactly as designed (one governance anchor
satisfies a SOC 2 CC1.x criterion AND a CSF GOVERN Subcategory through the SAME
anchor). The no-analog claim is at the **framework-structure** grain (SOC 2 has
no GOVERN Function), and the constitutional guarantee the test proves is
**invariant #7**: CSF GOVERN reaches governance coverage ONLY through SCF
anchors, never via a CSF → SOC 2 requirement-to-requirement edge.

This distinction is the bug-of-the-build (see the detection-tier header): the
first test draft conflated the two and was corrected.

**Confidence:** HIGH on the mapping (GV.RR-02 → GOV-04 is unambiguous); HIGH on
the corrected assertion shape.

## D7 — Low-confidence mappings flagged for spot-check

These rows carry the lowest strengths (≤0.65) and have an explicit residual gap.
They are the top of the revisit list:

| CSF Subcategory | Anchor | STRM            | Strength | Gap                                                                                                           |
| --------------- | ------ | --------------- | -------- | ------------------------------------------------------------------------------------------------------------- |
| GV.OV-01        | CPL-03 | intersects_with | 0.6      | Strategy-outcome _review_ overlaps Internal Audit, but strategy _adjustment_ governance is not in the anchor. |
| RC.CO-03        | BCD-02 | intersects_with | 0.6      | Recovery-progress _communication_ is a thin facet of the broad BCP anchor.                                    |
| RS.AN-03        | IRO-04 | intersects_with | 0.65     | Incident _root-cause/forensic_ analysis is more specific than the IR-plan anchor.                             |
| RC.RP-04        | BCD-02 | intersects_with | 0.65     | Post-incident operational-norms is a thin facet of the BCP anchor.                                            |
| DE.AE-02        | MON-08 | intersects_with | 0.7      | Adverse-event _analysis/correlation_ is broader than anomalous-behavior detection.                            |

The seeded SCF sample fixture (53 anchors) does not include a dedicated
strategy-governance, recovery-communication, or forensic-analysis anchor; the
real SCF catalog the operator imports (slice 006) likely has finer-grained
anchors that would lift these strengths and tighten the STRM type.

**Confidence:** LOW (by design — these are the flagged rows).

---

## Revisit once in use

1. **Re-map the five ≤0.65 rows (D7)** once the operator imports the full SCF
   catalog (not the 53-anchor sample). Finer-grained anchors for strategy
   governance (GV.OV-01), recovery communication (RC.CO-03), and forensic
   analysis (RS.AN-03) likely exist and would raise the mappings to `subset_of`.
2. **Re-confirm the curated subset (D3)** against what enterprise customers and
   insurers actually request in CSF self-assessments — the "high-signal" call is
   the agent's best guess, not field-validated. Specifically, validate whether
   the GOVERN over-weighting (8 of 35) matches real diligence demand.
3. **Re-calibrate strength scalars (D4)** once a publisher-official NIST CSF 2.0
   → SCF crosswalk is available; flip `source_attribution` from `community_draft`
   to `scf_official` on rows that match the official mapping.
4. **Full Subcategory coverage** — this slice ships 35 of ~106; the remaining
   ~71 are a follow-on slice (filed below as 514).
5. **CSF Profile / Tier assessment** — the CSF maturity construct (Tiers 1-4 +
   Current/Target Profiles) is explicitly out of catalog scope (P0-480-6); it is
   a separate future slice (filed below as 515).
6. **PR.DS-01 / PR.DS-02 outcome-vs-mechanism (D4)** — re-check whether the CSF
   data-protection outcomes should map `intersects_with` rather than `subset_of`
   the encryption anchors once a non-encryption at-rest/in-transit protection
   evidence_kind exists (mirrors the SOC 2 CC6.7 revisit note in slice 438).

## Confidence summary

| Decision                                | Confidence                       |
| --------------------------------------- | -------------------------------- |
| D1 — generic loader, no CSF code        | HIGH                             |
| D2 — licensing posture                  | HIGH                             |
| D3 — curated subset / Function coverage | HIGH coverage / MEDIUM selection |
| D4 — STRM type + strength               | HIGH type / MEDIUM strength      |
| D5 — four-framework shared anchor       | HIGH                             |
| D6 — GOVERN no-analog proof             | HIGH                             |
| D7 — low-confidence flagged rows        | LOW (by design)                  |
