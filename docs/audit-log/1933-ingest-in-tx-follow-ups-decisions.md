# #1933 — ingest in-transaction follow-ups — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/1933 (from the
#1932 review of #1830). Builds on
[`1830-ledger-writes-through-ingest-decisions.md`](1830-ledger-writes-through-ingest-decisions.md).

- **`detection_tier_actual`: `manual_review`** (the #1932 review).
- **`detection_tier_target`: `integration`** for the early sink emit (only a
  rollback against real Postgres shows it); `unit` for the guard regex gaps,
  the provenance label and the incident-close status mapping.

## Decisions

### D1 — Sink events from `ProcessInTx` wait in a context-carried outbox. **Confidence: HIGH.**

`insertAudit` used to emit to the external audit sink as soon as the
`evidence_audit_log` row was inserted. Under `ProcessInTx` that row sits in
the caller's transaction, so a rollback left an "accepted" sink event for a
record that never existed. pgx has no commit hook, so the event has to wait
somewhere the caller controls.

`insertAudit` now returns the event instead of emitting it. `writeAudit`
emits after its own transaction commits. `ProcessInTx` appends to an
`ingest.AuditOutbox` carried on `ctx` (`ingest.WithAuditOutbox`); each domain
store's transaction helper opens one and calls `Flush` only after `Commit`
succeeds. A rollback drops the outbox.

Context rather than a new `ProcessInTx` return value: the stores call
`ProcessInTx` several frames below their transaction helper, and the helper
is the only place that knows when the commit happened. Threading a return
value up through each transition would touch every caller for no gain.

`ProcessInTx` refuses a `ctx` with no outbox (`ErrNoAuditOutbox`) before it
does any work. The alternative, emitting immediately when no outbox is
present, would bring back this defect for any new caller that forgets.

### D2 — No second connection on the accepted path. Refusals keep theirs. **Confidence: MEDIUM.**

The review asked about pool pressure: the schema and redaction lookups and
the refusal audit took a second pooled connection while the caller held one.

- **Schema lookups.** The domain-writer kinds are platform schemas. The
  registry loads them into memory at boot (`ImportPlatformSchemas`), and
  `IsRegisteredForTenant`, `ValidatePayload` and `RedactionRulesFor` answer
  global kinds from that cache. They reach the database only for a
  tenant-private kind, which no domain writer uses. No change.
- **Refusal audit.** It has to use its own connection: the row must outlive
  the caller's rollback (the #1751 contract). `auditWriteTimeout` (3s) already
  bounds the acquire, so a pool exhausted by callers each holding a
  transaction delays a refusal and may drop its audit row, but cannot
  deadlock. Kept, and documented on `ProcessInTx`.

Moving the lookups onto the caller's transaction would mean a transaction
parameter on the `SchemaValidator` interface for a path that does not hit
the database. Not done.

### D3 — `credential_type: "system"` from a flag on the credential. **Confidence: HIGH.**

`credstore.Credential` gains `System bool`, set only by
`ingest.SystemCredential`, never persisted. Provenance records `"system"`
for it and `"api_key"` otherwise. Inferring it from a `system:` id prefix
would miss the access-review writer, whose id is `access_review_campaign`,
and would let any credential id that happened to start with `system:` claim
the label.

### D4 — Guard: function-scoped exception, wider matcher, sqlc scan. **Confidence: HIGH.**

`TestNoDirectEvidenceLedgerWrites` now:

- matches any `.InsertEvidenceRecord*(` call, `INSERT INTO` or `COPY` into
  `evidence_records` bare, quoted or schema-qualified, and
  `pgx.Identifier{"evidence_records"}` with or without a schema element;
- scopes the demoseed exception to `writeEvidence` (found with `go/ast`), so
  a second raw insert elsewhere in `writers.go` fails;
- scans `internal/db/queries/*.sql` and fails on any named query other than
  `InsertEvidenceRecord` that writes the ledger.

`TestDirectLedgerWritePattern`, `TestSQLLedgerWriteQueries` and
`TestLedgerWriteHitsNameEnclosingFunc` pin each part.

### D5 — `main.go` wires ingest into both personnel-security stores. **Confidence: HIGH.**

The worker-event subscriber and the overdue notifier never complete an item
today, so they never reach `ProcessInTx`. Wiring `WithIngest(ingestSvc)`
anyway means a later change that completes items from either path writes
evidence instead of failing with `ErrUnavailable`. That is cheaper than a
construction-time check whose only effect would be a different error.

### D6 — Incident close maps ingest refusals to 4xx. **Confidence: MEDIUM.**

| Ingest error                       | Status |
| ---------------------------------- | ------ |
| `ErrValidation`, `ErrMissingField` | 400    |
| `ErrScopeViolation`                | 400    |
| `ErrIdempotencyMismatch`           | 409    |

`ErrScopeViolation` is 400 rather than the push API's 403. Here the
credential is the platform's own, so "forbidden" would describe the caller
wrongly. `ErrMissingField` was also returning 500 and is mapped with
`ErrValidation`.
