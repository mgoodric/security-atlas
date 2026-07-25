# 651 — Map THR-05 / THR-06 / THR-07 into the framework crosswalks: JUDGMENT decisions log

Slice type: JUDGMENT (crosswalk strength selection). This file records the
subjective build-time calls for slice 651 — the per-framework SEARCH for
insider-threat and coordinated-disclosure requirements, the one edge that search
found and its STRM relationship + strength, the candidates REJECTED (and why),
and the detection-tier classification. It does NOT block merge; the maintainer
iterates post-deployment from the "Revisit" notes.

Parent: slice 646 (`docs/audit-log/646-thr-crosswalk-edges-decisions.md` —
authored the THR-02/03/04/09/10 edges and filed this gap as D4 spillover).
Grandparent: slice 641 (imported `THR-02..THR-10` as `scf_anchors` rows).

## D0 — Scope, invariants, and the strength rubric reused

- **All edges are requirement → SCF anchor** (invariant #7 / #1), STRM-typed
  (`relationship_type` ∈ {equal, subset_of, superset_of, intersects_with,
  no_relationship} + `strength` 0..1), the same YAML shape slices 635/646 used.
  NO requirement → requirement edges; the b227 guard
  `TestImport_NoDirectRequirementToRequirementTableExists` stays green.
- **Additive, not replacement.** The one new edge is a SECOND anchor on a
  requirement that already had one (invariant #1 — N anchors per requirement).
  No existing edge is changed or deleted.
- **Strength rubric** (reused verbatim from the existing crosswalks): `1.0` STRM
  equal · `0.9` equal-minor-scope · `0.7–0.8` subset/high-overlap · `0.6`
  intersects partial.
- **No new framework catalog bundled.** The search ran against the five
  crosswalks already in `data/crosswalks/`; adding a catalog to manufacture a
  mapping target was out of bounds (licensing implications) and was not done.
- **Pure crosswalk-data slice.** Only `data/crosswalks/nist-csf-2.0.yaml`
  changes, plus one new integration test file and a loader-test count update.
  No migration; no schema or evidence-kind change.

## D1 — The search (acceptance criterion #1), per framework

The five bundled crosswalks were swept programmatically over every requirement's
`title` + `body`, once for insider-threat vocabulary
(`insider|sabotage|misuse|abuse|disgruntl|trusted user|workforce|screening|
background|disciplin|sanction|personnel activ|user behavio(u)r|behaviour
analytics|separation of duties|whistle`) and once for coordinated-disclosure
vocabulary (`disclos|unsolicit|bug bount|research(er)|external report|report.*
vulnerab|vulnerab|coordinat|29147|intake`). Every hit was then read against the
SCF anchor text before accept/reject. Requirement counts are the shipped file
totals.

| Framework             | Reqs | Insider-threat hits (all rejected)                                                                                                                                          | Coordinated-disclosure hits                                                                                       |
| --------------------- | ---: | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| `soc2-tsc-2017`       |   43 | none                                                                                                                                                                        | CC2.3 (external communication), CC7.1 (new-vulnerability detection) — both rejected                               |
| `iso27001-2022`       |   93 | A.5.3 (segregation of duties), A.6.1 (screening), A.6.4 (disciplinary process)                                                                                              | A.5.14 (information transfer), A.6.6 (NDAs), A.8.8 (technical vulnerability management) — all rejected            |
| `nist-csf-2.0`        |  106 | PR.AA-05 (least privilege / SoD), DE.CM-03 (personnel activity monitored)                                                                                                   | ID.RA-01/04/05 (internal vuln identification + risk), GV.SC-02, RS.MA-01 — rejected; **ID.RA-08 — ACCEPTED (D2)** |
| `pci-dss-4.0`         |   31 | 3.6.1 (key misuse), 8.5.1 (MFA misuse) — lexical false positives                                                                                                            | 2.2.1, 6.3.1, 6.3.3, 11.3.1 — all internal vuln management, rejected                                              |
| `hipaa-security-rule` |   67 | 164.308(a)(1)(ii)(C) sanction policy, (a)(3)(i) workforce security, (a)(3)(ii)(A) authorization/supervision, (a)(3)(ii)(B) workforce clearance, (a)(5)(i)+(ii)(A) awareness | 164.308(a)(1)(ii)(B) risk management, 164.314(b)(1) group health plan — rejected                                  |

**Result: one genuine match across 340 bundled requirements — NIST CSF 2.0
ID.RA-08 → THR-07.** Nothing for THR-05 or THR-06.

**Slice 646's D2 was right about insider threat and wrong about VDP.** Its sweep
reached ID.RA-01 ("Vulnerabilities in assets are identified, validated, and
recorded" — genuinely internal identification) and concluded from it that "no
bundled framework has a dedicated VDP / coordinated-disclosure requirement." It
did not reach ID.RA-08, which slice 514's full-Subcategory pass had added to the
same file and anchored to VPM-04 alone. The correction is recorded here rather
than by editing 646's log; that log stays the record of what 646 decided.

## D2 — Edge ACCEPTED (one)

### NIST CSF 2.0 (`data/crosswalks/nist-csf-2.0.yaml`)

| Requirement | → SCF  | Relationship | Strength | Reasoning                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| ----------- | ------ | ------------ | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| ID.RA-08    | THR-07 | `equal`      | 0.9      | ID.RA-08 "Processes for receiving, analyzing, and responding to vulnerability disclosures are established" vs THR-07 "establish a Vulnerability Disclosure Program (VDP) … that receives unsolicited reports about potential security vulnerabilities." Same control concept, named in both directions: an established intake process for externally-reported vulnerabilities. This is the single dedicated coordinated-disclosure requirement in the bundled set. |

**Why `equal` and not `intersects_with`.** The distinction slice 646 drew — and
drew correctly — is between INTERNAL vulnerability identification (scan,
identify, remediate) and EXTERNAL coordinated-disclosure INTAKE. ID.RA-08 is
unambiguously the latter: "receiving … vulnerability disclosures" is receipt of
reports from outside, which is exactly THR-07's "receives unsolicited reports."
This is not a broader control that happens to cover VDP as one facet; VDP is
what the Subcategory is about. `intersects_with` would understate a match the
two texts make explicit.

**Why `0.9` and not `1.0`.** Two minor scope differences, in opposite
directions, and neither is large enough to demote the relationship type:

1. ID.RA-08 spans "analyzing, and responding" as well as receiving — the
   downstream triage/remediation half, which the pre-existing
   `ID.RA-08 → VPM-04 subset_of/0.75` edge (Vulnerability Remediation Process)
   already carries. THR-07 is the intake-side anchor.
2. THR-07 scopes the VDP to "the secure development and maintenance of products
   and services"; ID.RA-08 does not name a scope.

`0.9` is the rubric's "STRM equal with minor scope difference" band, and this
is the same call — for the same stated reason (a trailing "and analyzed" /
"and responding" clause) — that slice 646 made for `ID.RA-02 → THR-03`. Using
the same number for the same shape of residual keeps the two readable against
each other.

**The VPM-04 edge stays.** Additive, non-destructive, per D0 and slice 646's
precedent: THR-07 is the primary intake anchor, VPM-04 the remediation-side
secondary. A maintainer may later re-weight VPM-04 down; that is a one-line
edit, not a re-author, and is deliberately not done here.

## D3 — Candidates REJECTED (honest non-mappings)

**THR-05 (Insider Threat Program) — NO EDGE in any framework.** THR-05 is
"implement an insider threat program that includes a cross-discipline insider
threat incident handling team" — an organizational/program construct with a
named cross-functional team. The candidates and why each fails:

- **CSF DE.CM-03** ("Personnel activity and technology usage are monitored to
  find potentially adverse events") is the strongest candidate and the one worth
  naming explicitly, because CSF 2.0's non-normative Implementation Examples for
  DE.CM-03 do mention insider-threat use cases. It is still a reject: the
  Subcategory's OUTCOME is monitoring telemetry, not standing up a program with
  a cross-discipline handling team. It already anchors to
  `MON-08 (Anomalous Behavior Detection) intersects_with/0.7`, which is the
  correct anchor for a detection outcome. Asserting DE.CM-03 → THR-05 would let
  an operator who merely monitors user activity show as having an insider-threat
  program — fabricated coverage of exactly the kind invariant #1's honesty
  posture and the project's AI-assist boundary both forbid. Program ≠ telemetry.
- **CSF GV.RR-04** ("Cybersecurity is included in human resources practices") —
  HR-practice integration, correctly anchored to HRS-01. Not a program.
- **ISO A.6.1 (screening) / A.6.4 (disciplinary) / A.5.3 (segregation of
  duties)** — personnel-security and SoD controls that REDUCE insider risk but
  do not constitute an insider-threat program. Correctly anchored to HRS-01 /
  IAC-05.
- **HIPAA 164.308(a)(1)(ii)(C) / (a)(3)(i) / (a)(3)(ii)(A) / (a)(3)(ii)(B)** —
  sanction policy, workforce security, authorization/supervision, workforce
  clearance. Same reading: workforce-security controls, not a program.
- **PCI 3.6.1 / 8.5.1** — lexical false positives on "misuse" (key protection,
  MFA replay resistance). Not related.

**THR-06 (Insider Threat Awareness) — NO EDGE in any framework.** THR-06 is
"security awareness training on recognizing and reporting potential indicators
of insider threat" — a specialized training TOPIC. Every candidate (SOC 2 CC1.x,
ISO A.6.3, CSF PR.AT-01, PCI 12.6.1, HIPAA 164.308(a)(5)(i)) is general security
awareness training, already correctly anchored to HRS-04 (Security Awareness
Training). General awareness does not carry the insider-indicator curriculum;
mapping it to THR-06 would over-state the relationship. This confirms slice
646's call on independent re-examination.

**THR-07 — the other four frameworks stay unmapped.** SOC 2 CC2.3 (external
communication about internal control) is corporate communication, not a
vulnerability-report intake channel. ISO A.8.8, PCI 6.3.1 / 11.3.1, and CSF
ID.RA-01 are internal vulnerability management. ISO A.6.8 (information security
event reporting) is an INTERNAL personnel reporting channel, correctly anchored
to IRO-09 — a VDP is external and unsolicited. ISO/IEC 29147 (the coordinated-
disclosure standard) is not a bundled framework and bundling it was out of
bounds. The `CC9.2 → THR-07` candidate slice 646 rejected stays rejected for the
reason 646 gave: vendor risk management is a different control concept.

## D4 — Frameworks COVERED vs unchanged

- **Covered (gained a THR-05/06/07 edge):** NIST CSF 2.0 (one edge, ID.RA-08 →
  THR-07).
- **No honest edge (covered-by-absence, documented in D1 + D3):** SOC 2,
  ISO 27001:2022, PCI DSS 4.0, HIPAA Security Rule.
- **THR anchors still carrying NO framework edge:** THR-05, THR-06. This is a
  recorded finding with the search evidence above, not an oversight, and
  `TestTHRInsiderAnchors_RemainUnmapped` guards it so that closing the gap later
  is a deliberate act with its own rationale.
- **THR-08** was never imported by slice 641 and is out of scope here.

## D5 — Verification

- `data/crosswalks/nist-csf-2.0.yaml`: 106 Subcategories, 111 mappings
  (106 base + 4 slice-646 finer-THR + 1 slice-651 VDP).
- `internal/api/soc2import/csf_loader_test.go` — `wantTHRExtraEdges` lifted
  4 → 5. The exact-total assertion is the ratchet that makes a dropped or
  smuggled-in row fail loudly; it was updated deliberately, not relaxed.
- `internal/api/soc2import/thr_vdp_crosswalk_integration_test.go` (new):
  - `TestTHRVDPCrosswalk_EdgeResolves` — ID.RA-08 resolves to BOTH THR-07 and
    VPM-04 through real `fw_to_scf_edges` rows (proves the edge lands and is
    additive).
  - `TestTHRVDPCrosswalk_RelationshipIsHonest` — pins `equal` / `0.9` in the DB,
    so a future re-weighting must be a deliberate edit.
  - `TestTHRInsiderAnchors_RemainUnmapped` — THR-05 and THR-06 have zero edges.
- Importer runs clean: `go test -tags=integration -p 1 ./internal/api/soc2import/...`
  passes end-to-end against Postgres 16, as does the full Leg A package set
  (`scfseed` + `soc2import` + `ucfcoverage`) in the shard's declared order.
  Repo-wide `go test ./internal/...` is green.

## D6 — Confidence

- **High** on the ID.RA-08 → THR-07 edge. The two texts name the same concept in
  near-identical words; this is the least speculative edge in the THR band. The
  strength (`0.9`, not `1.0`) is the only judgment with any room in it, and it
  is anchored to the precedent set for `ID.RA-02 → THR-03`.
- **High** on the THR-05 / THR-06 rejections. DE.CM-03 is the only candidate
  that required real deliberation rather than a lexical check, and the
  program-vs-telemetry distinction is clear enough that it did not warrant
  escalation.
- **Inherited caveat (from slice 641 D5 / 646 D5):** the THR anchor DESCRIPTIONS
  in `migrations/fixtures/scf-sample.json` are the slice-641 house-style
  reconstruction pending maintainer verification against the SCF workbook. The
  THR-07 rationale above quotes that reconstruction. If the canonical SCF text
  for THR-07 differs materially in scope, this edge's strength deserves a
  re-read. The TITLE ("Vulnerability Disclosure Program (VDP)") is
  verbatim-canonical.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** `manual_review`. The defect this slice fixed was a
  documentation-and-data gap — a real coordinated-disclosure requirement present
  in the bundled data since slice 514 but recorded as absent by slice 646 — and
  nothing in the test suite could have caught it. Every test was green with
  ID.RA-08 unmapped to THR-07, because "an anchor has no edges" is a legitimate
  state, not a failure. It surfaced only when this slice re-ran the search
  against the shipped requirement text instead of trusting the prior log.
- **detection_tier_target:** `manual_review`. Correct tier, and no cheaper one
  exists: whether a requirement genuinely matches an anchor is a semantic
  judgment, not a machine-checkable invariant. The generalizable lesson is about
  METHOD rather than tooling — slice 646 concluded "no bundled framework carries
  a coordinated-disclosure requirement" from a targeted look at the requirements
  it expected to be candidates, rather than an exhaustive keyword sweep of all 340. The sweep in D1 is cheap, reproducible, and is what this slice recorded
  so the next crosswalk pass starts from evidence rather than recollection.
- No defect escaped to `production`.

## Revisit once in use

- **THR-05 / THR-06 remain the open gap.** They become authorable when a bundled
  framework gains a dedicated insider-threat requirement — NIST 800-53 PM-12
  (Insider Threat Program) and AT-2(2) (insider-threat awareness) are the
  obvious targets, and the full SCF catalog import would bring its own
  crosswalks. Bundling either is separate work with its own licensing review.
- **The `ID.RA-08 → VPM-04` weighting** may deserve a maintainer re-read now
  that a dedicated intake anchor exists: `subset_of/0.75` was authored when
  VPM-04 was the only available home for the whole Subcategory. Kept as-is here
  (additive, non-destructive).
- **Slice 646's D2 text** now overstates the absence for THR-07. It is left
  intact as the record of that slice's reasoning; this log's D1 is the
  correction. If the maintainer prefers logs to be self-correcting, a one-line
  pointer in 646's D2 to this file is the minimal edit.
