# 635 — Seed the THR detection anchor for the SIEM-rule kind: JUDGMENT decisions log

Slice type: JUDGMENT (anchor selection / catalog mapping). This file records
the subjective build-time calls for slice 635 — the path chosen (seed vs remap),
the canonical-anchor reasoning (THR-01 vs MON-08 vs another), the STRM crosswalk
to SOC 2 CC7.2/CC7.3 + ISO 27001 A.5.7/A.8.16, and the detection-tier
classification. It does NOT block merge; the maintainer iterates
post-deployment from the "Revisit once in use" notes.

Parent: slice 533 (`docs/audit-log/533-datadog-siem-rules-decisions.md`, esp. D2 —
the THR-01-not-in-catalog caveat). Slice 533 shipped `datadog.siem_rule.v1` with
`x-default-scf-anchors=[MON-01, THR-01]` and explicitly filed this slice to close
the catalog gap.

## D1 — Path (a): SEED THR-01 into the catalog, NOT (b) remap the schema's advisory anchor

- **Options considered.** The slice offered two paths:
  - **(a)** Seed the `THR-01` anchor (+ STRM crosswalk rows) into
    `migrations/fixtures/scf-sample.json` so the connector's advisory detection
    anchor resolves against the seeded catalog.
  - **(b)** Remap `datadog.siem_rule.v1`'s advisory `x-default-scf-anchors` to a
    catalog-resolving anchor instead (touching only the schema's advisory list,
    e.g. swapping `THR-01` → `MON-08`).
- **Chosen:** (a), seed. **Confidence: high.**
- **Rationale.**

  1. **THR-01 is the correct canonical anchor (see D2), so the connector's stated
     mapping should be made true, not weakened.** Path (b) would replace a correct
     advisory anchor with a less-precise already-seeded one purely because the
     catalog lacked a row — fixing the symptom by degrading the mapping. Seeding
     fixes the actual gap: the catalog was missing a real SCF domain.
  2. **The connector binds `scf:THR-01` in six places**, not just the schema:

     - `internal/api/schemaregistry/schemas/datadog.siem_rule/1.0.0.json`
       (`x-default-scf-anchors`)
     - `connectors/datadog/cmd/atlas-datadog/cmd_run.go` (`--siem-control` default
       `scf:THR-01`)
     - `connectors/datadog/README.md` (documented default)
     - `connectors/datadog/cmd/atlas-datadog/cmd_run_seam_test.go`,
       `integration_test.go` (×2), `internal/siemrules/record_test.go` (×3)

     Path (b) would have to change the schema AND the run-flag default AND the
     README AND rewrite those tests to stay consistent — far more surface, all of
     it walking back a correct decision. Path (a) touches only catalog data and
     leaves every connector reference intact.

  3. **The catalog genuinely lacked a THR domain.** The sample catalog carried MON
     (incl. MON-08 "Anomalous Behavior Detection") but no Threat-Management
     domain at all. Seeding adds a real, missing, canonical SCF anchor the catalog
     should have had — net-additive, not a workaround.
  4. **It lets us upgrade a known LOW-CONFIDENCE crosswalk edge (D3).** The ISO
     A.5.7 "Threat intelligence" → MON-08 edge was authored in slice 438 with an
     explicit `LOW CONFIDENCE` note: _"SCF has no dedicated threat-intel anchor in
     the sample catalog."_ Seeding THR-01 lets that edge become the correct
     `equal/1.0` it always wanted to be.

- **Constraint honored (AC-3):** the `datadog.siem_rule.v1` evidence-kind SHAPE
  and the schema-drift/bijection guard are UNCHANGED. The schema's anchor list
  stays `["MON-01", "THR-01"]`; only the catalog fixture
  (`migrations/fixtures/scf-sample.json`) and the crosswalk data files
  (`data/crosswalks/soc2-tsc-2017.yaml`, `data/crosswalks/iso27001-2022.yaml`)
  change. The schemaregistry drift/bijection test stays green (verified).
- **Fixture vs migration:** the sample catalog is seeded from the committed
  fixture JSON by `internal/api/scfimport` (loaded via `internal/api/scfseed`),
  NOT by a SQL migration — so this is a pure fixture + crosswalk-data edit, no new
  migration file. (The slice's `20260608090000_*` migration-naming contingency
  did not apply; editing the fixture JSON was the preferred path the slice named.)

## D2 — Canonical anchor: THR-01 "Threat Intelligence Program", NOT MON-08

- **Chosen:** `THR-01`, family `THR`, title "Threat Intelligence Program",
  description "Mechanisms exist to implement a threat intelligence program that
  includes a cross-organization information-sharing capability to detect and
  respond to security threats." **Confidence: high** on THR-01 being a real,
  on-point canonical SCF anchor; **medium-high** on the exact description wording
  (SCF's published THR-01 control text is paraphrased here in the catalog's own
  one-line style — the same posture every other sample-catalog anchor uses, none
  of which reproduce verbatim SCF prose; see the fixture's existing GOV-01/MON-01
  descriptions).
- **Why THR-01 over MON-08.** The two answer DIFFERENT control questions, exactly
  the distinction slice 533 D1 used to justify the sibling kind:
  - **MON-08 "Anomalous Behavior Detection"** ("Mechanisms exist to detect
    anomalous behavior") is the _operational-detection_ anchor — it is already
    the `equal/1.0` STRM target for SOC 2 CC7.2's anomaly-detection language. It
    answers "is anomaly detection operating?"
  - **THR-01 "Threat Intelligence Program"** is the _threat-management_ anchor —
    it answers "is there a threat-detection/intelligence program that the
    detection rules implement?" A SIEM detection rule is a _configured artifact
    of a threat-detection program_; THR-01 is the program-level anchor that a rule
    inventory evidences, which is precisely why slice 533 chose `[MON-01, THR-01]`
    (operational-monitoring + threat-program) rather than `[MON-01, MON-08]`.
  - Using MON-08 as the SIEM-rule's second anchor would collapse the
    threat-program axis into the anomaly-detection axis and lose the distinction
    the sibling kind exists to preserve. THR-01 is the right canonical anchor.
- **Why not a more-specific THR sub-control.** The real SCF THR domain has
  sub-controls (threat-intel feeds, indicators-of-compromise, etc.). At the
  sample-catalog's altitude (one representative anchor per domain — the catalog is
  a 53→54-anchor curated subset, not full SCF), the domain-head THR-01 is the
  correct grain: it mirrors how the catalog seeds one MON-01, one GOV-01, etc. A
  finer THR sub-control is a follow-on of the full SCF import, not this slice.

## D3 — The STRM crosswalk: SOC 2 CC7.2/CC7.3 + ISO 27001 A.5.7/A.8.16

All edges are requirement → SCF anchor (invariant #7), STRM-typed, with the same
strength rubric the existing crosswalks use (1.0 equal · 0.9 equal-minor-scope ·
0.7–0.8 subset/high-overlap · 0.6 intersects partial). Rows added:

**SOC 2 (`data/crosswalks/soc2-tsc-2017.yaml`):**

| Requirement | Title                                    | → SCF  | Relationship    | Strength | Rationale (short)                                                                                                             |
| ----------- | ---------------------------------------- | ------ | --------------- | -------- | ----------------------------------------------------------------------------------------------------------------------------- |
| CC7.2       | Monitors system components for anomalies | THR-01 | intersects_with | 0.7      | Threat-intel program informs the detection rules that monitor for malicious-act anomalies; the SIEM-rule family anchors here. |
| CC7.3       | Evaluates security events                | THR-01 | intersects_with | 0.6      | Threat-intel program provides the context to evaluate whether a detected event could cause a failure to meet objectives.      |

(CC7.2 keeps its existing MON-01 subset_of/0.9 and MON-08 equal/1.0 edges; CC7.3
keeps AAA-01 and IRO-04. THR-01 is an ADDITIONAL anchor, not a replacement — the
"N framework satisfactions per anchor / N anchors per requirement" graph shape.)

**ISO 27001:2022 (`data/crosswalks/iso27001-2022.yaml`):**

| Requirement | Title                 | → SCF  | Relationship    | Strength | Rationale (short)                                                                                                                             |
| ----------- | --------------------- | ------ | --------------- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| A.5.7       | Threat intelligence   | THR-01 | **equal**       | **1.0**  | THR-01 is the direct STRM equivalent of A.5.7 — **upgraded** from the slice-438 LOW-CONFIDENCE MON-08 placeholder (intersects_with/0.5).      |
| A.8.16      | Monitoring activities | THR-01 | intersects_with | 0.6      | A.8.16's "evaluate potential information security incidents" is informed by the threat-intel program; the SIEM-rule family anchors at THR-01. |

- **A.5.7 is a REMAP, not an addition.** Before this slice, A.5.7 mapped to MON-08
  (intersects_with/0.5) with an explicit `LOW CONFIDENCE` note that the catalog
  had no dedicated threat-intel anchor. With THR-01 seeded, that placeholder edge
  is replaced by the correct THR-01 equal/1.0 edge. This is the cleaner outcome
  D1 point 4 anticipated. A.8.16 keeps its existing MON-01 equal/0.9 edge; THR-01
  is added alongside.
- **The slice asked for "ISO 27001 A.12".** ISO/IEC 27001:**2022** retired the
  2013 Annex A.12 ("Operations security", which held A.12.4 Logging & monitoring)
  and reorganized those controls into the 2022 numbering: A.8.15 (Logging) and
  A.8.16 (Monitoring activities) are the 2022 successors of the A.12.4 logging/
  monitoring family, plus A.5.7 (Threat intelligence) is new in 2022. The
  project's crosswalk is the **2022** edition (`iso27001-2022.yaml`), so the
  correct targets are A.5.7 + A.8.16. Mapping to a literal "A.12" code would have
  pointed at a requirement that does not exist in the 2022 crosswalk. Recorded so
  the maintainer sees the 2013→2022 translation was deliberate, not a miss.

## D4 — Test surface: prove resolution + edges, keep the existing suites green

- **New test:** `internal/api/soc2import/thr_anchor_integration_test.go`
  (`//go:build integration`, in the already-enrolled `soc2import` package — no
  new enrolment needed; `scripts/audit-integration-enrolment.sh` stays OK at
  103 tagged / 107 enrolled).
  - `TestTHRAnchor_ResolvesInSeededCatalog` — asserts THR-01 resolves through the
    EXACT production path the importer uses (`GetSCFAnchorBySCFID` against
    `slug='scf' AND status='current'`), the same path whose failure produced the
    original `scf_anchor "…" not found` signature. Pre-seed this returned
    `pgx.ErrNoRows`; post-seed it resolves with family `THR`.
  - `TestTHRAnchor_DetectionCrosswalkEdgesExist` — imports BOTH crosswalks and
    asserts SOC 2 CC7.2/CC7.3 + ISO A.5.7/A.8.16 each resolve to THR-01 through
    real `fw_to_scf_edges` rows, and that A.5.7→THR-01 is specifically `equal`
    (the upgrade from the MON-08 placeholder).
- **Existing suites stay green (verified):** scfseed (3 tests), scfimport
  (load/fuzz/version), and the full soc2import integration suite (SOC2 + ISO +
  CSF + PCI + HIPAA crosswalk imports — all share the catalog and every
  referenced anchor must resolve). The dynamic count assertions
  (`len(cw.Mappings)`, `len(cw.Requirements)`, `len(cat.Controls)`) absorb the
  +1 anchor / +4 edges automatically — no hardcoded `53`/`54` count anywhere.
- **schemaregistry drift/bijection test stays green** — the schema anchor list is
  untouched.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** `none`. No bug surfaced during the slice. The change
  is purely additive catalog data + crosswalk edges + a new test; the build, vet,
  gofmt, pre-commit (prettier/JSON), and every integration suite passed on the
  first run after the edits.
- **detection_tier_target:** `integration`. IF this seed had been wrong (e.g. a
  malformed anchor row, a crosswalk edge referencing a non-existent anchor, or a
  duplicate scf_id), the failure would surface at the integration tier — the
  crosswalk importer's `GetSCFAnchorBySCFID` resolution and the new resolution
  test run against a real Postgres, exactly where a catalog-data defect belongs.
  This is the correct tier: a catalog-data error is a real-services-resolution
  concern, not a pure-Go unit branch.
- No defect escaped to `unit`, `playwright`, `manual_review`, or `production`.

## Revisit once in use

- When the **full SCF THR-domain import** lands (the real Threat-Management domain
  with all its sub-controls), reconcile THR-01's description against the verbatim
  SCF control text and decide whether the SIEM-rule's advisory anchor should point
  at a finer THR sub-control (e.g. a dedicated detection-rule control) rather than
  the domain head. That is the full-import follow-on, filed as spillover below.
- If a maintainer reviewing the SOC 2 crosswalk judges CC7.2's THR-01 edge should
  be `equal` rather than `intersects_with` (the SIEM-rule family is arguably the
  direct evidence of CC7.2's "monitors … for anomalies indicative of malicious
  acts"), bump the strength — it is a one-line crosswalk edit, not a re-seed.
