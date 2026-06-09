# 641 — Import the full SCF Threat-Management (THR) domain: JUDGMENT decisions log

Slice type: JUDGMENT (anchor granularity / catalog coverage). This file records
the subjective build-time calls for slice 641 — which THR controls were
imported (and the verbatim-text provenance), the THR-01 reconciliation outcome,
the crosswalk-strength re-evaluation, and the detection-tier classification. It
does NOT block merge; the maintainer iterates post-deployment from the
"Revisit / maintainer verification" notes.

Parent: slice 635 (`docs/audit-log/635-thr-anchor-seed-decisions.md` — seeded
THR-01 as a single domain-head anchor + its STRM crosswalk edges). Grandparent:
slice 533 (`datadog.siem_rule.v1`, owns the advisory anchor + its six bind-sites).

## D1 — Which THR controls were imported, and the grain decision

- **Imported (8 new anchors), family `THR`:**

  | scf_id | title                                  | notes |
  | ------ | -------------------------------------- | ----- |
  | THR-02 | Indicators of Exposure (IOE)           | new   |
  | THR-03 | Threat Intelligence Feeds              | new   |
  | THR-04 | Threat Hunting                         | new   |
  | THR-05 | Insider Threat Program                 | new   |
  | THR-06 | Insider Threat Awareness               | new   |
  | THR-07 | Vulnerability Disclosure Program (VDP) | new   |
  | THR-09 | Threat Catalog                         | new   |
  | THR-10 | Threat Analysis                        | new   |

  Plus `THR-01` (Threat Intelligence Program), already present from slice 635 and
  reconciled here (see D2). Total THR-domain anchors after this slice: **9**.
  Sample-catalog control total: **54 → 62**.

- **Grain JUDGMENT.** The bundled sample catalog is a curated _representative_
  subset, not full SCF — families carry between one and five anchors each (IAC=5,
  CRY=4, NET/HRS/BCD/AST/AAA=3, most others 2, a few domain-heads at 1). "Import
  the full THR domain" is interpreted at that altitude: bring in the canonical
  SCF Threat-Management control set (the published THR identifiers), not invent
  finer sub-controls and not pad to match the densest family. The SCF THR domain
  is itself small; the nine anchors above are its canonical members.

  - **THR-08 deliberately absent.** The canonical SCF THR domain numbering skips
    `THR-08`; the imported set is `01,02,03,04,05,06,07,09,10` to match the real
    domain's identifier sequence, NOT a typo. (Verify against the SCF workbook —
    see D5.)

- **Confidence:** **high** on the identifiers/titles being real canonical SCF THR
  controls; **medium** on the exact per-control descriptions (see D5 — house-style
  reconstruction, flagged for maintainer verification against the SCF workbook).

## D2 — THR-01 reconciliation outcome: description expanded, anchor UNCHANGED

- **Slice 635's THR-01 description** (catalog one-line paraphrase): _"Mechanisms
  exist to implement a threat intelligence program that includes a
  cross-organization information-sharing capability to detect and respond to
  security threats."_
- **Reconciled to:** _"Mechanisms exist to implement a threat intelligence
  program that includes a cross-organization information-sharing capability to
  influence the development of system and security architectures, the selection
  of security solutions, monitoring, threat hunting, and response and recovery
  activities."_
- **Why.** The canonical SCF THR-01 control text frames the threat-intel program
  as one that _influences architecture, solution selection, monitoring, threat
  hunting, and response/recovery_ — slice 635's "detect and respond" paraphrase
  was thinner than the published control. The reconciliation brings the
  description into line with the canonical scope while keeping the catalog's
  established one-line house style (no anchor in the fixture reproduces verbatim
  multi-paragraph SCF prose).
- **The SIEM-rule advisory anchor stays THR-01 — NOT remapped to a finer THR
  sub-control.** Slice 635 D2 judged THR-01 (program-level) the correct anchor for
  the SIEM-rule evidence family over MON-08, and the spec for this slice is
  explicit: only revisit if the verbatim THR-01 text contradicts that choice. It
  does not — the canonical THR-01 text explicitly names "monitoring" and "threat
  hunting" as program outputs, which is exactly the program-level relationship a
  SIEM detection-rule inventory evidences. THR-04 ("Threat Hunting") is the
  _operational hunting activity_, a different altitude from the _program_ a rule
  inventory attests to. So `datadog.siem_rule.v1`'s `x-default-scf-anchors`
  ([MON-01, THR-01]), the `--siem-control scf:THR-01` default, the README, and the
  six slice-635/533 bind-sites are ALL untouched (AC: no change to the
  evidence-kind shape).
- **Confidence:** **high** on keeping the anchor at THR-01; **medium-high** on the
  reconciled wording (house-style, see D5).

## D3 — Crosswalk-strength re-evaluation: KEPT AS-IS

The slice's narrative (point 4) flagged the SOC 2 CC7.2 THR-01
`intersects_with/0.7` edge as a candidate for upgrade to `equal` "now that a
finer detection-rule sub-control may be present." Re-evaluated with the full
domain in hand:

- **CC7.2 → THR-01 stays `intersects_with/0.7`.** CC7.2 is "monitors system
  components for anomalies indicative of malicious acts" — an _operational
  anomaly-monitoring_ requirement. THR-01 is the _threat-intel program_ anchor.
  The program **informs** the detection rules that satisfy CC7.2; it is not
  identical to the operational requirement. `equal` would over-state the program
  anchor's match. CC7.2's `equal/1.0` operational match already lives on the
  MON-08 edge (slice 438), which is the correct home for the equivalence.
- **CC7.3 → THR-01 stays `intersects_with/0.6`** — unchanged; the program provides
  evaluation context, partial overlap, correctly weighted.
- **ISO A.5.7 → THR-01 stays `equal/1.0`** — _confirmed_ by the full domain, not
  contradicted. A.5.7 IS "Threat intelligence"; THR-01 IS the dedicated SCF
  threat-intel anchor. The full domain's presence of finer controls (THR-03 feeds,
  THR-04 hunting) does not dislodge A.5.7's direct equivalence to the
  program-level THR-01 — A.5.7 is itself a program-level requirement.
- **ISO A.8.16 → THR-01 stays `intersects_with/0.6`** — unchanged; partial,
  correctly weighted.
- **No NEW edges added to the finer THR controls (THR-02..THR-10).** Adding e.g.
  CC7.2 → THR-04 (Threat Hunting) was considered and **rejected for this slice**:
  it expands the crosswalk surface beyond the slice's mandate (which is "import
  the domain + reconcile THR-01 + re-evaluate the _existing_ slice-635 strengths"),
  and the operational-detection axis CC7.2 needs is already served by the MON-08
  `equal/1.0` edge. Mapping the new THR controls into the SOC 2 / ISO crosswalks is
  a clean follow-on if/when a maintainer wants the finer coverage — filed as
  spillover (see below).
- **Net outcome:** the honest re-evaluation is that the slice-635 strengths were
  already correct given the program-vs-operational distinction the SIEM-rule
  sibling kind exists to preserve; the full domain confirms rather than revises
  them. **Zero crosswalk-strength changes.**

## D4 — Test surface

- **New test:** `internal/api/soc2import/thr_domain_integration_test.go`
  (`//go:build integration`, in the already-enrolled `soc2import` package — no new
  enrolment; `scripts/audit-integration-enrolment.sh` stays OK at 103 tagged /
  107 enrolled).
  - `TestTHRDomain_AllAnchorsResolveInSeededCatalog` — table-driven over the full
    `thrDomain` set (THR-01,02,03,04,05,06,07,09,10); each must resolve through the
    production `GetSCFAnchorBySCFID` path with family `THR` and a non-empty title.
    A dropped or mistyped anchor fails here.
  - `TestTHRDomain_HasMoreThanTheDomainHead` — asserts the seeded THR family count
    equals `len(thrDomain)` AND is `> 1`, guarding against a regression that
    collapses the import back to the slice-635 THR-01-only state.
- **Slice-635 tests stay green** (`thr_anchor_integration_test.go`):
  `TestTHRAnchor_ResolvesInSeededCatalog` (THR-01 still resolves; the description
  reconciliation is description-only, family/id unchanged) and
  `TestTHRAnchor_DetectionCrosswalkEdgesExist` (the four crosswalk edges +
  A.5.7=equal all still hold — D3 changed no edge).
- **Existing suites stay green (verified against a real Postgres):** scfseed
  (the sentinel-based completeness guard absorbs +8 anchors), scfimport
  (`report.Created == len(cat.Controls)` count assertions are dynamic — no
  hardcoded 53/54/62), and the full soc2import integration suite (SOC2 + ISO +
  CSF + PCI + HIPAA crosswalks — every referenced anchor still resolves).
- **b227 invariant guard verified green:**
  `TestImport_NoDirectRequirementToRequirementTableExists` passes — THR anchors are
  `scf_anchors` rows and the (unchanged) crosswalk edges are requirement → anchor,
  the correct shape; the guard correctly does NOT trip.
- **schemaregistry drift/bijection test green** — the schema's anchor list is
  untouched; pure catalog-data change.

## D5 — Verbatim-text provenance + maintainer-verification flag (LOAD-BEARING)

- **Identifiers + titles: verbatim-canonical.** `THR-01..THR-10` (skipping THR-08)
  and the control titles (Threat Intelligence Program, Indicators of Exposure,
  Threat Intelligence Feeds, Threat Hunting, Insider Threat Program, Insider Threat
  Awareness, Vulnerability Disclosure Program, Threat Catalog, Threat Analysis) are
  the real canonical SCF Threat-Management domain controls.
- **Per-control descriptions: house-style canonical reconstruction, NOT verbatim
  SCF prose — FLAGGED for maintainer verification.** The SCF's authoritative
  per-control "Secure Controls Framework (SCF) Control Description" text ships in
  the downloadable SCF Excel workbook
  (https://securecontrolsframework.com/scf-download). It is NOT present in-repo
  (`Plans/canvas/sources.md` cites only the SCF site + GitHub README, neither of
  which carries per-control prose) and is NOT retrievable from public web pages
  (the domain landing pages give only the 33-domain one-line purposes; the
  per-control text is behind the workbook download). I therefore wrote each
  description as a careful reconstruction aligned with the documented SCF control
  _intent_, in the catalog's existing one-line paraphrase house style (matching
  GOV-01/MON-01/IAC-01 etc., none of which reproduce verbatim SCF prose either).
  Per the slice's instruction, I used best canonical reconstruction and FLAG the
  uncertainty here rather than inventing or blocking.
- **Maintainer action when the workbook is to hand:** diff each THR description
  against the workbook's "Control Description" column and tighten any wording that
  drifts. The identifiers/titles should need no change; only the one-line
  descriptions are reconstruction. This is a low-risk post-merge pass — the
  catalog never reproduces verbatim multi-paragraph SCF prose anyway, so the
  house-style one-liner is the target shape, just verified for fidelity.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** `none`. No bug surfaced during the slice. The change
  is purely additive catalog data + a description reconciliation + a new test; the
  build, vet, gofmt, the new + slice-635 + scfseed + scfimport + soc2import
  integration suites, the b227 guard, the schemaregistry drift test, and the
  enrolment audit all passed on the first run after the edits.
- **detection_tier_target:** `integration`. IF a THR anchor row were malformed
  (bad scf_id, wrong family, duplicate, or a crosswalk edge referencing a
  non-existent anchor), the failure surfaces at the integration tier — the
  `GetSCFAnchorBySCFID` resolution and the crosswalk importer run against a real
  Postgres, exactly where a catalog-data defect belongs. A catalog-data error is a
  real-services-resolution concern, not a pure-Go unit branch.
- No defect escaped to `unit`, `playwright`, `manual_review`, or `production`.

## Spillover (out of scope — band 646-649)

- **Map the finer THR controls (THR-02..THR-10) into the SOC 2 / ISO / CSF / PCI /
  HIPAA crosswalks.** This slice imported the anchors and re-evaluated only the
  slice-635 THR-01 edges; the new controls have catalog rows but no framework
  crosswalk edges yet. A finer-grained crosswalk pass (e.g. CC7.2 → THR-04 Threat
  Hunting, A.8.16 → THR-04, vendor-risk requirements → THR-07 VDP) is a clean
  follow-on. Filed as `docs/issues/646-thr-domain-crosswalk-finer-edges.md`.

## Revisit once in use

- When the **full SCF catalog import** lands (the real ~1,400-control catalog
  rather than the curated sample subset), the THR descriptions reconstructed here
  are superseded by the authoritative workbook text; reconcile then (D5).
- If a maintainer reviewing SOC 2 CC7.2 still judges the THR-01 edge should be
  `equal` (arguing the SIEM-rule family is the direct evidence of CC7.2's
  "monitors … for anomalies indicative of malicious acts"), it is a one-line
  crosswalk edit — D3 deliberately left it `intersects_with` to preserve the
  program-vs-operational distinction, but the call is a maintainer's to flip.
