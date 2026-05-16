# First audit — AuditPeriod to OSCAL SSP

<!-- Slice 057 shipped the audit-workspace screenshot at
     docs/images/audit-workspace.png. Kept out of the docs-site to
     avoid duplicating ~250 KB; the canonical render is in the README
     "Screenshots" section. -->

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - How to create and freeze an AuditPeriod
    - How sample populations and walkthroughs compose
    - How to export an OSCAL SSP, AP, AR, and POA&M
<!-- prettier-ignore-end -->

This is the end-to-end audit workflow: from creating the period through
the auditor's frozen-horizon read to the OSCAL export bundle the auditor
files. It is the canvas §8 workflow, end to end.

## The audit primitives

| Primitive     | What it does                                                                       |
| ------------- | ---------------------------------------------------------------------------------- |
| `AuditPeriod` | A tenant- and framework-scoped time window with a `frozen_at` horizon              |
| `Population`  | What a sample is drawn from — `(control, scope_predicate, time_window)`            |
| `Sample`      | Deterministic, reproducible draw of N from a population (with `seed`)              |
| `Walkthrough` | Auditor or owner narrative + attachments, hashed and signed                        |
| `Finding`     | A pass/fail/finding annotation on a sample — drives POA&M                          |
| `AuditNote`   | Threaded comment on a control, finding, or sample — the in-product comment surface |

These primitives **compose**. An audit cycle is a graph of populations,
samples, walkthroughs, findings, and notes against the control set —
not a wizard with five fixed steps.

## Step 1 — create an AuditPeriod

```sh
curl -fsS -X POST http://localhost:8080/v1/audit-periods \
  -H "Authorization: Bearer $ATLAS_TOKEN" \
  -d '{
    "name": "SOC 2 2026 Q2",
    "framework_version_id": "<your soc2:v2017 id>",
    "period_start": "2026-04-01T00:00:00Z",
    "period_end":   "2026-06-30T23:59:59Z"
  }'
```

The period starts in state `open`. While open, evidence keeps flowing in
through the [Evidence SDK](https://github.com/mgoodric/security-atlas/blob/main/Plans/EVIDENCE_SDK.md).

## Step 2 — generate sample populations

A population is a query you can sample from later. The most common
shape is "every change record for this control during the period":

```sh
curl -fsS -X POST http://localhost:8080/v1/populations \
  -H "Authorization: Bearer $ATLAS_TOKEN" \
  -d '{
    "control_id": "<your change-management control id>",
    "audit_period_id": "<period id>",
    "scope_predicate": { "env": "prod" },
    "time_window": { "from": "2026-04-01", "to": "2026-06-30" }
  }'
```

Draw a deterministic sample (`seed` is required — same seed reproduces
the same N records):

```sh
curl -fsS -X POST http://localhost:8080/v1/populations/<id>:sample \
  -H "Authorization: Bearer $ATLAS_TOKEN" \
  -d '{"n": 25, "seed": "soc2-q2-cc7-1"}'
```

## Step 3 — walkthroughs and findings

For each control under test, attach a walkthrough — the narrative + any
screenshots or transcripts — and record findings against the sampled
records. Both are first-class objects with append-only audit logs;
nothing is deleted.

```sh
just atlas-cli walkthrough record \
  --period <id> --control <id> \
  --narrative "Quarterly change review demo" \
  --attachment ./change-review.pdf
```

## Step 4 — freeze the period

When the auditor is ready for a fixed evidence universe, freeze the
period:

```sh
curl -fsS -X POST http://localhost:8080/v1/audit-periods/<id>:freeze \
  -H "Authorization: Bearer $ATLAS_TOKEN"
```

After `freeze`:

- Sample populations for this period draw **only** from evidence with
  `observed_at <= frozen_at`.
- Control state for the period is computed against frozen evidence; live
  state continues independently.
- New evidence after `frozen_at` does **not** retroactively change the
  auditor's view.
- A `frozen_hash` is computed from the content (period bounds + framework
  version + sorted evidence + control IDs) — re-freezing the same
  content produces the same hash, so tampering is detectable.

This is the practical answer to **post-window evidence pollution** —
the recurring practitioner complaint about Vanta and Drata's
"continuous" model.

## Step 5 — auditor comments

The auditor leaves comments on controls, samples, or findings; the
auditee replies in-product with attachments. The thread persists as an
audit artifact and exports to OSCAL as `assessment-results` →
`observation` annotations. No email, no Drive links.

## Step 6 — OSCAL export

Four artifacts come out of an audit cycle. Each is OSCAL JSON v1.1.x:

| Artifact           | Generated from                                                                         |
| ------------------ | -------------------------------------------------------------------------------------- |
| SSP                | Org profile + scope cells + applicable controls + implementation narratives + policies |
| Assessment Plan    | Selected sample populations + planned procedures                                       |
| Assessment Results | Sampled evidence + auditor pass/fail/finding annotations                               |
| POA&M              | Open findings with milestones, owners, due dates                                       |

Export the full bundle:

```sh
just atlas-cli oscal-export \
  --period <id> \
  --out ./oscal-soc2-q2-2026/
```

Bundles are signed at export time (sha256 + cosign signature). The
auditor receives a tamper-evident package, not a folder of PDFs.

## Next steps

- [Board reporting →](board-reporting.md) — generate the monthly brief
  and quarterly pack from the same primitives

---

## Was this helpful?

Tell us in [GitHub Discussions](https://github.com/mgoodric/security-atlas/discussions).
