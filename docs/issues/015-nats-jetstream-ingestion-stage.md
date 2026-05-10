# 015 — NATS JetStream evidence buffer + ingestion stage

**Cluster:** Evidence pipeline
**Estimate:** 2d
**Type:** AFK

## Narrative

Insert NATS JetStream as the durable buffer between the push endpoint (slice 013) and the evaluation stage (slice 012). The flow becomes: push API receives → publishes to JetStream stream `evidence.ingest` → ingestion stage consumer reads, canonicalizes, applies redaction rules per evidence_kind, hashes, scope-tags, writes to the ledger. JetStream gives us at-least-once delivery, durable replay (critical for evidence reprocessing), and decouples push acknowledgment latency from downstream evaluation cost. The slice delivers value because high-throughput pushes from CI/CD won't back-pressure the API; a slow downstream doesn't fail the connector.

## Acceptance criteria

- [ ] AC-1: NATS JetStream included in docker-compose (slice 037 consumes this); single binary, ~50MB
- [ ] AC-2: Push endpoint publishes to `evidence.ingest` stream; ack returns immediately after stream commit (not after ledger write)
- [ ] AC-3: Ingestion-stage consumer reads from JetStream, processes the record, writes to ledger
- [ ] AC-4: Replay test: stopping the consumer, pushing 100 records, restarting consumer — all 100 records process exactly once
- [ ] AC-5: Stream retention configured to 7 days for replay capability
- [ ] AC-6: Redaction rules (per `evidence_kind`) applied at ingestion stage — defined as JSONPath expressions in the schema-registry entry

## Constitutional invariants honored

- **Invariant 2 (ingestion/eval separated):** ingestion stage canonicalizes/redacts/writes; evaluation is downstream and read-only
- **Invariant 3 (two SDK profiles):** both connector pull and pusher push converge on the same JetStream subject

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.3 (ingestion vs evaluation stages diagram)
- `Plans/canvas/09-tech-stack.md` §9.3 (NATS JetStream rationale)
- `Plans/EVIDENCE_SDK.md` §4.6 (rate limiting + back-pressure)

## Dependencies

- #013

## Anti-criteria (P0)

- Does NOT skip the ingestion stage (push → ledger direct write is not permitted)
- Does NOT log full payloads if redaction rules are configured
- Does NOT permit at-most-once delivery (must be at-least-once with idempotency)

## Skill mix (3–5)

- NATS JetStream client (Go)
- Stream consumer patterns
- Postgres transactional writes
- JSONPath for redaction rules
- Replay/idempotency testing
