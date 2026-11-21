# 2873 — Only the acknowledgement handler writes `policy.acknowledgment.v1`: decisions log

Issue: https://github.com/mgoodric/security-atlas/issues/2873 (follow-up from
#2817, PR #2869; the push-trust item that log's "Not changed" section filed).
Part of the ADR 0028 rows 11-13 thread (cc1_5, cc5_3, cc7_4).

- detection_tier_actual: manual_review
- detection_tier_target: integration

Since #2869 an unbound `policy.acknowledgment.v1` record (`control_id IS NULL`)
is evidence for every control whose queries declare the kind. Before this
change any push credential could send one through `POST /v1/evidence:push` or
the gRPC `Push`, and it landed. With the guard disabled,
`TestPush2873_GenericPushRefusesAckKind` shows it:

```
POST /v1/evidence:push: status 201 (want 403)
gRPC push of policy.acknowledgment.v1: got <nil>, want PermissionDenied naming the kind
after refused pushes: 2 policy.acknowledgment.v1 records, want 0
```

## D1 — Refuse the kind on every path but the handler's, rather than discount it by provenance

- **Options considered:**
  (a) refuse the kind in ingest unless the Service copy was built for it;
  (b) let it land, tag the handler's records, and count only tagged ones in
  every unbound reader.
- **Chosen:** (a).
- **Rationale:** (b) adds a predicate to each of the readers #2817 touched
  (evaluation, evidence list, summaries, freshness, period state, population,
  auditor lookup), and a reader that misses it counts forged records again. (a)
  is one check at the one write path, and a forged record never reaches the
  ledger. The ticket allows either.

## D2 — The check lives in `ingest.prepare`, keyed by `unbound.Kinds()`

- `ingest.OwnedKind(kind)` is true for exactly the unbound kinds. Those are the
  kinds whose unbound records count tenant-wide, so they are the ones a push
  could forge for many controls. A new unbound kind is owner-only from the day
  it is added, without a second list to keep in step.
- `prepare` runs for `Process`, `ProcessWithRecordID` (the JetStream consumer)
  and `ProcessInTx`. Every generic path refuses the kind: HTTP push, gRPC
  push, the stream consumer, and MCP write-proposal appliers.
- The refusal is `ErrOwnedKind`, which wraps `ErrScopeViolation`, so HTTP
  answers 403 `scope_violation` and gRPC `PermissionDenied` with no new
  mapping. The audit row is `rejected_scope_violation`; the decision CHECK
  constraint needed no migration.
- The check runs before the observed_at, size and schema checks: who may send
  the kind is decided before what it contains.

## D3 — The handler opts in with `WithOwnedKind`, a Service copy like `WithPath`

- `policyacks.Handler.Acknowledge` builds
  `ingester.WithPath(IngestionPath).WithOwnedKind(EvidenceKind)`. No credential
  field grants the kind, so no API key, JWT or system credential can be
  issued that pushes it.
- With the opt-in removed, `TestAcknowledge2817_ReachesStockAckControls` and
  the new test's handler half fail (`want 1 unbound policy.acknowledgment.v1
record, got 0`), so the handler's path is pinned.

## D4 — The JetStream publish path refuses synchronously

- With a publisher wired, the HTTP push answers before the consumer runs
  `Process`, so the pusher would get a receipt for a record the consumer then
  refuses. `HTTPHandler.dispatch` calls `Service.RefuseOwnedKind` first. It
  writes the same `rejected_scope_violation` audit row (credential id, kind,
  idempotency key) that `Process` does, so a forgery attempt is audited on
  either topology (review of #2909). The consumer still refuses the kind if a
  message carrying it arrives some other way.

## D5 — `WithOwnedKind` panics on a kind that is not owner-only

- Granting an ordinary kind is a wiring mistake, and a misspelled owner kind
  would leave the handler unable to write its own records. Both fail at
  startup instead of on the first acknowledgement (review of #2909).

## Tests

- `internal/evidence/ingest/owned_kind_test.go` (unit, pool-free): a generic
  Service refuses the kind with `ErrOwnedKind`/`rejected_scope_violation`; a
  `WithOwnedKind` copy passes the check; other kinds are unaffected;
  `RefuseOwnedKind` agrees on a built and a nil Service; `WithOwnedKind`
  panics on an ordinary kind.
- `internal/api/evidence/owned_kind_publish_2873_integration_test.go`: with a
  publisher wired, the forged push gets 403 `scope_violation`, the publisher
  receives nothing, and one `rejected_scope_violation` audit row names the
  credential, kind and idempotency key. With the refusal made audit-free, the
  row count was 0.
- `internal/api/policyacks/push_refused_2873_integration_test.go`: HTTP push
  gets 403, gRPC push gets `PermissionDenied`, no record lands, then an
  acknowledgement through the handler writes one. The #2817 test still shows
  that record passing cc1_5, cc5_3 and cc7_4.
- Each assertion was observed failing first: the refusals with the `prepare`
  check disabled, the handler half with `WithOwnedKind` removed.
