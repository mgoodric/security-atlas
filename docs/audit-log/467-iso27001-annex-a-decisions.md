# Slice 467 — ISO 27001:2022 full Annex A coverage decisions log

> JUDGMENT slice. Crosswalk-mapping accuracy is a subjective control call.
> Per the project's JUDGMENT-slice workflow, the agent makes the per-control
> mapping calls itself and records the rationale + confidence here rather than
> blocking the merge on a human sign-off; the maintainer iterates
> post-deployment. This file is the durable spot-check artifact (AC-2) and the
> companion to the slice-438 decisions log
> (`docs/audit-log/438-iso27001-crosswalk-decisions.md`), which covers the
> original curated 36-control subset.

**Slice:** 467 — ISO 27001:2022 full Annex A coverage completion (extends #438)
**Type:** JUDGMENT
**Crosswalk file:** `data/crosswalks/iso27001-2022.yaml`
**Source attribution:** `community_draft` (agent-authored, not a publisher-official crosswalk)
**Coverage:** all 93 Annex A controls (A.5 ×37, A.6 ×8, A.7 ×14, A.8 ×34) / 127 edges
**Added this slice:** 57 controls (+84 new edges) + the A.8.24 cryptography split (+2 edges) + an A.8.11 secondary edge
**Date:** 2026-06-11

---

## Detection-tier classification (slice 353 / Q-13)

- `detection_tier_actual`: `none`
- `detection_tier_target`: `none`

No bug surfaced during the build. This is pure data + test work on the
already-generalized loader (slice 438 made it framework-agnostic; no code
change here). The loader's anchor-existence guard (`crosswalk: scf_anchor ...
not found`, slice 007 / 438) caught nothing because every anchor was validated
against the 62-anchor sample fixture (`migrations/fixtures/scf-sample.json`)
before authoring — the binding constraint described in D1 below.

---

## Decisions made

### D1 — The 62-anchor sample fixture is the binding constraint on anchor choice

The crosswalk importer resolves each `scf_anchor` against the SCF catalog seeded
from `migrations/fixtures/scf-sample.json`. An edge to an anchor absent from that
catalog is a hard import error that rolls the whole import back
(`TestISOImport_RejectsEdgeToNonexistentAnchor`). The sample fixture holds **62
anchors**. Therefore every one of the 57 new controls maps to one of those 62
codes — not to the "ideal" SCF anchor a control might deserve in the full
~1,400-control SCF catalog. Each mapping is the best-reasoned match _within the
available sample anchor set_, recorded with its rationale in the YAML.

Where the sample catalog genuinely lacks a dedicated anchor for an ISO concept
(notably data masking — see D4), the control is mapped to the nearest available
anchor and flagged LOW CONFIDENCE here and in the YAML rationale, so the
maintainer re-maps once a fuller SCF catalog is importable. This mirrors the
slice-438 posture exactly.

**Confidence: HIGH** — this is a mechanical, verifiable constraint, not a
judgment call; the judgment is in _which_ of the 62 anchors fits best.

### D2 — STRM relationship-type + strength per edge (the per-control JUDGMENT)

STRM type follows NIST IR 8477 semantics (canvas §3.2), identical to the
slice-438 rubric for consistency:

- `equal` (strength 0.7–0.9): the ISO control and the SCF anchor describe the
  same control concept (e.g. `A.8.1 → END-04`, `A.8.28 → SEA-05`,
  `A.8.32 → CHG-02`, `A.5.34 → PRI-01`, `A.6.8 → IRO-09`).
- `subset_of` (0.6–0.8): the ISO control is narrower than the broader SCF anchor
  (e.g. `A.5.11 return of assets → HRS-09`, `A.5.25 event triage → IRO-04`,
  `A.8.17 clock sync → AAA-01`).
- `intersects_with` (0.4–0.6): partial overlap with an explicit residual gap
  (e.g. `A.5.6 special-interest-group contact → THR-01`, `A.7.5 environmental
threats → PES-04` + `BCD-02`).

Several controls carry **multiple edges** where the ISO concept legitimately
spans two anchors (e.g. `A.5.34 PII → PRI-01` + `PRI-04`; `A.7.5 → PES-04` +
`BCD-02`; `A.8.23 web filtering → NET-04` + `END-07`). Multiple
requirement→anchor edges are invariant-#1-correct (one anchor, N frameworks);
what is forbidden is a requirement→requirement edge (P0-467-1), of which there
are zero — the schema has no fw_to_fw table.

**New-control confidence distribution (by the control's best edge):**

| Confidence             | Count | Notes                                                               |
| ---------------------- | ----- | ------------------------------------------------------------------- |
| HIGH (best ≥ 0.8)      | 21    | `equal`/strong-`subset_of` to a closely-titled anchor               |
| MEDIUM (best 0.6–0.79) | 33    | defensible `subset_of`/`intersects_with`; the bulk of the long tail |
| LOW (best ≤ 0.59)      | 3     | see the LOW-confidence table below                                  |

**LOW-confidence new controls flagged for priority review:**

| ISO control                                       | Anchor              | type            | strength | Why low-confidence                                                                                                                                                       |
| ------------------------------------------------- | ------------------- | --------------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `A.5.6` Contact with special interest groups      | `THR-01`            | intersects_with | 0.5      | Forum/SIG engagement is a threat-information-sharing activity, but THR-01 (Threat Intelligence Program) is broader; the sample lacks a "professional-engagement" anchor. |
| `A.7.5` Protecting against physical/environmental | `PES-04` + `BCD-02` | intersects_with | 0.5      | Environmental-threat protection straddles physical security and continuity; neither anchor is a clean single fit. Two edges capture both facets.                         |
| `A.7.12` Cabling security                         | `PES-04` + `NET-01` | intersects_with | 0.5      | Cabling security is half physical-protection, half network-medium-integrity; the sample has no dedicated cabling/media anchor.                                           |

The remaining 54 new controls are `strength ≥ 0.6` and judged defensible against
the SCF anchor titles.

**Confidence: MEDIUM-HIGH** overall — the `equal` calls track the SCF anchor
titles closely; the three flagged `intersects_with` controls are the honest gaps
where the 62-anchor sample lacks a precise match.

### D3 — A.8.24 cryptography split (slice-438 revisit item closed)

The slice-438 decisions log "Revisit once in use" item #3 flagged that A.8.24
("Use of cryptography, including key management") was mapped to a single
`CRY-08` (Encryption In Transit) edge, but the control genuinely spans cryptography
policy _and_ key management. Slice 467 splits it into three edges:

- `A.8.24 → CRY-01` (Use of Cryptographic Controls), `equal` 0.9 — the
  policy-level primary edge.
- `A.8.24 → CRY-08` (Encryption In Transit), `subset_of` 0.7 — the in-transit
  application facet (still shared with SOC 2 CC6.7).
- `A.8.24 → CRY-09` (Cryptographic Key Management), `subset_of` 0.8 — the
  key-lifecycle half the slice-438 log called out.

**Confidence: HIGH** — all three CRY anchors are in the sample fixture and each
edge tracks a distinct, named facet of A.8.24.

### D4 — A.8.11 data-masking stopgap re-checked; DCH-01 retained + IAC-01 added

The slice-438 "Revisit once in use" item #1 paired A.5.7 (threat intel) and
A.8.11 (data masking) for re-mapping once a fuller SCF catalog landed. A.5.7 was
already re-mapped to the dedicated `THR-01` anchor by slices 635/641/646 when the
THR domain was seeded into the sample catalog. **A.8.11 data masking is NOT
similarly resolvable**: slice 467 re-checked the 62-anchor sample fixture and
confirmed it still has **no dedicated data-masking anchor**. The honest call is
to keep `DCH-01` (Data Classification & Handling) as the primary stopgap at
`intersects_with`/0.4 (LOW CONFIDENCE) and add a secondary `IAC-01`
(`intersects_with`/0.5) edge capturing the access-control facet — masking is
applied "in accordance with the access-control policy," which IAC-01 governs.
This is a better mapping than the slice-438 single DCH-01 edge but is still a
stopgap pending a masking-family anchor.

**Confidence: HIGH** on the "no masking anchor exists in the sample" finding;
**LOW** on the DCH-01/IAC-01 mappings themselves being the _right_ long-term home
(they are the best available, not the ideal).

### D5 — A.5.7 threat intelligence: no change needed (already re-mapped upstream)

The slice-438 revisit list paired A.5.7 with A.8.11. A.5.7 was independently
re-mapped to `THR-01` (Threat Intelligence Program, `equal`/1.0) plus `THR-03`
(Threat Intelligence Feeds, `subset_of`/0.7) by slices 635/641/646 once the THR
domain seeded into the sample catalog. Slice 467 verified this and left it
untouched — the slice-438 stopgap (`MON-08`/0.5) is already gone. No action
required beyond confirming the existing mapping is sound (it is).

**Confidence: HIGH** — verified existing state; no re-mapping risk introduced.

### D6 — Licensing posture unchanged (P0-467-2)

ISO/IEC 27001:2022 is a copyrighted standard. The 57 new controls reference only
the Annex A control **identifiers** (e.g. `A.7.11`) and **short titles** — both
factual references, not protected expression — and each carries an **original
agent-authored** one-line `body` description. No verbatim ISO standard text is
reproduced. This is byte-for-byte the slice-438 posture (D5 there), continued.
The SCF anchors remain operator-imported (slice 006); this slice ships only the
ISO→SCF edge data (P0-467-3 — no bundled pre-built SCF data).

**Confidence: HIGH** on the identifier-and-titles-are-factual posture; the SCF
redistribution legal review (open question, CLAUDE.md) is a separate pre-ship
gate orthogonal to authoring ISO edge data.

### D7 — Test extension: full-93 assertions, no new harness

The slice-438 integration suite (`internal/api/soc2import/iso_integration_test.go`)
already tied created-row counts to `len(cw.Requirements)`/`len(cw.Mappings)`, so
it auto-scaled to 93. Slice 467 extended in place rather than writing a new
harness:

- `iso_loader_test.go` — lifted the curated-subset `[30,45]` cap to assert
  **exactly 93** requirements, and added `TestLoad_ShippedISOCrosswalkCoversFullAnnexA`
  which asserts the per-theme cardinality (37/8/14/34) and that the requirement
  set _equals_ the standard's control-code set (no missing, no extras, no dups).
- `iso_integration_test.go` — added `TestISOImport_FullAnnexA_ImportsAll93Controls`
  (93 requirement rows persist; zero orphan requirements; idempotent re-import)
  and `TestISOImport_FullAnnexA_PreservesSlice438Subset` (a representative sample
  of the original 36 controls still resolves to their original anchors).

The existing invariant-#1 proof test (`...SharedAnchorSatisfiesBothFrameworks...`)
now observes IAC-01 satisfying SOC 2 CC6.1 plus **five** ISO controls
(A.5.15, A.5.16, A.5.17, A.8.3, A.8.11) — the full-coverage extension deepened
the shared-anchor graph, which is the point.

**Confidence: HIGH** — extended the existing suite; ran the full soc2import
integration suite (ISO + SOC 2 + PCI + CSF + HIPAA) against a real Postgres,
all green, plus ucfcoverage as a regression check.

---

## Revisit once in use

1. **A.8.11 data masking** — re-map off `DCH-01`/`IAC-01` once a dedicated
   data-masking / tokenization anchor exists in the imported SCF catalog. The
   sample fixture has none today (D4).
2. **The 3 LOW-confidence controls** (A.5.6, A.7.5, A.7.12 — D2 table) — these
   are the spot-check priority. Each is an `intersects_with` to the nearest
   available anchor where the sample lacks a precise match (professional-forum
   engagement; environmental/disaster-threat; cabling/media-integrity).
3. **The full ~1,400-control SCF catalog re-anchoring pass** — every mapping in
   this file targets one of the **62 sample anchors**, not the ideal anchor from
   the complete SCF catalog. When the production SCF catalog is imported, the
   whole crosswalk warrants a re-anchoring review — many `subset_of`/`intersects_with`
   edges could become `equal` against a more specific anchor (e.g. A.5.3
   segregation-of-duties, A.8.12 DLP, A.8.6 capacity management each likely have
   a dedicated full-catalog anchor).
4. **Strength calibration** — the strengths are first-pass rubric scores; a
   reviewer with the customer's specific scope and the full SCF catalog will tune
   them. The medium-confidence long tail (33 controls) is where calibration will
   move the most.
5. **Multi-edge controls** — several controls carry two anchors (D2). Whether
   both edges should survive into the production crosswalk, or one should be
   demoted, is a reviewer call once real coverage queries run against the graph.

---

## Anti-criteria self-check (P0-467-\*)

| P0                                          | Status                                                                                                                                                  |
| ------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| P0-467-1 — no requirement→requirement edge  | PASS — every one of the 127 edges is requirement→SCF-anchor; the schema has no fw_to_fw table (DDL-enforced; the loader only writes `fw_to_scf_edges`). |
| P0-467-2 — no verbatim copyrighted ISO text | PASS — identifiers + short titles (factual refs) + original agent-authored `body` only; no ISO standard prose reproduced (D6).                          |
| P0-467-3 — no bundled pre-built SCF data    | PASS — ships only the ISO→SCF edge YAML; SCF anchors remain operator-imported (slice 006). No fixture/catalog data added.                               |
