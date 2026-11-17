# #2520 — attest dedupe must not swallow a newer attestation (decisions log)

Parent: #2509 (server-derived key with a 5-minute retry window, D2).
No migration. One new read query, `ExistsAttestationAfter`.

- detection_tier_actual: manual_review
- detection_tier_target: integration

## The hole

For a keyless caller the key is `sha256(user, control, body, window)`. In
the order fail, pass, fail inside one window, the second fail maps to the
first fail's record, ingest returns it as a duplicate (200,
`deduplicated: true`), evaluation is skipped, and the control stays
passing. Before #2512 this was a visible 409.

## D1 — a superseded record does not dedupe; the key steps a generation

On a lookup hit for a **server-derived** key, the handler asks, in the same
tenant-GUC read tx, whether the control has an attestation later than the
record under the key. "Later" is the engine's order from
`latestAttestationWins`: `min(observed_at, ingested_at)`, then
`ingested_at`, then id compared bytewise (`COLLATE "C"`, matching Go's
string compare). Control resolution mirrors `ListEvidenceForControlAsOf`
(`control_id = $id OR control_ref = $id::text`).

If one exists, the key steps to `key.1`, then `key.2`, and so on; the first
generation with no record, or whose record is still the latest, is used.
So:

- fail, pass, fail: the second fail lands on `key.1`, is recorded with a
  fresh `observed_at`, and is evaluated. The control ends failing.
- a retry of that second fail walks the same chain (`key` superseded,
  `key.1` latest) and dedupes onto the second fail.
- a plain retry (fail, fail) stops at `key` and dedupes as before.

Rejected: dropping dedupe for a hit whenever any newer record exists,
without a stable key. The repeated request would need a key no retry could
reproduce, and its own retry would then be written again.

Rejected: a key salted with the newer record's id. A retry of the
re-recorded request finds itself as the newest record and would salt with
its own id, a third key.

The chain is bounded at 32 generations per window. Each step needs a newer
attestation between two identical bodies from one user in five minutes; a
person filling the form never reaches it (the form sends its own key
anyway). An API client can reach it, though, so past the bound the handler
answers **409 Conflict** (`errAttestKeyChainExhausted`) telling the caller
to send an `idempotency_key`, rather than loop. A failed lookup is still a 500. Tested by `TestAttestSubmit_KeyChainExhausted_Conflict` (stub reader).

A consequence to know: a genuine network retry of an older keyless
attestation that arrives after a newer one is now recorded, and as the
latest attestation it wins. Without a key the server cannot tell that retry
from a deliberate repeat; a caller that needs the difference sends its own
`idempotency_key`.

## D2 — client-supplied keys keep strict dedupe

A caller that sends `idempotency_key` gets exactly what it asked for: the
same key returns the first receipt even when a newer attestation exists,
and the newer attestation stands. Tested by
`TestAttest_ClientKeyRepeatAfterNewer_StillDedupes`.

## D3 — API docs

The OpenAPI summary for `POST /v1/controls/{id}/attestations` now says API
callers should send their own `idempotency_key` per attestation, and #2509's
D2 says the same.

## D4 — `ingested_at` on the production read path

`evidence_records.ingested_at` is `TIMESTAMPTZ NOT NULL DEFAULT now()`
(`migrations/sql/20260511000000_init.sql`), and both the engine's
`ListEvidenceForControlAsOf` and the handler's idempotency lookup select it,
so a ledger row always carries it. `attestedAt`'s zero fallback serves
fixtures built in Go only. The handler's lookup now fails closed if a row
ever comes back without it, rather than ordering against a zero time.

## D5 — tests

- `TestAttest_KeylessRepeatAfterNewer_IsRecorded` (integration, real
  Postgres): keyless fail, pass, fail in one window ends failing; the
  repeated fail is a new record and is evaluated; its retry dedupes onto it.
  Observed failing on the #2512 handler (it got 200 with
  `"deduplicated":true` where it expected 201) and passing with this change.
- `TestAttest_ClientKeyRepeatAfterNewer_StillDedupes` (integration).
- The existing `TestAttest_RetryWithoutKeyOrObservedAt_Dedupes` still
  passes: a plain retry dedupes.
