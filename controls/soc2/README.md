# SOC 2 stock control kit

This directory contains the v1 stock SOC 2 control library — 50 SCF-anchored
controls authored in the slice-009 bundle format. The kit ships with a fresh
security-atlas deploy so the solo security leader can run their SOC 2 program
out of the box without authoring controls from scratch.

## Provenance

- **Slice:** 010 (control-as-code · SCF-anchored SOC 2 control kit)
- **TSC version:** 2017 (with 2022 points-of-focus revisions)
- **SCF crosswalk:** `data/crosswalks/soc2-tsc-2017.yaml` (slice 007)
- **SCF anchor catalog:** `migrations/fixtures/scf-sample.json` (slice 006)
- **HITL review log:** `docs/audit-log/control-kit-review.md`

## Architectural invariants honored

- **Invariant 1 (one control, N framework satisfactions).** Each bundle
  references exactly one `scf_anchor_id`. SOC 2 satisfaction is derived at
  query time via the slice-008 graph traversal (control → SCF anchor → STRM
  edge → TSC requirement). The bundles do not encode framework mappings
  directly; that would duplicate the crosswalk and invite drift.
- **Invariant 7 (SCF canonical).** Every bundle is anchored to an SCF
  concept; the parser rejects bundles missing `scf_anchor_id`.
- **Invariant 9 (manual evidence first-class).** Manual controls have full
  lifecycle metadata (`owner_role`, `freshness_class`, `manual_evidence_schema`)
  and render in the UI on equal footing with automated ones.

## Bundle layout

Each control is a directory:

```
controls/soc2/<bundle_id>/
├── control.yaml      (the manifest — required)
└── description.md    (long-form description — optional)
```

The bundle parser (`internal/control/parser.go`) loads `control.yaml`,
unmarshals it into `Manifest`, runs `ValidateStructural()`, and validates the
`applicability_expr` against the slice-017 scope predicate validator.
`evidence_kind` references are validated against the schema registry at
upload time (`internal/control/validate.go::ValidateEvidenceKinds`).

## Control inventory

| Count | Implementation type | Notes                                                             |
| ----- | ------------------- | ----------------------------------------------------------------- |
| 24    | `automated`         | Rego or SQL queries over the ledger; freshness ≤ daily            |
| 4     | `semi_automated`    | Mixed — automated detection + manual attestation closeout         |
| 11    | `manual_periodic`   | Owner uploads evidence on schedule (annual / quarterly / monthly) |
| 11    | `manual_attested`   | Roleholder asserts state digitally on schedule                    |

Total: **50** controls. TSC coverage: **43 / 43** crosswalked TSC codes
satisfied (100% of the SCF-crosswalkable universe; matches the slice 007
crosswalk's mapping table).

## TSC family breakdown

| TSC family                       | Count | Range                                  |
| -------------------------------- | ----- | -------------------------------------- |
| CC1 — Control environment        | 5     | CC1.1, CC1.2, CC1.3, CC1.4, CC1.5      |
| CC2 — Communication              | 3     | CC2.1, CC2.2, CC2.3                    |
| CC3 — Risk assessment            | 4     | CC3.1, CC3.2, CC3.3, CC3.4             |
| CC4 — Monitoring                 | 2     | CC4.1, CC4.2                           |
| CC5 — Control activities         | 3     | CC5.1, CC5.2, CC5.3                    |
| CC6 — Logical & physical access  | 11    | CC6.1–CC6.8 (CC6.3 and CC6.6 have 2)   |
| CC7 — System operations          | 6     | CC7.1, CC7.2, CC7.3, CC7.4, CC7.5      |
| CC8 — Change management          | 2     | CC8.1 (CHG-02 + CFG-04)                |
| CC9 — Risk mitigation            | 3     | CC9.1, CC9.2                           |
| A1 — Availability                | 3     | A1.1, A1.2, A1.3                       |
| C1 — Confidentiality             | 2     | C1.1, C1.2                             |
| PI1 — Processing integrity       | 5     | PI1.1–PI1.5                            |
| **Total**                        | **50** |                                       |

## Forking

These bundles are stock — they're starting points. Real deployments will
fork them: tighten an `applicability_expr` for your environment, add a
`linked_policy_ids` reference once your policy library lands, swap a
manual control to automated when a connector matures. The bundle format
is designed to make `git diff` legible so forks remain reviewable.
