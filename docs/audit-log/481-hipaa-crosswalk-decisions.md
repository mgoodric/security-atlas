# Slice 481 — HIPAA Security Rule crosswalk: JUDGMENT decisions log

**Slice:** 481 — HIPAA Security Rule crosswalk loader (5th framework via the generic importer; CATALOG-ONLY)
**Type:** JUDGMENT (crosswalk-mapping accuracy is a subjective control call)
**Parent dependency:** slice 438 (generic crosswalk loader) — merged; slice 447 (PCI, 3rd) — merged; slice 480 (NIST CSF, 4th) — merged.
**Author:** agent (no human sign-off gate; per the JUDGMENT slice convention the
agent makes the mapping calls and records them here for post-deployment review).

- detection_tier_actual: none
- detection_tier_target: none

> No bug surfaced during the build. The data + tests landed first-pass: the
> loader unit suite passed without a DB, the integration suite (5-framework
> graph proof AC-4 + the AC-5 confidentiality assertion) passed against a real
> Postgres on the first run, and the existing SOC 2 / ISO / PCI / CSF suites
> passed unmodified (AC-7). Both detection-tier fields are therefore `none`.

> **CATALOG-ONLY — the load-bearing framing (AC-8 mandatory note).** This slice
> ships ONLY the HIPAA Security Rule → SCF crosswalk catalog: requirement-grain
> nodes (standards + implementation specifications) and their STRM-typed edges
> to SCF anchors, exactly like slices 438/447/480 ship for their frameworks. It
> does **NOT** ship the HIPAA covered-entity workflow, BAA tracking, the
> required-vs-addressable implementation-specification DECISION flow, breach
> risk-assessment (§164.402), or a HIPAA FrameworkScope ePHI-environment example.
> **All of that is the canvas §10.3 deferred phase-3 covered-entity workflow,
> NOT this slice** (canvas §10.1 "Deliberately deferred from MVP … HIPAA-specific
> covered-entity workflow"; §10.3 "HIPAA-specific covered-entity workflow
> primitives"). The grill held this line: no pull toward covered-entity process
> flows entered scope. Natural future-slice ideas surfaced during the build are
> filed as docs-only spillovers (516-518 below), not implemented.

This log records the subjective build-time calls: the curated subset, the
per-standard/spec HIPAA → SCF anchor mappings, the STRM type and strength per
row, the required-vs-addressable handling decision, the low-confidence flags,
and the five-framework shared-anchor rationale. It is the durable artifact a
maintainer (or an auditor scanning the catalog) reads to understand why each
edge is what it is.

---

## D1 — Loaded through the slice-438 generic loader (no HIPAA-specific code)

The HIPAA Security Rule ships purely as data
(`data/crosswalks/hipaa-security-rule.yaml`) plus tests. It imports through
`internal/api/soc2import` exactly as SOC 2 / ISO 27001 / PCI DSS / NIST CSF do:
the generic `requirement_code:` YAML key, the same `soc2import.Load` +
`soc2import.Import` entry points, the same anchor-existence + STRM + strength
validation. **No loader code was added or changed** (P0-481-2). The fifth
framework proves the loader generalizes yet again, and confirms the slice-438
posture that "a new framework is pure data + tests, no new package" — no new
`cmd/scripts/coverage-thresholds.json` key and no new
`scripts/integration-shards.txt` entry were required (the package was already
coverage-floored + shard-enrolled by slices 438/480).

**Confidence:** HIGH. Verified by `TestLoad_ShippedHIPAACrosswalkParses` and the
integration suite, which call the identical functions the SOC 2 / ISO / PCI /
CSF suites do; those existing suites pass unmodified (AC-7).

## D2 — Licensing posture: identifiers + titles + original descriptions only

The HIPAA Security Rule is codified at 45 CFR Part 164, Subpart C, and as a work
of the U.S. federal government it is in the **public domain** — like NIST CSF
(slice 480), unlike the copyrighted ISO 27001 (slice 438) and PCI DSS (slice
447). The licensing constraint is therefore weak here. The slice nonetheless
follows the 438/467/480 source-attribution discipline for consistency and for
the operator's mental model:

- CFR section **identifiers** (e.g. `164.312(a)(1)`, `164.308(a)(7)(ii)(A)`) —
  factual references;
- short factual **titles** (e.g. "Access Control", "Data Backup Plan");
- an **original agent-authored** one-line `body` for each standard / spec.

`source_attribution: community_draft` on every row (P0-481-6) — these are the
agent's independent draft mappings, not an HHS- or SCF-published official
crosswalk. SCF anchors are imported separately by the operator (slice 006); this
file ships only the HIPAA → SCF **edge** data, so no pre-built SCF catalog is
bundled (P0-481-6).

**Confidence:** HIGH.

## D3 — Curated subset: 32 standards/specs across all three safeguard categories

The HIPAA Security Rule has ~50+ standards + implementation specifications. The
slice narrative binds a curated high-signal subset (target ~25-35) spanning
**all three safeguard categories**. The shipped subset is 32:

| Safeguard category        | Count | Examples                                                                  |
| ------------------------- | ----- | ------------------------------------------------------------------------- |
| Administrative (§164.308) | 18    | Risk Analysis, Risk Management, Workforce Security, Training, Contingency |
| Physical (§164.310)       | 5     | Facility Access Controls, Workstation Security, Device & Media Controls   |
| Technical (§164.312)      | 9     | Access Control, Audit Controls, Integrity, Authentication, Transmission   |

Selection bias is deliberate: (a) the **overlap zone** with SOC 2 / ISO / PCI /
CSF (identity, encryption, monitoring, contingency, training, incident response)
so shared SCF anchors demonstrate invariant #1 at five frameworks; and (b)
**broad standard coverage** within each safeguard category rather than deep
implementation-spec drilling, so the catalog reads as a faithful map of the
Security Rule's shape. §164.314 (organizational requirements) and §164.316
(policies and documentation) are deliberately **out of this subset** — they are
follow-on coverage (spillover 516), not catalog scope for the first thin slice.

**Confidence:** HIGH on safeguard-category coverage (asserted by
`TestLoad_HIPAASpansAllThreeSafeguardCategories`); MEDIUM on which specific
standards/specs within each category are "highest signal" — a maintainer running
real HIPAA assessments will know which the operators actually ask about.

## D4 — STRM type + strength per mapping

Every edge carries an explicit `relationship_type` + `strength` (no silent
`equal/1.0`; the loader rejects a row that omits either). The distribution:

- `equal` (12): the SCF anchor and the HIPAA standard describe the same control
  concept (e.g. 164.308(a)(1)(ii)(A) Risk Analysis → RSK-04, 164.312(a)(1)
  Access Control → IAC-01, 164.312(b) Audit Controls → AAA-01).
- `subset_of` (7): the SCF anchor fully covers the HIPAA spec and is broader
  (e.g. 164.308(a)(7)(i) Contingency Plan → BCD-02, 164.312(d) Authentication →
  IAC-01).
- `intersects_with` (13): partial overlap with an explicit residual gap
  (e.g. 164.310(b) Workstation Use → PES-04, 164.312(c)(1) Integrity → DCH-01).

Strengths run [0.6, 0.9]. The rubric matches the SOC 2 / ISO / PCI / CSF
crosswalks for cross-framework consistency. HIPAA's Administrative safeguards are
**process-oriented** (they describe a program activity, not a control
mechanism), so several map `subset_of` an SCF anchor that is the governing
program (e.g. Security Management Process → GOV-01; Risk Management → RSK-01).

**Confidence:** HIGH on the STRM _type_ per row (the equal/subset/intersects
call is well-determined by the standard-vs-mechanism shape); MEDIUM on the
_strength scalars_ (the 0.05 gradations are calibrated by feel, not measurement).

## D5 — The five-framework shared anchor (AC-4): 164.312(a)(1) → IAC-01

HIPAA Technical Access Control standard 164.312(a)(1) ("only authorized
persons/programs access ePHI") maps to IAC-01 ("Identification & Authentication
Policy") at `equal`/0.9. IAC-01 is the anchor slices 438/447/480 already use for
the cross-framework invariant-#1 proof: SOC 2 CC6.1, ISO A.5.15, PCI 8.2.1, and
CSF PR.AA-01 all resolve to it. Adding HIPAA 164.312(a)(1) → IAC-01 extends the
proof from four frameworks to **five** — one SCF anchor, five framework
satisfactions, through the single shared anchor row, with NO per-framework
duplicated control and NO requirement → requirement edge.

The integration test additionally confirms 164.312(d) Person/Entity
Authentication also routes through IAC-01 (`subset_of`/0.8) — a second HIPAA
satisfaction of the same anchor, reinforcing invariant #1 within HIPAA itself.

**Confidence:** HIGH. Asserted by
`TestHIPAAImport_SharedAnchorSatisfiesFiveFrameworks_Invariant1`.

## D6 — Required-vs-addressable handling: documentation only, no loader field, no workflow

The HIPAA Security Rule classifies each implementation specification as
**(R)equired** or **(A)ddressable** (45 CFR §164.306(d)). The question for this
slice: how to represent that in the catalog WITHOUT building the
required-vs-addressable decision workflow (P0-481-1)?

**Decision: annotate it as a PUBLIC REGULATORY FACT in the per-requirement YAML
comment, and do NOT carry it as a structured loader field.** Rationale:

1. The generic 438 loader's `Requirement` / `Mapping` structs have **no metadata
   column** for an R/A tag. Adding one would be a loader change, and P0-481-2
   forbids HIPAA-specific loader code. The spec explicitly permits omitting the
   tag "if it doesn't fit cleanly" — it does not fit cleanly without a loader
   change, so it stays a comment.
2. A structured R/A field would be the first thread of the covered-entity
   workflow (the required-vs-addressable DECISION flow is exactly the deferred
   §10.3 mechanic). Keeping it a comment keeps the catalog-only line clean.
3. The R/A classification is nonetheless valuable reading for the operator, so
   it is preserved as a public-domain regulatory fact inline (e.g.
   `# Implementation spec (A)` on 164.312(a)(2)(iv) Encryption/Decryption).

When the phase-3 covered-entity workflow lands (spillover 517), it owns adding
the structured R/A field and the addressable-spec decision flow — and at that
point the comments here become the seed data for that field.

**Confidence:** HIGH on the decision; the comments reflect the standard 45 CFR
§164.306(d) classifications (public-domain regulatory fact).

## D7 — Low-confidence mappings flagged for spot-check

These rows carry the lowest strengths (≤0.65) and have an explicit residual gap.
They are the top of the revisit list:

| HIPAA spec    | Anchor | STRM            | Strength | Gap                                                                                          |
| ------------- | ------ | --------------- | -------- | -------------------------------------------------------------------------------------------- |
| 164.310(b)    | PES-04 | intersects_with | 0.6      | Workstation-use POLICY (function/manner/surroundings) is broader than physical access.       |
| 164.312(c)(1) | DCH-01 | intersects_with | 0.65     | Data-integrity protection has no dedicated integrity anchor in the 53-anchor sample fixture. |
| 164.308(a)(8) | CPL-03 | intersects_with | 0.65     | Periodic Evaluation's nontechnical facet is broader than the Internal Audit Function anchor. |

The seeded SCF sample fixture (53 anchors) does not include a dedicated
data-integrity anchor, a workstation-use-policy anchor, or a
program-evaluation anchor; the real SCF catalog the operator imports (slice 006)
likely has finer-grained anchors that would lift these strengths and tighten the
STRM type.

**Confidence:** LOW (by design — these are the flagged rows).

---

## Revisit once in use

1. **Re-map the three ≤0.65 rows (D7)** once the operator imports the full SCF
   catalog (not the 53-anchor sample). A dedicated data-integrity anchor (for
   164.312(c)(1)) and a program-evaluation anchor (for 164.308(a)(8)) likely
   exist in the full catalog and would raise the mappings to `subset_of`.
2. **Re-confirm the curated subset (D3)** against what HIPAA covered entities
   and their auditors actually request — the "high-signal" call is the agent's
   best guess, not field-validated.
3. **Re-calibrate strength scalars (D4)** once a publisher-official HIPAA → SCF
   crosswalk (or the NIST SP 800-66r2 → SP 800-53 → SCF chain) is available;
   flip `source_attribution` from `community_draft` to `scf_official` on rows
   that match the official mapping.
4. **Full Security Rule coverage** — this slice ships 32 of the ~50+ standards +
   specs, and omits §164.314 (organizational) + §164.316 (documentation); the
   remainder is a follow-on slice (filed below as 516).
5. **The phase-3 covered-entity workflow** — BAA tracking, the
   required-vs-addressable decision flow, breach risk-assessment (§164.402), and
   the §164.308 administrative-safeguard process flows are the deferred §10.3
   work (filed below as 517), explicitly NOT catalog scope.
6. **HIPAA FrameworkScope ePHI-environment example** — proving the
   FrameworkScope intersection (HIPAA covered systems ≠ SOC 2 system ≠ PCI CDE,
   canvas §5.5) pairs with the covered-entity workflow (filed below as 518).

## Confidence summary

| Decision                                | Confidence                       |
| --------------------------------------- | -------------------------------- |
| D1 — generic loader, no HIPAA code      | HIGH                             |
| D2 — licensing posture                  | HIGH                             |
| D3 — curated subset / category coverage | HIGH coverage / MEDIUM selection |
| D4 — STRM type + strength               | HIGH type / MEDIUM strength      |
| D5 — five-framework shared anchor       | HIGH                             |
| D6 — required-vs-addressable handling   | HIGH                             |
| D7 — low-confidence flagged rows        | LOW (by design)                  |
