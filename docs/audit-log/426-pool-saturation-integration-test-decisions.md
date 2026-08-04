# 426 — Pool-saturation API-boundary integration test: decisions

**Issue:** OPENENGINE-426 (follow-up 2 of slice 354's D9; parent OE-382)
**Date:** 2026-08-04
**Type:** JUDGMENT
**Branch:** `open-engine/OE-426-integration-test-pin-the-api-e`
**Gap being closed:** [`docs/audit-log/354-db-pool-exhaustion-execution-decisions.md`](354-db-pool-exhaustion-execution-decisions.md) D8
**Claim being pinned:** [`docs/audits/335-chaos-experiment-design.md`](../audits/335-chaos-experiment-design.md) §Experiment 1 expected outcome

**Detection-tier classification:** `detection_tier_actual` = `manual_review`
(the behaviour-vs-design divergence recorded in D2 was found by reading the
handler and error-translation code, then confirmed by the new test);
`detection_tier_target` = `integration` (the new suite pins it there — the
tier slice 354 D8 named).

---

## Introduction

Slice 354 executed slice 335's Experiment 1 and found the experiment could
not reach its own antecedent: an external connection storm cannot starve a
warm in-process `pgxpool`. Its D9 filed two halves of the fix — OE-425
redesigns the chaos Variable; this slice (OE-426) is the cheaper, durable
half: an integration test that saturates the platform's pool FROM THE INSIDE
and pins what the API boundary actually does, on every PR.

The new suite is
[`internal/api/evidence/integration_test.go`](../../internal/api/evidence/integration_test.go):
a pool constrained to N=2 connections, every slot held by the test, driving
N+1 concurrent requests through the evidence read handler
(`GET /v1/evidence`) and the push handler (`POST /v1/evidence:push`).

Per the issue boundary, this slice changes NO production error-handling
behaviour. It asserts what the platform does, and records here where that
differs from what slice 335 said it should do.

---

## D1 — The pool-acquisition path and its error translation (Do step 1)

**Read path** (`GET /v1/evidence`): `controldetail.Store.inTx` →
`pgxpool.Pool.Begin(ctx)` per query (`internal/api/controldetail/store.go`).
On acquisition failure the handler routes through `httperr.WriteInternal`
(slice 367's CWE-209 helper): status **500**, body exactly
`{"error":"internal error","request_id":"<uuid>"}`, full error text logged
server-side only.

**Write path** (`POST /v1/evidence:push`, direct dispatch):
`ingest.Service.Process` → `pgx.BeginTxFunc(tenantCtx, s.pool, …)`
(`internal/evidence/ingest/ingest.go`). An acquisition failure is not one of
the seven mapped ingest error categories, so `writeBatchError`
(`internal/api/evidence/http.go`) falls through to status **500** with body
`{"error":"record[0] rejected_internal_error: <err>","code":"internal_error"}`.
Unlike the read path, `err.Error()` IS reflected into the body here; for a
starved acquire that text is the context error ("context deadline
exceeded") — structured and leak-free, which the test pins so a future
refactor that starts reflecting richer driver errors fails CI.

Before the response is written, the write path also burns up to
`auditWriteTimeout` (3 s) on the best-effort reject-audit acquire, which is
equally starved. That serialized wait is why the saturated push response
arrives ~deadline+3 s, and why the test's per-request deadline is short.

---

## D2 — Observed behaviour vs slice 335's expected outcome (Do step 3)

Slice 335 §Experiment 1 expected: "writes fail fast with a structured 4xx
carrying a `retry_after` hint, no stack trace leaks to the client."

Observed, and now asserted by the test:

| Contract dimension           | Slice 335 claim        | Actual (pinned)                                                                 |
| ---------------------------- | ---------------------- | ------------------------------------------------------------------------------- |
| Fail fast                    | yes                    | **No.** Acquisition queues until the request context is done; with a patient client the request waits indefinitely (no server-side handler deadline — `httpserver.go` sets only `ReadHeaderTimeout`). |
| Status code                  | 4xx                    | **500** on both read and write paths once the caller's deadline expires.        |
| Structured body              | yes                    | **Yes.** Read: slice-367 generic envelope with `request_id`. Write: `errorBody` with `code:"internal_error"` and the `rejected_internal_error` decision token. |
| `retry_after` hint           | present                | **Absent.** No `Retry-After` header, no body field, on either path. (The push rate limiter's 429 does send `Retry-After`, but that is a different, unsaturated code path.) |
| No stack/credential/path leak | yes                   | **Holds.** Asserted against stack-trace markers, `file.go:` frames, DSN forms, and the live DSN's user/password/host/database. |
| Ledger integrity             | (implied)              | **Holds.** Saturated pushes land zero rows; the append-only count is unchanged and recovery pushes land normally. |

The two claims that FAIL are fail-fast-4xx and the retry hint. Whether the
platform SHOULD fail fast with a 429/503 + `Retry-After` under pool
starvation is a real design question — but changing it is production
error-handling behaviour, which this slice's boundary forbids. The test
deliberately asserts the 500 (with comments warning future editors), so the
day a slice implements the aspirational contract, this test fails loudly and
gets updated alongside the behaviour change, keeping the pinned contract and
the implementation in lockstep. Filing that behaviour-change slice is left
to the maintainer's prioritisation; slice 335's redesigned Experiment 1
(OE-425) is the other consumer of this question.

---

## D3 — Determinism: saturation is held, not raced (AC "bounded, no flaky sleeps")

The integration tier has no retry (Q-16), so the test avoids every timing
race:

- The test holds ALL N pool slots for the whole assertion window. A
  saturated request can never acquire, regardless of goroutine scheduling,
  so the 500 outcome is guaranteed, not raced.
- The per-request deadline (1 s) is a BOUND on a wait that provably cannot
  succeed — it is the mechanism that terminates the request (modelling a
  timing-out caller), not a sleep that hopes something finishes in time.
- Baseline and recovery requests run against a fully released pool with a
  generous harness deadline (15 s), the same exposure every other
  integration suite has to a slow CI database.
- Held slots release via `t.Cleanup` (idempotent), so even a failing
  assertion cannot leave connections held; the read test additionally
  asserts `pool.Stat().AcquiredConns() == 0` after recovery.

Verified locally: 6 consecutive `-race -count` runs, all green, ~5.5 s per
iteration (the write test's ~4 s is the deterministic
deadline + `auditWriteTimeout` serialization described in D1).

---

## D4 — Scope: the direct dispatch path, not the JetStream path

Production wiring routes pushes through the slice-015 JetStream publisher
when NATS is attached (`internal/api/register_graph.go`), acking at
stream-commit time — under pool saturation that path can 201 before any
ledger write is attempted, and the starvation surfaces later in the
consumer. The test pins the DIRECT `Service.Process` dispatch (publisher
nil): the path where pool acquisition sits on the HTTP request's critical
path, i.e. the only path with an API-boundary error shape to pin. The
consumer-side starvation behaviour of the stream path is chaos-experiment
territory and belongs to OE-425's redesigned Variable, not this test.

Two further scope choices, same reasoning:

- **Stub schema validator.** Validation runs before pool acquisition and is
  irrelevant to the saturation contract; stubbing it keeps the suite off
  the Leg-A `evidence_kind_schemas` catalog seed, letting it ride a Phase B
  shard (D5).
- **No production seam was needed** (the issue's "If blocked" concern):
  `pgxpool.Config.MaxConns` constrains the pool at construction, exactly as
  `pool_max_conns` in `DATABASE_URL_APP` would in deployment.

---

## D5 — Q-7 enrolment (Do step 4)

`internal/api/evidence` imports `internal/db/dbx` (export.go) and shipped no
`integration_test.go` — exactly the Q-7 gap. With this slice the package
gains one and is enrolled in the integration matrix on **Leg B4**
(`scripts/integration-shards.txt`): it is tenant-scoped with no
catalog-seed coupling (stub validator, fresh-UUID tenants, `dbtest.SeedTenant`
cleanup), so it fits B4's cross-cutting-read-handler profile rather than
adding to critical-path Leg A. Both structural guards pass:
`audit-integration-enrolment.sh` (127 tagged / 131 enrolled / 0 waived) and
`check-integration-shard-coverage.sh` (union complete, disjoint, Phase-A pin
holds).

---

## Acceptance-criteria trace

| AC                                                                | Where satisfied                                                                                     |
| ----------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| Integration test exercises pool saturation deterministically       | `internal/api/evidence/integration_test.go` (held-slot saturation, D3); passes `go test -tags=integration -p 1` locally 6/6 |
| Asserts status code, body shape, leak absence                      | Both tests: status pin, JSON-shape pin, `assertNoLeakage` (stack/DSN/credential/path), `assertNoRetryHint` |
| Divergence recorded in a decisions log                             | D2 (fail-fast-4xx and `retry_after` both diverge)                                                    |
| Bounded — no held connections, no flaky wall-clock sleeps          | D3 (cleanup-released slots, `AcquiredConns()==0` assertion, deadlines as bounds not races)           |
| Q-7 enrolment                                                      | D5                                                                                                   |

---

## Cross-references

- Slice **354** — D8 names this gap; D9 row 2 files it as OE-426.
- Slice **335** — owns the expected-outcome claim D2 diverges from, and
  (via OE-425) the chaos-side redesign that will consume the same
  fail-fast design question.
- Slice **367** — the `httperr` generic-5xx envelope the read path's pinned
  body shape comes from.
- Slice **417/747** — the shard manifest the package is enrolled into.
