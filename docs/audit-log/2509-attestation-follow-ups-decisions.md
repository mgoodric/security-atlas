# #2509 — attestation follow-ups from #2111's review (decisions log)

Parent: #2111 (latest attestation wins). No migration, no schema change.

- detection_tier_actual: manual_review
- detection_tier_target: unit

## D1 — future dates: refused at the handler and capped in the engine

Both fixes the ticket offered, because they cover different writers.

- **Handler.** `POST /v1/controls/{id}/attestations` refuses an
  `observed_at` later than server time + 5 minutes with 422, before the
  control is read. Five minutes is ordinary clock drift; ingest's
  `MaxObservedAtSkew` (±24h) is far looser and stays as it is for other
  kinds. 422 rather than 400: the field parses, the value is refused.
- **Engine.** `attestationAfter` orders by `min(observed_at, ingested_at)`
  first. A pusher sending a `manual.attestation.v1` record straight to
  `/v1/evidence:push` never meets the handler check, and could still date a
  pass up to a day ahead. With the cap it counts from the moment it
  arrived, so an honest fail made later wins. Backdating is unaffected: a
  backdated record's `observed_at` is already the smaller of the two, and
  it only makes the record lose.

The #2111 "skewed clock" unit case (a record ingested at t0 claiming
t0+1h beats one ingested at t0+2h claiming t0) encoded the old rule. It is
replaced by a backdated case and three future-dated cases, including the
ticket's "a future-dated pass loses to a later real fail" in both input
orders.

## D2 — same text: the server key gets a retry window; the web form sends its own key

The default key was `sha256(user, control, body)`. The server stamps
`observed_at` when the body has none, so the same body a second time
always got the same key with a different hash, and ingest answered 409:
neither a retry nor a re-attestation went through.

Now:

1. **Server default key** = `sha256(user, control, body, start of the
5-minute window the request falls in)`. The same body in a later window
   is a new key, so it is recorded.
2. **Retries dedupe.** When the body has no `observed_at`, the handler looks
   up the record already under the key (tenant-GUC read tx, the ingest
   lookup query) and reuses its `observed_at`, so the retry hashes the same
   and ingest returns the first receipt (200, `deduplicated: true`). The
   server stamps `observed_at` at microsecond precision so the stored value
   round-trips. This applies to a client-supplied key too.
3. **The web form sends a fresh `idempotency_key` per submission**
   (`attest-web-` + 16 random bytes). Rejected alternative: relying on the
   window alone for the form. Inside one window, "pass X, fail Y, pass X"
   would dedupe the third to the first record, which is older than Y, and
   the control would stay failed — exactly the flip-back case the ticket is
   about. The form has no automatic retry and disables submit while busy,
   so a per-submission key loses no dedupe it had.

A body straddling a window boundary on retry gets a new key and is written
twice. Both records say the same thing, so the control state is unchanged;
the cost is one extra ledger row. An API caller that needs exact-once
sends its own `idempotency_key`. Rejected: a client-supplied key only,
because existing API callers send none and would keep the 409.

Known limit: the ledger hash covers `source_attribution.session_id`, which
for a JWT caller is the token's `jti`. A retry sent after an access-token
refresh hashes differently and still gets 409, as before this change. Retry
dedupe holds within one token.

## D3 — replays do not evaluate

When the receipt is `Deduplicated`, nothing was written and the first
request already evaluated, so the handler skips `EvaluateControl` and
returns `evaluated: false` with the 200. The web does not read `evaluated`.
The cost: if the first request's evaluation failed or timed out, a retry no
longer re-evaluates, and the control waits for the scheduled run (or
Evaluate now). The ticket asks for the skip, and replays should not be a way
around the evaluation rate limit (D4).

## D4 — rate limit noted on #2111's D4

Added there: attest-triggered evaluations are not rate-limited (#2479's
limiter is in the Evaluate-now handler, `controlstate/evaluate_limit.go`),
and #2497 must cover this call site.

## D5 — fixture

`fixtures/e2e/attest.sql` used `env == "prod"`, which the scope engine
cannot parse. It now uses `{"op":"true"}`, as `attest-result.sql` does.

## D6 — `fail_reason` maxLength left for 1.3.0

Optional in the ticket. A schema version for one constraint would land just
before #1742's period fields, which also need 1.3.0. Left for that version.

## D7 — tests

- Engine (unit): `TestLatestAttestationWins` future-dated and backdated cases.
- Handler (unit): `TestAttestSubmit_RejectsFutureObservedAt`,
  `TestDeriveIdempotencyKey_RetryWindow`.
- API (integration, real Postgres): `TestAttest_FutureObservedAt_Refused`,
  `TestAttest_RetryWithoutKeyOrObservedAt_Dedupes`,
  `TestAttest_SameTextAgain_IsRecorded`.
