# 646 — Map the finer SCF THR controls into the framework crosswalks: JUDGMENT decisions log

Slice type: JUDGMENT (crosswalk strength selection). This file records the
subjective build-time calls for slice 646 — which framework requirements gain a
requirement → SCF-anchor edge to the freshly-imported `THR-02..THR-10` controls,
the STRM relationship + strength chosen for each, which candidate edges were
REJECTED (and why), which frameworks were covered vs spilled, and the
detection-tier classification. It does NOT block merge; the maintainer iterates
post-deployment from the "Revisit" notes.

Parent: slice 641 (`docs/audit-log/641-scf-thr-domain-decisions.md` — imported
`THR-02..THR-10` as `scf_anchors` rows; D3 + its spillover deliberately deferred
the finer crosswalk pass to this slice). Grandparent: slice 635
(`docs/audit-log/635-thr-anchor-seed-decisions.md` — the original `THR-01` edges

- the STRM strength rubric reused here).

## D0 — Scope, invariants, and the strength rubric reused

- **All edges are requirement → SCF anchor** (invariant #7 / #1), STRM-typed
  (`relationship_type` ∈ {equal, subset_of, superset_of, intersects_with,
  no_relationship} + `strength` 0..1), mirroring the exact YAML shape slice 635
  used for `THR-01`. NO requirement → requirement edges. The b227 guard
  `TestImport_NoDirectRequirementToRequirementTableExists` stays green (these are
  the correct shape).
- **Additive, not replacement.** Every new edge is a SECOND (or third) anchor on
  a requirement that already had one — the "N anchors per requirement" graph
  shape (invariant #1). No existing slice-635/641 edge is changed; the `THR-01`
  edges re-evaluated by slice 641 D3 are kept as-is (the finer domain does not
  change the program-vs-operational distinction those edges encode).
- **Strength rubric** (the existing crosswalks' rubric, reused verbatim): `1.0`
  STRM equal · `0.9` equal-minor-scope · `0.7–0.8` subset/high-overlap · `0.6`
  intersects partial.
- **Pure crosswalk-data slice.** Only `data/crosswalks/{soc2-tsc-2017,
iso27001-2022,nist-csf-2.0}.yaml` change, plus a new integration test and a
  CSF-loader-test count update. No migration; no schema or evidence-kind change;
  the schemaregistry drift/bijection guard is untouched.

## D1 — Edges ACCEPTED (per framework, with relationship + strength + reasoning)

### SOC 2 (`data/crosswalks/soc2-tsc-2017.yaml`)

| Requirement | → SCF  | Relationship      | Strength | Reasoning                                                                                                                                                                                                                                                                                                                                                                                                                            |
| ----------- | ------ | ----------------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| CC7.2       | THR-04 | `intersects_with` | 0.6      | CC7.2 "monitors system components … for anomalies indicative of malicious acts." Threat Hunting (THR-04) is the PROACTIVE operational activity that detects malicious-act anomalies evading existing controls — a partial overlap (hunting is one technique within CC7.2's broader monitoring; CC7.2 also covers natural-disaster/error anomalies hunting does not). Sits ALONGSIDE the existing CC7.2 → MON-01/MON-08/THR-01 edges. |

### ISO 27001:2022 (`data/crosswalks/iso27001-2022.yaml`)

| Requirement | → SCF  | Relationship      | Strength | Reasoning                                                                                                                                                                                                                                                                                                                                                                                                                 |
| ----------- | ------ | ----------------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A.5.7       | THR-03 | `subset_of`       | 0.7      | A.5.7 "information relating to information security threats is collected and analyzed to produce threat intelligence." Threat Intelligence Feeds (THR-03) are a SUBSET mechanism of that program — the ingest side of producing intelligence. Added alongside the existing A.5.7 → THR-01 `equal/1.0` program edge (the candidate the slice spec named: "ISO A.5.7 → THR-03 subset alongside the existing THR-01 equal"). |
| A.8.16      | THR-04 | `intersects_with` | 0.6      | A.8.16 "networks, systems, and applications are monitored for anomalous behavior" overlaps Threat Hunting (THR-04) — hunting is one monitoring technique that surfaces anomalous behavior. Partial (A.8.16 also covers passive/automated monitoring). The candidate the spec named ("ISO A.8.16 → THR-04"). Sits alongside the existing A.8.16 → MON-01/THR-01 edges.                                                     |
| A.8.8       | THR-02 | `subset_of`       | 0.6      | A.8.8 "the organization's exposure is evaluated" is the threat-EXPOSURE facet of vulnerability management; Indicators of Exposure (THR-02) covers exposure-evaluation directly — a subset of the broader A.8.8 obtain/evaluate/remediate cycle (VPM-01 carries the `equal`). Honest subset: A.8.8's full scope is broader than IOE.                                                                                       |

### NIST CSF 2.0 (`data/crosswalks/nist-csf-2.0.yaml`)

| Requirement | → SCF  | Relationship      | Strength | Reasoning                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| ----------- | ------ | ----------------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| ID.RA-02    | THR-03 | `equal`           | 0.9      | ID.RA-02 "cyber threat intelligence is received from information-sharing sources and analyzed" is the DIRECT STRM match for Threat Intelligence Feeds (THR-03). The slice-480 row's existing `MON-08 intersects_with/0.6 (LOW)` note explicitly flagged "a dedicated threat-intelligence anchor is absent" — THR-03 is now that dedicated anchor (the strongest, cleanest upgrade in this slice, mirroring how slice 635 upgraded ISO A.5.7's MON-08 placeholder). THR-03 is the primary anchor; the MON-08 anomaly-detection overlap stays as a secondary. `0.9` not `1.0` because ID.RA-02 also includes "and analyzed", marginally broader than feed ingestion alone. |
| DE.AE-07    | THR-01 | `intersects_with` | 0.7      | DE.AE-07 "cyber threat intelligence and other contextual information are integrated into event analysis" is the PROGRAM-level threat-intel function; Threat Intelligence Program (THR-01, reconciled in slice 641 to name monitoring/hunting/response as program outputs) is the dedicated anchor for integrating intelligence into analysis. Supersedes the existing `MON-08 intersects_with/0.6 (LOW)` placeholder as the primary anchor. `intersects_with` (not `equal`) — DE.AE-07 is the event-analysis APPLICATION of the program, not the program itself.                                                                                                         |
| ID.RA-03    | THR-09 | `subset_of`       | 0.7      | ID.RA-03 "internal and external threats are identified and recorded" — the "recorded" half is threat-cataloging; Threat Catalog (THR-09) is the dedicated record of identified threats. Subset alongside the existing ID.RA-03 → RSK-04 risk-assessment edge.                                                                                                                                                                                                                                                                                                                                                                                                            |
| ID.RA-03    | THR-10 | `subset_of`       | 0.6      | The "identified" half of ID.RA-03 (distinct from the "recorded" half THR-09 carries) is the threat-ANALYSIS activity that surfaces internal/external threats; Threat Analysis (THR-10) is that anchor. Partial — ID.RA-03 spans both analysis (THR-10) and cataloging (THR-09). Gives THR-10 an honest framework home it otherwise lacked in the bundled crosswalks.                                                                                                                                                                                                                                                                                                     |

**Net: 8 new edges. THR anchors now reached by a framework requirement:
THR-01 (already, + DE.AE-07), THR-02, THR-03, THR-04, THR-09, THR-10.**

## D2 — Candidate edges REJECTED (honest non-mappings)

- **`CC9.2 → THR-07` (Vulnerability Disclosure Program) — REJECTED.** The slice
  spec named "vendor / third-party requirements → THR-07." CC9.2 is "vendor and
  business partner risk management"; a VDP is about RECEIVING external reports of
  vulnerabilities in YOUR OWN systems (a coordinated-disclosure intake program),
  NOT managing vendor risk. Different control concept. CC9.2 already anchors
  correctly to `TPM-01 (equal/1.0)` + `TPM-04 (intersects_with/0.8)`. Mapping
  CC9.2 → THR-07 would be a speculative edge I can't justify.
- **THR-05 (Insider Threat Program) + THR-06 (Insider Threat Awareness) — NO
  EDGES in any framework.** The spec named "insider-threat / personnel-security
  requirements → THR-05 / THR-06." None of the five bundled crosswalks carries a
  DEDICATED insider-threat requirement. The nearest candidates are general
  security-awareness controls (SOC 2 CC1.x, ISO A.6.3, NIST CSF PR.AT, PCI
  12.6.1, HIPAA 164.308(a)(5)) — but general security awareness is NOT
  insider-threat awareness (THR-06 is a specialized subset: awareness OF the
  insider-threat program specifically). Mapping general-awareness → THR-06 would
  over-state the relationship. ISO does not even carry A.6.1 (screening) or A.6.4
  (disciplinary) in this curated crosswalk (only A.6.3 awareness + A.6.5
  post-employment). Honest call: no dedicated match → no edge. Spilled (see D4).
- **THR-07 (VDP) — NO EDGES in any framework.** Beyond the CC9.2 rejection above:
  PCI 6.3.1 / 11.3.1 and CSF ID.RA-01 are INTERNAL vulnerability identification
  (scan/identify/remediate), not EXTERNAL coordinated disclosure intake. No
  bundled framework has a dedicated VDP / coordinated-disclosure requirement.
  Honest call: no match → no edge. Spilled (see D4).
- **PCI DSS + HIPAA — NO THR EDGES (whole-framework).** Their threat/vuln/
  awareness/incident controls already anchor correctly: PCI vuln-management →
  VPM family, anti-malware → END/MAL, IRP → IRO; HIPAA risk-management → RSK-04,
  awareness → SAT, malicious-software → END, incident → IRO. None has a dedicated
  threat-intelligence / threat-hunting / insider-threat / VDP requirement that
  would earn an honest THR edge at the sample crosswalk's altitude. Adding a
  weak THR edge to a PCI/HIPAA requirement just to "cover" the framework would be
  exactly the speculative-edge anti-pattern the slice forbids. Covered-by-absence
  decision, recorded here.

## D3 — Frameworks COVERED vs SPILLED

- **Covered (gained THR-02..THR-10 edges):** SOC 2, ISO 27001:2022, NIST CSF 2.0.
- **No honest THR edge (covered-by-absence, documented in D2):** PCI DSS 4.0,
  HIPAA Security Rule.
- **No missing crosswalk file.** All five framework crosswalk YAMLs already exist
  (`data/crosswalks/{soc2-tsc-2017,iso27001-2022,nist-csf-2.0,pci-dss-4.0,
hipaa-security-rule}.yaml`) — so the "do NOT scaffold a new framework crosswalk
  file" constraint did not bind, and there is no missing-file spillover. The only
  spillover (D4) is the finer THR-05/06/07 coverage gap, not infra.

## D4 — Spillover (out of scope — band 651-654)

- **`docs/issues/651-thr-insider-vdp-crosswalk-edges.md`** — map THR-05 (Insider
  Threat Program), THR-06 (Insider Threat Awareness), and THR-07 (Vulnerability
  Disclosure Program) into framework crosswalks once a bundled framework carries
  a dedicated insider-threat or coordinated-disclosure requirement (e.g. when the
  full SCF catalog import lands, or a framework like NIST 800-53 PM-12 / RA-10 /
  AC-22 is added). This slice deliberately authored NO edges for these three
  controls rather than over-stating general-awareness or internal-vuln
  requirements onto them (D2). Parent: #646.

## D5 — Confidence + verbatim-text caveat

- **Confidence:** **high** that the accepted edges are honest STRM relationships
  at the strengths chosen; **high** on the rejections (the non-mappings are
  clear-cut concept mismatches, not borderline). The `ID.RA-02 → THR-03 equal`
  and `DE.AE-07 → THR-01` upgrades are the most load-bearing calls — both replace
  edges the source data ITSELF flagged `(LOW)` as placeholders for an absent
  threat-intel anchor, so the upgrade is data-supported, not speculative.
- **Verbatim caveat (inherited from slice 641 D5):** the THR anchor TITLES used
  in the rationales (Threat Hunting, Threat Intelligence Feeds, Indicators of
  Exposure, Threat Catalog, Threat Analysis) are verbatim-canonical SCF; the
  per-control descriptions remain the slice-641 house-style reconstruction
  (flagged there for maintainer verification against the SCF workbook). This
  slice adds no new anchor prose — only crosswalk edges — so it inherits that
  caveat without extending it.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** `unit`. One real failure surfaced during the slice
  and was caught at the unit (loader) tier BEFORE any integration run: the CSF
  loader test `TestLoad_CSFFullSubcategoryCoverage` asserted a strict 1:1
  Subcategory↔edge invariant (`len(cw.Mappings) == 106` AND "Subcategory mapped
  more than once" fails) that the four additive CSF THR edges legitimately break
  (invariant #1 — N anchors per requirement). Caught it by reasoning over the
  test before running, updated it to a `len()`-style exact total
  (`106 + 4 finer-THR edges`) + "mapped at least once" coverage, kept it
  meaningful. This is the correct tier: a crosswalk-shape change is exactly what
  the pure-Go loader test exists to catch, fast, with no Postgres.
- **detection_tier_target:** `unit`. Same tier — the strict-count regression is a
  loader-level data-shape concern, caught by the pure-Go loader test, not by a
  Postgres-backed integration run. (The edge-RESOLUTION assertions are
  integration-tier by nature — they need a real `fw_to_scf_edges` row — but no
  bug surfaced there.)
- No defect escaped to `integration`, `playwright`, `manual_review`, or
  `production`.

## Revisit once in use

- When the **full SCF catalog import** lands (the real ~1,400-control catalog),
  the bundled frameworks may gain dedicated insider-threat / VDP / threat-modeling
  requirements — at which point slice 651's THR-05/06/07 edges become authorable
  with honest STRM relationships.
- A maintainer reviewing NIST CSF may choose to RETIRE the now-secondary
  `ID.RA-02 → MON-08 (LOW)` and `DE.AE-07 → MON-08 (LOW)` placeholders entirely,
  since the dedicated THR-03/THR-01 anchors now carry the primary relationship.
  This slice kept them (additive, non-destructive) rather than deleting source
  rows — the retirement is a clean one-line maintainer edit, not a re-author.
