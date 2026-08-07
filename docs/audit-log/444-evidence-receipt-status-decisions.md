# OE-444 — Evidence receipt terminal-status surface decisions

**Type:** JUDGMENT · **Date:** 2026-08-04

## D1 — Add a credential-scoped receipt lookup endpoint

Decision: add `GET /v1/evidence/receipts/{record_id}` as the pusher-visible
status surface. It reads the latest `evidence_audit_log` row for the
authenticated `(tenant_id, credential_id, record_id)` and returns the terminal
ingest decision plus `reason_code`.

Why: push receipts already carry `record_id`, and `evidence_audit_log` is the
existing append-only decision ledger for accepted, deduplicated, and rejected
pushes. A read surface over that table closes the gap without making
`POST /v1/evidence:push` synchronous or moving validation ahead of the NATS
stream-commit ack.

Security boundary: the query is scoped by tenant and credential. A receipt from
another credential returns `404`, the same shape as a receipt that has not yet
reached a terminal consumer decision, so the endpoint does not leak cross-
credential existence.

## D2 — Use the publish-time receipt ID as the consumer-side record ID

Decision: the JetStream publisher writes the deterministic receipt `record_id`
into NATS headers, and the consumer passes it to ingest. Ingest uses that UUID
for the final ledger row when accepted and for the audit row when terminally
rejected before any ledger insert.

Why: before this change, stream-commit receipts used a deterministic
publish-time UUID, while the consumer generated a separate random ledger UUID.
That split made a receipt-status endpoint awkward and would have forced either
idempotency-key polling or rejection-only status. Using one ID makes the receipt
the durable handle for the pushed record.

## D3 — Rejected alternatives

- `GET /v1/evidence` absence inference: rejected because absence cannot
  distinguish "not yet consumed" from "terminally rejected".
- Per-credential rejected-records feed: useful later, but it does not give CI
  jobs a direct answer for the receipt they just got.
- Webhook or notification kind: useful later for high-volume connectors, but it
  adds delivery, retry, and subscription state before the basic polling
  contract exists.
- Make `POST /v1/evidence:push` validate synchronously: rejected because slice
  015 intentionally acks at stream commit and the ingestion/evaluation split
  depends on that asynchronous boundary.
