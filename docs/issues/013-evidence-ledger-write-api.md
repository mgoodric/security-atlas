# 013 — Evidence ledger write API + push endpoint

**Cluster:** Evidence pipeline
**Estimate:** 3d
**Type:** AFK

## Narrative

Implement the canonical inbound API for the evidence ledger: `POST /v1/evidence:push` (REST, batch ≤100) + `Push(stream<EvidenceRecord>)` (gRPC streaming). Both wrap the same internal `IngestEvidence` call into the **ingestion stage** — a logically separate function path (`internal/evidence/ingest`) that canonicalizes, hashes (sha256 of canonical form), schema-validates against the schema registry (slice 014), checks for idempotency (replay protection within 24h window), scope-tags, and writes to the append-only `evidence_records` table. In this slice the ingestion stage is invoked **synchronously** from the API handler; slice 015 swaps in NATS JetStream as the durable buffer between API and ingestion stage without modifying the ingestion-stage function itself. The logical separation between API and ingestion honors invariant 2 from day one — slice 015 is a substrate swap, not a separation introduction. Every write produces a signed receipt with the hash. Records that fail any check are rejected with explicit errors and logged to the audit log. The slice delivers value because connectors (slice 004+), the CLI (slice 003), and middleware can all push real evidence and see it land queryable.

## Acceptance criteria

- [ ] AC-1: `POST /v1/evidence:push` with a valid record (auth + provenance + schema + idempotency_key) returns 201 with `{record_id, hash, received_at}` receipt
- [ ] AC-2: Missing any required field (provenance, idempotency_key, evidence_kind, schema_version, control_id, scope, observed_at, result) returns 400 with the missing field named
- [ ] AC-3: Re-pushing same idempotency_key within 24h returns the original receipt, no duplicate row
- [ ] AC-4: Re-pushing same idempotency_key with different content is rejected (409)
- [ ] AC-5: Records exceeding the rate limit (default 100/s per credential) return 429 with `Retry-After`
- [ ] AC-6: Records with `payload > 1MB` redirect the payload to S3 (slice 036); `payload_uri` set
- [ ] AC-7: Audit log entry for every push attempt — accepted or rejected — keyed by credential id
- [ ] AC-8: `observed_at` more than 24h skewed from `received_at` is rejected (replay protection)
- [ ] AC-9: Ingestion-stage logic lives in a separate `internal/evidence/ingest` package; the HTTP/gRPC handler calls `ingest.Process(ctx, record)` rather than writing the ledger directly. Slice 015 will swap this synchronous call for a NATS publish without modifying the ingestion package — verified by an integration test that asserts the package boundary holds before and after slice 015

## Constitutional invariants honored

- **Invariant 2 (ingestion/eval separated):** the API writes to the ledger; evaluation never invoked here
- **Invariant 3 (two SDK profiles):** push profile of the SDK — connector profile (slice 004) also lands here
- **Invariant 6 (RLS):** all writes tenant-scoped via credential context
- **Anti-criterion (no anonymous/schemaless/scope-less push):** all three reject paths verified by integration tests
- **Anti-criterion (provenance, idempotency, schema validation):** all three are mandatory at the endpoint

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.1 (Evidence SDK)
- `Plans/EVIDENCE_SDK.md` §4 (push API surface, schema, auth, idempotency, rate limits, threat model)

## Dependencies

- #002, #003, #014

## Anti-criteria (P0)

- Does NOT accept records without all required provenance fields
- Does NOT accept records without schema-registry-validated payload
- Does NOT accept records without an idempotency_key
- Does NOT skip the audit log on rejection
- Does NOT permit cross-tenant writes (credential scope check)

## Skill mix (3–5)

- Go HTTP + gRPC handlers
- Postgres append-only writes with sqlc
- Schema validation (JSON Schema)
- sha256 canonical hashing
- Rate limiting (token bucket per credential)
