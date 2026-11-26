# Onboarding a past audit

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - How to load last year's audit results with their real dates
    - The order that needs no amendment: create the period, import, freeze
    - How to add evidence to a period you already froze, as a new freeze revision
<!-- prettier-ignore-end -->

Live ingest refuses any `observed_at` more than 24 hours from the time the
platform receives the record. That is replay protection for connectors, and
it stays on for every push, pull, subscribe, webhook, attestation and upload.
Evidence from a past period (last year's SOC 2 results, history from a
spreadsheet or another GRC tool) goes in through the historical import
instead, which keeps each record's real date.

Loaded this way, prior-year evidence reads as stale, because it is. Loaded as
a baseline attestation dated today, it would read as 100% fresh and would land
in the current audit period.

## What the import does

- **Admin only.** `POST /v1/evidence:import` refuses any credential that is
  not a tenant admin with `403`, before it reads the request. Connector and
  push credentials cannot reach it.
- **All or nothing.** The batch commits in one transaction. If any record is
  refused, nothing is written. The response names the refused record, and it
  is the one that gets an audit-log row: the records before it roll back with
  the batch, and the records after it are never tried. A batch refused as a
  whole (a frozen-period conflict) audits every record.
- **One at a time with freezes.** An import and a freeze in the same tenant
  wait for each other. If a period is frozen while an import is starting, the
  import retries; if that keeps happening it fails with `concurrent_freeze`
  and writes nothing, and you can run it again. A `concurrent_freeze`
  refusal writes no evidence audit-log row: no record was processed.
- **The normal pipeline.** Every record is schema-validated, kind-scoped,
  redacted and hashed like a pushed record. The past skew limit is lifted;
  a record more than 24 hours in the **future** is still refused.
- **Marked as imported.** Each record has `ingestion_path = import` and its
  provenance names the batch (`import_batch_id`), your `source` text and the
  importer. Every accepted and refused attempt writes an evidence audit-log
  row tied to the batch.
- **Written now.** A record's `ingested_at` is the time of the import, never
  its `observed_at`. A replay of an earlier date, or a period frozen before
  the import, does not see it.

## The file

JSON Lines: one record per line, in the envelope `POST /v1/evidence:push`
accepts. For a prior-year result, `manual.attestation.v1` schema `1.3.0`
carries the period the attestation covers and the report it came from:

```json
{
  "idempotency_key": "fy25-cc6-1",
  "evidence_kind": "manual.attestation.v1",
  "schema_version": "1.3.0",
  "control_id": "<control id>",
  "scope": [{ "key": "environment", "values": ["prod"] }],
  "observed_at": "2025-12-15T00:00:00Z",
  "result": "pass",
  "payload": {
    "attestor": "ciso@example.com",
    "statement": "Quarterly access reviews completed for FY25",
    "result": "pass",
    "period_start": "2025-01-01",
    "period_end": "2025-12-31",
    "report_reference": "FY25 SOC 2 Type II report"
  },
  "source_attribution": {
    "actor_type": "human",
    "actor_id": "ciso@example.com"
  }
}
```

`period_start`, `period_end` and `report_reference` are optional. A `1.1.0`
or `1.2.0` attestation still validates.

## Recommended order: create, import, freeze

This order needs no amendment, and the period's single freeze covers the
imported evidence.

1. **Create the past audit period** with its real window, for example
   2025-01-01 to 2025-12-31. Do not freeze it yet.

   ```sh
   curl -fsS -X POST "$ATLAS_ENDPOINT/v1/audit-periods" \
     -H "Authorization: Bearer $ATLAS_TOKEN" \
     -d '{"name":"FY25 SOC 2","framework_version_id":"<id>","period_start":"2025-01-01T00:00:00Z","period_end":"2025-12-31T00:00:00Z"}'
   ```

2. **Import its evidence** while the period is open. Records dated inside an
   open period join it like any other evidence.

   ```sh
   atlas-cli evidence import --historical fy25.jsonl \
     --source "FY25 SOC 2 Type II report"
   ```

3. **Freeze the period.** The freeze hash now covers the imported records.

   ```sh
   curl -fsS -X POST "$ATLAS_ENDPOINT/v1/audit-periods/<id>/freeze" \
     -H "Authorization: Bearer $ATLAS_TOKEN"
   ```

## A period you already froze: `--amend-frozen`

If a record's `observed_at` falls on a day inside a **frozen** period
(between its `period_start` and `period_end`) and at or before the period's
`frozen_at`, the record belongs to the frozen population. The import fails
before anything is written and names each such period with its record count.
A record in the window but observed after `frozen_at` (a period frozen before
its window ended) is outside the frozen population, as live evidence is, and
imports without an amendment.

```text
import failed: nothing was written. Records fall in frozen audit periods:
  7c1f…  FY25 SOC 2  (42 records)
Re-run with --amend-frozen <id> for each period and --amend-reason "<why>" to re-freeze them as a new revision.
```

To add the evidence anyway, name each period and say why. The reason is
required (`amend_reason` in the API); it is stored on the revision and the
period's audit log, and shown to anyone reading the period or its samples:

```sh
atlas-cli evidence import --historical fy25.jsonl \
  --source "FY25 SOC 2 Type II report" \
  --amend-frozen 7c1f… \
  --amend-reason "Auditor asked for the FY25 Type II results in this period"
```

Every frozen period the batch touches must be named; one left out still fails
the whole import. A named period the batch does not touch is refused too.

Each named period is re-frozen as a **new freeze revision**:

- The original freeze is unchanged. `frozen_at` and `frozen_hash` on the
  period stay what they were, and revision 1 can still be replayed.
- The new revision's population is the previous one plus the batch's records
  inside the period window and observed at or before the original
  `frozen_at`. Its hash (`revision_hash`) is computed the same way as the
  original (ADR 0003) over that population.
- The period's audit log gets a `period_amended_by_import` row naming the
  importer, the batch, the reason, the previous hash and the new hash.
- `GET /v1/audit-periods/{id}` returns `freeze_revision`, `revision_hash`,
  `amended_by_import: true`, `amend_reason` and the list of
  `freeze_revisions`. The audit period page shows **Amended by import** with
  the revision, its hash and the reason.
- Control state, samples drawn from the period's populations, the frozen
  evidence summary and auditor access all read the latest revision. A sample
  drawn before the amendment keeps the records it drew.
- Each sample records the revision it was drawn from (`freeze_revision`, also
  on its `sample_drawn` log row), so the same seed reproduces it. A sample
  drawn from an amended revision shows the amendment notice and reason.
- The OSCAL export bundle's `manifest.json` carries `freeze_revision`,
  `revision_hash`, `amended_by_import` and `amend_reason`. The revision and
  its hash are covered by the bundle signature, so an export taken before an
  amendment and one taken after are told apart. `amend_reason` and
  `amended_by_import` are informational: they are not signed, and the
  period's audit log is the record of the reason.

Tell your auditor before you amend a period they are already working in:
the population they sample from grows.
