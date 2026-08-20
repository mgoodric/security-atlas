# Slice 356a — Chaos Experiment 3 execution: Postgres primary unavailable · decisions log

**Type:** JUDGMENT · **Approach:** execute the slice-335 design as written (no redesign) · **Date:** 2026-07-24

- detection_tier_actual: `manual_review` (chaos run)
- detection_tier_target: `integration`

> Three resilience gaps surfaced (G-1, G-2, G-3 below). None is reachable from
> the existing tiers today: no test in any tier exercises the platform with
> Postgres stopped. `integration` is the right target tier — the Go integration
> suite already runs a real Postgres and could stop/start it — which is itself
> the argument for the follow-ups filed at the end.

**Design contract:** [`docs/audits/335-chaos-experiment-design.md`](../audits/335-chaos-experiment-design.md) §Experiment 3.
**Slice narrative:** [`docs/issues/356-data-tier-outage-chaos-round-1.md`](../issues/356-data-tier-outage-chaos-round-1.md).
**Scope of THIS log:** Experiment 3 only. Slice 356 bundles Experiment 3 with
Experiment 5 (atlas restart mid-push); Experiment 5 was NOT executed here and
its AC-6 through AC-9 remain open. This log is deliberately filed at `356a-`
rather than at the bundled `356-data-tier-outage-chaos-round-1-decisions.md`
path named by slice 356 AC-10, so that the bundled path is not created
half-populated and read as though both experiments had run.

---

## Headline

The narrow falsification check the slice defines (AC-5: no hang > 30s, no atlas
crash, no stack traces) **HOLDS on all three counts**.

The design's **hypothesis and expected outcome are FALSIFIED** on the
error-shape and health-signal claims. The platform degrades safely — fast,
bounded, non-leaky, non-corrupting — but it degrades **illegibly**: it reports a
database outage as an authorization failure, logs nothing about it, and keeps
answering its health probe `200 {"status":"ok"}`.

Per the design's own framing, that is a successful experiment. The falsified
claims are recorded plainly below and carried into follow-ups.

---

## Pre-execution checklist — sign-off (AC-1)

The design's §Experiment 3 checklist has two items. Both were executed by
`preflight()` in `scripts/chaos/run-exp3-pg-outage.sh` and signed off against
real output, not asserted. Two operational gates (C-0, C-1b) were added because
the design's two items are not sufficient on their own to make the run
meaningful; the additions are recorded here as part of the sign-off.

| ID      | Item                                                       | Source              | Result on the run of record                                                      |
| ------- | ---------------------------------------------------------- | ------------------- | -------------------------------------------------------------------------------- |
| C-0     | Whole stack healthy before injecting                       | added (operational) | PASS — every long-lived service running and healthy                              |
| C-1a    | No in-flight evidence pushes that would orphan             | design              | PASS — ledger quiesced, 0 rows stable across a 3s settle window                  |
| C-1b(i) | The preflight push actually LANDS in the ledger            | added (see D2)      | PASS — ledger 0 → 1                                                              |
| C-1b    | Idempotency-key registry makes a replayed push safe        | design              | PASS — replay returned an identical `record_id`, ledger steady at 1              |
| C-2     | Snapshot `evidence_records` BEFORE; verify identical AFTER | design              | PASS — captured at 1; see D3 for which pair of snapshots is the load-bearing one |

Scope-discipline gates enforced in the tooling itself, not just by operator
care (slice 335 §Scope discipline / P0-335-2, slice 356 P0-1):

- Both scripts **refuse to run** unless the base-URL host is a loopback literal.
- The postgres container to stop is resolved via `docker compose ps -q postgres`
  against the compose file passed in, so the stop cannot reach a container
  belonging to atlas-edge, a hosted deployment, or any unrelated stack.
- No hosted or edge endpoint appears anywhere in `scripts/chaos/`.

Injection was not started until every row above read PASS.

---

## What was run

- **Stack:** local docker-compose only — `deploy/docker/docker-compose.yml`,
  reset with `down -v && up -d` immediately before the run of record.
- **Base URL:** `http://127.0.0.1:58080` (loopback guard passed).
- **Run of record:** `2026-07-24T22:39:45Z` → `22:41:42Z`.
- **Steady state:** 30s, captured **before** any injection.
- **Injection:** `docker compose stop postgres`, held **60s** — the design's
  specified duration — with metrics captured every second throughout.
- **Recovery:** `docker compose start postgres`, polled to steady state.
- **Probes, 1 round/s, all four fired concurrently:** `GET /v1/anchors` (read),
  `POST /v1/evidence:push` (write), `GET /health`, `GET /healthz`.

Tooling: [`scripts/chaos/run-exp3-pg-outage.sh`](../../scripts/chaos/run-exp3-pg-outage.sh)
(orchestrator), [`scripts/chaos/pg-outage-probe.sh`](../../scripts/chaos/pg-outage-probe.sh)
(synthetic traffic), [`scripts/chaos/ensure-chaos-control.sh`](../../scripts/chaos/ensure-chaos-control.sh)
(fixture; see D2).

---

## Steady state vs injection — the measured comparison (AC-2, AC-3)

Latency in ms, 60 injection rounds and 30 steady rounds per surface.

| Surface                  | Phase     | Code            | n   | p50 | p95 | max | Response body                            |
| ------------------------ | --------- | --------------- | --- | --- | --- | --- | ---------------------------------------- |
| `GET /v1/anchors`        | steady    | 200             | 30  | 8   | 14  | 20  | (payload)                                |
| `GET /v1/anchors`        | injection | **500** (60/60) | 60  | 7   | 10  | 13  | `{"error":"authorization engine error"}` |
| `POST /v1/evidence:push` | steady    | 200             | 30  | 7   | 10  | 12  | `{"receipts":[...]}`                     |
| `POST /v1/evidence:push` | injection | **500** (60/60) | 60  | 6   | 9   | 13  | `{"error":"authorization engine error"}` |
| `GET /health`            | steady    | 200             | 30  | 4   | 6   | 16  | `{"status":"ok","db":"ok"}`              |
| `GET /health`            | injection | **200** (60/60) | 60  | 6   | 9   | 10  | `{"status":"ok","db":"degraded"}`        |
| `GET /healthz`           | steady    | 401             | 30  | 3   | 4   | 13  | `{"error":"authorization must be ..."}`  |
| `GET /healthz`           | injection | 401             | 60  | 3   | 5   | 8   | `{"error":"authorization must be ..."}`  |

Ledger and buffer, across the whole run:

| Reading                 | @checklist | @injection boundary | after recovery |
| ----------------------- | ---------- | ------------------- | -------------- |
| `evidence_records` rows | 1          | 31                  | **31**         |
| `EVIDENCE_INGEST` depth | 1          | 31                  | 31             |

- **Recovery to steady state: 1s** (design budget: 30s).
- **Post-recovery write probe: `POST /v1/evidence:push` → 200** — the write
  path recovered, not just the read path.
- **Aborted: no.** Neither abort criterion tripped.

---

## Falsification verdicts

### The slice-356 AC-5 check — HOLDS on all three

| #   | Check                              | Evidence                                                                                              | Verdict |
| --- | ---------------------------------- | ----------------------------------------------------------------------------------------------------- | ------- |
| F-1 | No request hangs > 30s             | max injection latency **13ms**; 0 of 240 requests over threshold                                      | HOLDS   |
| F-2 | atlas does not crash               | container running throughout; `RestartCount` 0 → 0                                                    | HOLDS   |
| F-3 | No stack traces in response bodies | 0 of 240 injection-phase bodies matched any Go-runtime / driver / SQLSTATE / import-path leak pattern | HOLDS   |

F-3 is the check that overlaps slice **367**'s error-leak work. It came back
clean under this failure mode — a positive datapoint for 367, not a duplicate
finding. The bodies returned under outage are terse structured JSON with no
`pgx`, no SQLSTATE, no file:line frame, no internal import path.

### The design's hypothesis and expected outcome — FALSIFIED in three places

The design states more than AC-5 does. Measured against its own text:

| Design claim (§Experiment 3)                                           | Measured                                                                                                          | Verdict             |
| ---------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------- | ------------------- |
| "structured 5xx … within 5 seconds of request arrival"                 | structured 5xx at p95 **10ms**                                                                                    | HOLDS (comfortably) |
| "not stack traces, not raw pgx errors"                                 | no leakage of any kind                                                                                            | HOLDS               |
| "No request hangs indefinitely"                                        | max 13ms                                                                                                          | HOLDS               |
| "503 with body `{error: "database_unavailable", retry_after: 5}`"      | **500** with `{"error":"authorization engine error"}`; no `retry_after`                                           | **FALSIFIED**       |
| "`/healthz` … flips to a degraded state … returns **503** … within 1s" | `/health` returns **200** `{"status":"ok","db":"degraded"}`; `/healthz` is not a registered route and returns 401 | **FALSIFIED**       |
| "After postgres restarts: full recovery within 30s"                    | **1s**, write path confirmed                                                                                      | HOLDS               |

The two falsifications are stated plainly rather than softened: this
experiment's designed purpose was to test error-shape discipline, and
error-shape discipline is exactly where it failed.

---

## D1 — Why the DB outage presents as an authorization failure (G-1, G-2)

Root cause, confirmed in code rather than inferred from the response body.

`internal/api/authzmw/middleware.go:73-77`:

```go
decision, err := engine.Decide(r.Context(), in)
if err != nil {
    httpresp.WriteError(w, http.StatusInternalServerError, "authorization engine error")
    return
}
```

Every authenticated request passes the authz middleware before reaching a
handler. The OPA decision needs DB-backed inputs, so with Postgres stopped
`engine.Decide` is the **first** thing to fail. The request never reaches the
handler that could have produced a `database_unavailable` 503. Both the read
and the write surface therefore return the identical authz-flavoured 500 — which
is precisely what the measured table shows: 60/60 reads and 60/60 writes with
byte-identical bodies.

Two distinct defects fall out of those four lines:

- **G-1 — wrong and misleading error shape.** HTTP 500 where the design
  specifies 503; no `retry_after` hint, so a well-behaved client has nothing to
  back off against; and the error string points the operator at the
  authorization subsystem during what is actually a database outage. An
  operator paged at 3am reads "authorization engine error" and starts
  debugging OPA and JWTs. Severity: the platform is _safe_ but _illegible_
  under this failure.

- **G-2 — the underlying error is discarded, unlogged.** `err` is dropped on
  the floor. `internal/api/authzmw/middleware.go` imports no logger at all.
  This was confirmed empirically as well as by reading: across the 60-second
  outage the platform returned **120 HTTP 500s and wrote zero corresponding log
  lines**. The only server-side signal that anything was wrong came from the
  streambuf consumer, on an unrelated code path. The one place holding the real
  cause throws it away.

G-1 and G-2 are one cohesive fix in one file and are filed as a single
follow-up.

## D2 — A harness defect that made the first attempt's checklist pass vacuously

Recorded because it very nearly produced a clean-looking but worthless result,
and because the guard against it is now part of the tooling.

The write probe carries a `control_id`. `evidence_records` has a composite FK
on `(tenant_id, control_id)`. The first execution attempt sourced that id with
`select id from controls limit 1`, which returned a row belonging to the
**catalog** tenant (`00000000-0000-0000-0000-000000000000`), not the bearer's
tenant (`...4000-8000-000000000001`).

The failure mode is quiet, and instructive in its own right. Because the
NATS-backed ingest path acks onto the buffer **before** the ledger write
(constitutional invariant #2's ingest/eval separation, `streambuf.go`), every
push still returned **200 with a receipt**. Meanwhile every insert failed its FK
and redelivered forever:

```
streambuf: process error; will redeliver  decision=rejected_internal_error
err="insert evidence: ERROR: insert or update on table \"evidence_records\"
violates foreign key constraint \"evidence_records_tenant_id_control_id_fkey\""
```

The ledger therefore read **0 rows for the entire run** — and the design's
checklist item C-2, "snapshot the row count BEFORE, verify identical AFTER",
**passed**: `0 == 0`. A completely non-functional write path signed off the
checklist that exists to prove the write path is intact.

Two changes close it:

1. `scripts/chaos/ensure-chaos-control.sh` returns a control_id guaranteed to
   belong to the bearer's tenant, synthesizing a documented fixture control if
   the tenant owns none. The run of record used it.
2. `preflight()` gained **C-1b(i)**: the preflight push must be observed to
   _land_ (ledger strictly increases) before the replay assertion runs. A
   ledger that does not move now fails the checklist loudly, with the FK
   hypothesis named in the failure message.

The observation underneath this — that a permanently unpersistable record still
receives a 200 receipt and redelivers indefinitely — is a real property of the
ack-before-persist design. It is a consequence of invariant #2 rather than a
violation of it, it is out of scope for Experiment 3, and it is **not** filed as
a resilience gap here. Noting it for whoever executes Experiment 2 (slice 355),
where consumer behaviour is the subject.

## D3 — Which `evidence_records` snapshot pair is load-bearing

The design says "snapshot BEFORE; verify identical AFTER". Taken literally at
checklist time, that comparison is confounded: the steady-state window
deliberately writes evidence between the checklist and the injection, so a
perfectly healthy run reports BEFORE=1, AFTER=31 and looks like a divergence.

The question the item is actually asking is "did the outage leave partial-write
state?" The snapshot that answers it is taken at the **injection boundary** —
after steady-state traffic, immediately before the stop. Both pairs are
reported; the injection-boundary pair is the verdict:

**ledger 31 → 31 across the outage and recovery: IDENTICAL. No partial-write
state, no lost records, no orphaned transactions.** This is the experiment's
strongest positive result.

## D4 — `/health` returning 200 under DB outage is a deliberate prior decision, not a bug (G-3)

The design expected 503. The shipped behaviour is 200, and
`internal/api/httpserver.go:527-536` documents why on purpose:

> It always returns 200 when the process is serving HTTP. … a failed ping
> reports `{"db":"degraded"}` but still 200 — `/health` is liveness ("is the
> process up?"), not readiness. Returning 503 on a transient DB blip would
> cause compose to mark atlas unhealthy and restart-loop it during Postgres
> warm-up.

That reasoning is sound and this slice does not overturn it — slice 335 owns the
design, and reversing a slice-037 decision is outside an execution slice's
remit. The experiment nonetheless surfaces a genuine gap, and it is sharper than
"the status code is wrong":

**G-3 — the platform has a liveness probe and no readiness probe.** There is no
endpoint an operator can point a load balancer or a k8s readiness gate at to
learn "stop sending me traffic". `grep` over `internal/api` finds `/health` and
nothing else — no `/ready`, no `/readyz`, no `/healthz`. During the 60s outage
every authenticated request failed while the only available probe answered
`200 {"status":"ok"}`. In a multi-replica deployment that is a black hole: the
LB keeps routing to a replica that cannot serve a single authenticated request.

The right fix is additive (a readiness endpoint alongside the liveness one), not
a change to `/health`'s semantics.

**G-4 — path divergence, folded into G-3.** The design names `/healthz`
throughout; the shipped router registers `/health`. `/healthz` is not a route,
so it falls through to auth middleware and answers 401 — in steady state as well
as under outage. The probe fires both paths deliberately so this divergence is
recorded with evidence instead of silently reconciled.

## D5 — On the slice-332 baseline

Slice 335 §Cross-references ties the 10 req/s synthetic baseline to Experiments
**1 and 2**. Experiment 3's steady state is defined qualitatively — "all API
endpoints return 2xx for valid requests; `/healthz` returns `{status: "ok"}`;
pool reports healthy connections" — with no rate parameter, so no rate had to be
re-derived from slice 332 and no baseline-staleness block applies.

Steady state was nonetheless measured rather than assumed, and the numbers are
in the comparison table above (read p95 14ms, write p95 10ms, health p95 6ms at
4 req/s). Those are this experiment's own baseline. They are not comparable to
slice 332's published 5.89ms/6.91ms figure, which is the slice-008 UCF
graph-traversal benchmark on a different surface — noted so a later reader does
not mistake one for a regression against the other.

## D6 — Abort handling is detect-and-record, not kill-mid-window

The design says an abort criterion "triggers immediate rollback". The
implementation runs a watcher that trips a sentinel the instant either criterion
fires, but lets the fixed 60-second injection window finish before rolling back.

Deliberate. The primary abort criterion _is_ a >30s hang, and killing the probe
to roll back faster would truncate the very measurement the criterion exists to
capture. The window is bounded at 60s against a local container, so the cost of
finishing it is at most ~29 additional seconds of a failure that is already
understood. Rollback is additionally guaranteed by an `EXIT` trap that restarts
postgres on any exit path, expected or not. Neither criterion tripped on the run
of record.

---

## Resilience gaps and follow-ups filed

| ID  | Gap                                                                                                                 | Severity | Follow-up               |
| --- | ------------------------------------------------------------------------------------------------------------------- | -------- | ----------------------- |
| G-1 | DB outage returns 500 `{"error":"authorization engine error"}` — wrong code, misleading subsystem, no `retry_after` | high     | OPENENGINE-432          |
| G-2 | The underlying authz-engine error is discarded unlogged; 120 5xx produced 0 log lines                               | high     | OPENENGINE-432          |
| G-3 | No readiness probe exists; liveness answers 200 while the platform cannot serve authenticated traffic               | medium   | OPENENGINE-433          |
| G-4 | Design says `/healthz`, router registers `/health`; `/healthz` answers 401                                          | low      | OPENENGINE-433 (folded) |

Both are filed as children of OPENENGINE-384. G-1 and G-2 are bundled into
OPENENGINE-432 because they are one cohesive fix in one file
(`internal/api/authzmw/middleware.go`). G-4 is folded into OPENENGINE-433
because both concern the health-probe surface.

---

## Acceptance criteria — status

| AC          | Status       | Note                                                                                                          |
| ----------- | ------------ | ------------------------------------------------------------------------------------------------------------- |
| AC-1        | MET          | Checklist executed and signed off against real output; see table above                                        |
| AC-2        | MET          | Steady state captured 30s BEFORE injection; gate required 2xx on read + health                                |
| AC-3        | MET          | 60s injection via `docker compose stop postgres`; codes, latencies, body shapes captured every second         |
| AC-4        | MET          | Recovery performed; **1s** to steady state; post-recovery write probe 200                                     |
| AC-5        | MET          | All three checks evaluated explicitly; all three HOLD; design hypothesis falsified separately and recorded    |
| AC-6 – AC-9 | NOT IN SCOPE | Experiment 5; not executed by this slice                                                                      |
| AC-10       | PARTIAL      | This log covers Experiment 3; the bundled 356 log completes when Exp 5 runs                                   |
| AC-11       | MET          | Cross-references slice 335 (design), 356 (narrative), 367 (error-leak, F-3 clean), 355 (streambuf note in D2) |

## Cross-references

- Slice **335** — [`docs/audits/335-chaos-experiment-design.md`](../audits/335-chaos-experiment-design.md) §Experiment 3, the design contract. Not modified by this slice.
- Slice **356** — [`docs/issues/356-data-tier-outage-chaos-round-1.md`](../issues/356-data-tier-outage-chaos-round-1.md), the bundled execution narrative.
- Slice **367** — error-leak work. F-3 clean under this failure mode; cross-referenced, not duplicated.
- Slice **355** — Experiment 2 (NATS consumer lag). D2's ack-before-persist observation belongs to that experiment's subject matter.
- Slice **332** — [`docs/audits/332-performance-audit-report.md`](../audits/332-performance-audit-report.md); see D5 for why its baseline is not load-bearing here.
- Slice **037** — the liveness-not-readiness decision behind `/health`; see D4.
