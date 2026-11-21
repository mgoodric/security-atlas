# 2863 — Demo-seed evidence matches the registered schemas: decisions log

Issue: mgoodric/security-atlas#2863. Follows #2815 / PR #2858.

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found while fixing the AWS
  connector's S3 payload in #2858. All twelve demo kinds failed their
  registered schema, not only S3.
- **detection_tier_target:** unit. `writeEvidence` bypasses ingest (the
  #1830 exception), so a pure-Go test running the seed's payloads through
  `schemaregistry.ValidatePayload` is the only check that can see them.
  `TestDemoEvidencePayloads_MatchRegisteredSchemas` is that test.

## Decisions

### D1 — demo rows use the registered kind spelling, with `.v1`

The registry keys schemas by `x-evidence-kind`, which is
`<family>.v1` (`aws.s3.bucket_encryption_state.v1`). The connectors push
that spelling, so real ingest stores it. The seed wrote the bare family
name, which the validator does not know and which a kind filter such as
#2825's cc6_7 check would not match. The pool now uses the `.v1` names.
Nothing outside `internal/demoseed` read the bare names (grep of
`internal/`, `cmd/`, `web/`, `scripts/`, `isa/`).

Upgrade note: demo tenants seeded before this change keep the old rows.
`atlas-cli demo teardown` then `demo seed` replaces them.

### D2 — schema_version is pinned per kind in one table

`evidenceSchemaVersions` holds the version each kind is written at: the
latest registered version for each (S3 1.1.0 from #2858, manual.attestation
1.2.0, the rest 1.0.0). `writeEvidence` stamps it on
`evidence_records.schema_version`, and the tests validate at that version,
so a payload and its stamped version cannot disagree.

### D3 — payloads that state a verdict agree with the row's result

`buildEvidencePayload` takes the row's result. An S3 `pass` (and `na`)
row shows `encryption_enabled: true` with `AES256` or `aws:kms`; a `fail`
row shows `encryption_enabled: false` and no algorithm; `inconclusive`
adds a `reason`, as the connector does. A `fail` manual attestation sets
`result: fail` with a `fail_reason`. The algorithm alternates per pool
cycle, because every S3 row sits at the same index modulo the pool length.

### D4 — the UI render check was not run end to end

The ticket asks to re-run the demo-seed e2e and see the S3 rows render.
The only demo-seed spec, `web/e2e/admin-demo.spec.ts`, mocks the admin
API and renders no evidence rows, so it cannot show this. Instead the
real seed (`atlas-cli demo seed --tenant-slug …`) was run against a
scratch Postgres and the stored S3 rows were read back; see the PR body.
