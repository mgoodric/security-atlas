# #2815: S3 connector payload vs the registered bucket-encryption schema (decisions log)

Parent: #1860 (ADR 0028). Unblocks #2825, the cc6_7 SQL payload check,
which reads the keys settled here. No migration.

- detection_tier_actual: manual_review
- detection_tier_target: unit

## D1: the connector was wrong, not the schema

The mismatch was real. Before this change `buildRecord` emitted
`bucket_arn`, `bucket_name`, `bucket_region`, `algorithm`, `kms_key_id`,
`bucket_key_enabled` and `reason` at schema `1.0.0`. The registered
`aws.s3.bucket_encryption_state/1.0.0.json` requires `bucket_name` and
`encryption_enabled`, and sets `additionalProperties: false`. Run through
the validator ingest uses, every record the connector built was rejected:

```
schema aws.s3.bucket_encryption_state.v1/1.0.0 rejected payload: jsonschema validation failed
- at '': missing property 'encryption_enabled'
- at '': additional properties 'algorithm', 'bucket_arn', 'bucket_region', 'kms_key_id', 'reason' not allowed
```

The connector (slice 004, `423febee`) was written about four hours before the
schema registry (slice 014, `44718c96`, both on 2026-05-11). The schema is
what was published: the registry, the docs and the SOC 2 bundles all
reference it, and #2825 will read its keys. So the connector changes to fit
the schema, rather than the schema being redefined around the connector's
names.

Nothing caught it because the connector's push test runs against
`api.New`'s in-memory registry, which checks only that `(kind, version)` is
registered. Only the DB-backed `ingest.Service` validates payloads.

## D2: a 1.1.0 for the two fields 1.0.0 has no place for

Two things the connector reports have no 1.0.0 key:

- `kms_key_id`. AWS's `KMSMasterKeyID` may be a key ID, an alias or an ARN.
  Writing an alias into 1.0.0's `kms_key_arn` would mislabel it.
- `reason`, which says why a result is inconclusive (the AWS error).
  Dropping it would leave an operator with nothing to act on.

`1.1.0.json` adds both as optional strings. Nothing else changes, and
`CheckAdditiveOver(1.0.0, 1.1.0)` passes, so it stays kind `.v1`, the
control bundles need no edit, and 1.0.0 stays registered. The connector now
pushes `schema_version: 1.1.0` (`SchemaVersion` in `root.go`). `DefaultSeed`
lists 1.1.0 so the in-memory fallback accepts it too. The connector never
fills `kms_key_arn`, because it cannot tell a key ARN from an alias ARN
without a KMS call.

`bucket_arn` is dropped from the payload. It is `arn:aws:s3:::<bucket_name>`
and still keys the idempotency hash. `bucket_region` becomes `region`, and
`account_id` (from the assumed identity) is now filled in.

## D3: encryption_enabled on an inconclusive record is false

1.0.0 requires `encryption_enabled`, and an additive version cannot make it
optional. The connector sets it to `result == pass`, so for an inconclusive
record it is `false`, meaning "not observed", with `reason` saying why. The
record's `result` column is the verdict. 1.1.0's description of the field
says so. #2825's SQL check should therefore read `result` (or
`encryption_enabled` together with `sse_algorithm`), and should not treat
`encryption_enabled = false` alone as proof that a bucket is unencrypted.

## D4: tenant data, none affected

Every push from the connector to a platform running the DB-backed ingest
stage (`cmd/atlas` wires it whenever it has a pool and the schema registry)
was refused at `ValidatePayload`. The refusal wrote a
`rejected_validation` audit row, and the connector exited non-zero on the
first bucket. No ledger row in the connector's shape can exist, so nothing
needs a backfill or a correction. The cost was missing evidence: no tenant
had connector-sourced S3 encryption records for CRY-07 / cc6_7.

This is derived from the code path. I have not queried any running
deployment's ingest audit log for those rejections.

## D5: the test is a unit test and needs no database

`schemaregistry.NewEmbedded(PlatformSchemasFS())` is a Service with no pool,
holding only the bundled schemas. It is the same type, compile step
(`addGlobalLocked`, now shared with `LoadFromDB`) and `ValidatePayload` that
ingest calls. The connector's `schema_conformance_test.go` builds records
with the real `buildRecord`, marshals the payload with `protojson` as
`ingest.go` does, and validates them for pass (AES256, aws:kms), fail and
inconclusive. It also pins the legacy key set as rejected. It runs in
`go test ./...`, so CI's `Go · build + test` gates it.

A lookup that would reach the database (a tenant-private kind) returns
`ErrUnknownKind` when there is no pool, so it does not dereference a nil
pool.

## Out of scope, noted

`internal/demoseed/fixtures.go` writes S3 demo evidence with yet another key
set (`bucket`, `kms_key_id`, `bucket_versioning`, and no `bucket_name` or
`encryption_enabled`). That writer bypasses ingest by design, so the rows
land, but #2825's SQL check will not find its keys in them. It needs a
separate ticket.
