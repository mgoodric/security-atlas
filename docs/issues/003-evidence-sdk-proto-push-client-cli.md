# 003 — Evidence SDK: proto + Go push client + CLI

**Cluster:** Spine
**Estimate:** 2.5d
**Type:** AFK

## Narrative

Define the gRPC contract for the Evidence SDK in `proto/evidence/v1/`: `IngestEvidence` RPC accepting the `EvidenceRecord` message with all mandatory fields (idempotency_key, evidence_kind, schema_version, control_id, scope, observed_at, result, payload, source_attribution). Generate Go bindings; implement a stub server that validates required fields and returns receipts. Build `pkg/sdk-go` as a clean wrapper around the generated client. Build the `cmd/atlas-cli` binary exposing `security-atlas evidence push --kind=... --control=... --scope=... --payload=@file.json --idempotency-key=...`. Slice delivers value because any developer or CI script can push a fake evidence record into a running atlas and receive a signed receipt back.

## Acceptance criteria

- [ ] AC-1: `proto/evidence/v1/evidence.proto` compiles; Go code generation produces consumable types
- [ ] AC-2: `security-atlas evidence push --kind=foo.v1 --control=SCF:TEST-01 --scope='{"env":"dev"}' --observed-at=2026-05-10T12:00:00Z --result=pass --payload='{"test":true}' --idempotency-key=test-1` returns a receipt with the record id and hash
- [ ] AC-3: Missing required field (e.g., omit `--idempotency-key`) is rejected at the client before round-trip
- [ ] AC-4: Server stub rejects (with explicit error) any record where `evidence_kind` isn't registered in the schema registry (mocked at this point)
- [ ] AC-5: Re-sending the same `idempotency_key` returns the original receipt without writing a duplicate (mock dedup)
- [ ] AC-6: Receipt includes sha256 of canonical record form

## Constitutional invariants honored

- **Invariant 3 (Two SDK profiles):** the pusher profile is established here; connector profile (slice 004) consumes it
- **Anti-criterion (provenance/idempotency/schema validation on ingest):** all three are required at the contract level

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.1 (SDK contract)
- `Plans/EVIDENCE_SDK.md` §4.1–4.5 (push API surface, push record schema, auth, idempotency, schema registry)

## Dependencies

- #001

## Anti-criteria (P0)

- Does NOT accept anonymous push (must surface auth even if mocked)
- Does NOT accept schemaless push
- Does NOT accept scope-less push
- Does NOT auto-create unregistered `evidence_kind`s

## Skill mix (3–5)

- gRPC + protobuf
- Go (cobra/urfave for CLI)
- sha256 canonical hashing
- Idempotency key handling
- proto schema design
