# Slice 358 — Chaos Experiment 7 execution: schema-registry unavailable · decisions log

**Type:** JUDGMENT · **Approach:** execute the slice-335 design as written (no redesign) · **Date:** 2026-07-25

- detection_tier_actual: `manual_review` (chaos run)
- detection_tier_target: `integration`

> The headline gap (G-1) is reachable from the integration tier and is not
> reached today. `internal/api/schemaregistry` has an integration suite that
> covers registration, semver enforcement, tenant-private resolution and
> `ValidatePayload` against a live Postgres — but every case runs with the
> table readable. Nothing asserts what the registry returns when its own
> backing store refuses the read, and `Service.lookupCompiled` collapses that
> case into `ErrUnknownKind` on the way out (`service.go:605`). One
> integration case that revokes SELECT inside the test transaction and asserts
> a non-`ErrUnknownKind` error would have caught this without a chaos run.

**Design contract:** [`docs/audits/335-chaos-experiment-design.md`](../audits/335-chaos-experiment-design.md) §Experiment 7.
**Slice narrative:** [`docs/issues/358-schema-registry-chaos-execution.md`](../issues/358-schema-registry-chaos-execution.md).
**Registry shape reference:** [`docs/audit-log/068-schema-registry-evidence-kind-fix-decisions.md`](068-schema-registry-evidence-kind-fix-decisions.md) (slice 358 AC-9).
**Tooling:** [`scripts/chaos/run-exp7-schema-registry.sh`](../../scripts/chaos/run-exp7-schema-registry.sh),
[`scripts/chaos/schema-registry-probe.sh`](../../scripts/chaos/schema-registry-probe.sh).
**Run of record:** tag `oe389`, 2026-07-25T07:36:08Z → 07:39:20Z UTC, compose
project `security-atlas-chaos358` (local docker-compose only).

---

## Headline

**The hypothesis splits cleanly in two, and the two halves give opposite
answers.**

The hot-cache half **HOLDS**, and holds without a scratch: 180 known-kind
pushes across three phases, 180 accepted, 180 landed in the ledger, p95 flat at
11ms in steady state and 11ms under injection. A cached kind does not notice
that the registry's store is gone.

The fail-fast half is **FALSIFIED**, and it is falsified in a way the design did
not anticipate. The design expects an uncached kind to be refused during the
outage with `503 schema_registry_unavailable`, distinguishable from the
kind-not-found 400. What the platform actually does is accept the push with
**201 and a receipt**, then drop the record in the consumer and classify the
drop as `rejected_unknown_kind` — the same verdict it gives a kind that never
existed. Nothing distinguishes "your evidence is invalid" from "our registry
was down when your evidence arrived", and the pusher is told 201 either way.

The consequence is worse than a bad status code. `rejected_unknown_kind` is on
the **poison** list (`streambuf.go:569-581`), so the consumer `Term()`s the
message. Term means no redelivery, ever. A registry outage therefore converts
valid, accepted-and-acked evidence into **permanent silent loss**. This run
measured exactly that: one valid registered kind, pushed once per phase,
landed in steady state, landed after recovery, and **did not land during the
outage** — with a 201 receipt in the pusher's hand for the push that vanished.

The ledger-quality invariant the experiment was built to protect **survived**:
nothing unvalidated reached `evidence_records` in any phase. The failure is not
that bad data got in. It is that good data got dropped, and the drop is
mislabelled as bad data.

---

## Pre-execution checklist — sign-off (slice 358 AC-2, design §Experiment 7)

The design carries two checklist items. Both are executed by `preflight()`
against the running stack rather than asserted, and three further checks this
registry shape requires are joined to them. **Injection does not start unless
every check passes** — `run-exp7-schema-registry.sh` exits 1 on any FAIL,
before the first `REVOKE`. Verbatim run output:

| ID  | Design item                                                    | Verdict | Evidence from the run                                                                                                                                              |
| --- | -------------------------------------------------------------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| C-1 | "Identify the schema-registry's current deployment shape"      | PASS    | IN-PROCESS in atlas. No registry service in the project (`atlas atlas-bootstrap atlas-migrate minio minio-mc nats postgres`); Postgres-backed with 48 global rows. |
| C-2 | "Choose a known-kind pushed at least once in the process life" | PASS    | `access_review.completion.v1/1.0.0` pushed (HTTP 201) **and** confirmed landed in the ledger inside the current atlas process (started 07:12:04Z).                 |
| C-3 | executor-added: injection mechanism reversible + confined      | PASS    | `atlas_app` held SELECT pre-injection; no reader of `evidence_kind_schemas` outside `internal/api/schemaregistry` (checked mechanically, not asserted).            |
| C-4 | executor-added: fresh tenant scope + tenant-OWNED control      | PASS    | Fresh tenant `…001784964965`, tenant-owned control `…c01784964965`, 0 pre-existing ledger rows.                                                                    |
| C-5 | executor-added: scope boundary                                 | PASS    | Loopback base `http://127.0.0.1:38080`; project carries no edge/hosted-looking service.                                                                            |

C-2 deliberately requires a ledger landing and not just a 2xx. On this
architecture a 201 is returned at stream-commit, before validation — so a
receipt alone would not have proven the seed record traversed the registry's
validate path at all. That distinction turned out to be the whole experiment
(D1).

C-4 exists because `evidence_records` carries a composite FK on
`(tenant_id, control_id)`: a control owned by another tenant still yields a
receipt while every insert fails its FK, so the ledger reads zero rows and a
count-based check passes vacuously. Slice 356a walked into that trap (its D2).

---

## What was run

**Injection mechanism.** The design's Variable field offers "container stop (if
schema registry is a separate process per slice 068) OR in-process flag (if
embedded)" and instructs the executor to confirm the shape first. The shape is
neither: `internal/api/schemaregistry.Service` is an in-process Go service with
no availability flag, backed by the `evidence_kind_schemas` table and fronted
by a process-local hot cache. There is no container to stop and no flag to set.

What "unavailable" means for that shape is that its backing store stops
answering while the rest of the platform keeps running:

```
REVOKE SELECT ON evidence_kind_schemas FROM atlas_app;
```

applied inside the compose project's own postgres container. The registry runs
on the `atlas_app` pool (`cmd/atlas/main.go:98` → `main.go:164`), so this
disables exactly the registry's read path; `evidence_records` INSERT, NATS,
MinIO and auth are untouched. Reversible by the matching GRANT, which the
script's EXIT trap issues on every exit path. Stopping Postgres outright would
have been Experiment 3, and would confound the registry surface with the
ledger surface.

**Probe classes.** Four per tick, one tick per second, so all four observe the
same instant of the window:

| Class                    | What it is                                                           | Which design claim it tests                                |
| ------------------------ | -------------------------------------------------------------------- | ---------------------------------------------------------- |
| `known`                  | global kind, pushed once in-process, so the hot cache holds it       | "cached schemas continue to work"                          |
| `unknown`                | kind registered nowhere                                              | the fail-fast conjunct                                     |
| `cold`                   | **tenant-private kind that IS registered but has never been pushed** | hot-cache vs cold-miss (slice 358's reason to stand alone) |
| `registry_list` / `_get` | `GET /v1/schemas`, `GET /v1/schemas/K/V`                             | the error-shape claim                                      |

The `cold` class is the one that matters, and it is the class the design's own
step list does not contain. A known kind never touches the registry's store and
an unknown kind is invalid whether the registry is up or down — neither can
separate "the registry is down" from "the kind does not exist". A registered
kind that has never been pushed is the only probe that is both **valid** and
forced through `Service.lookupCompiled`'s DB path. It is pushed exactly once
per phase, with a dedicated kind per phase, because the first push hydrates the
tenant cache and the kind stops being cold (D3).

**Timeline.** 60s steady state captured **before any injection**, then inject,
then 60s injected, then restore, then 60s recovered, then drain to a stable
ledger count.

| Marker           | UTC                     |
| ---------------- | ----------------------- |
| steady state     | 07:36:08 → 07:37:08     |
| **INJECT**       | 07:37:08                |
| injected window  | 07:37:09 → 07:38:09     |
| **RESTORE**      | 07:38:09                |
| recovered window | 07:38:10 → 07:39:10     |
| ledger settled   | 07:39:20 (183 rows, 8s) |

**On the window length.** Experiment 7's Method is a step sequence and names no
duration, unlike Experiments 1, 2 and 5 which specify 60-second windows. This
run adopts that same 60s per phase rather than inventing a number or firing a
one-shot probe per step. The choice is the executor's, is recorded here, and is
overridable via `--window-seconds`; the run of record used 60s.

**Injection reached the variable.** Not assumed — checked twice. The script
refuses to continue unless `has_table_privilege('atlas_app', …, 'select')`
flips to `f`, and the registry's own read surface moved 200 → 500 on all 60
injected ticks and back to 200 on all 60 recovered ticks. A no-op revoke could
not have been mistaken for a resilient platform.

---

## Steady state vs injection — the measured comparison (slice 358 AC-3, AC-5, AC-6)

| phase        | class           | reqs | 2xx   | **landed in ledger** | status codes | p50 ms | p95 ms |
| ------------ | --------------- | ---- | ----- | -------------------- | ------------ | ------ | ------ |
| steady       | `known`         | 60   | 60    | **60**               | 201×60       | 6      | 11     |
| **injected** | `known`         | 60   | 60    | **60**               | 201×60       | 7      | 11     |
| recovered    | `known`         | 60   | 60    | **60**               | 201×60       | 5      | 11     |
| steady       | `unknown`       | 60   | 60    | **0**                | 201×60       | 6      | 11     |
| **injected** | `unknown`       | 60   | 60    | **0**                | 201×60       | 7      | 10     |
| recovered    | `unknown`       | 60   | 60    | **0**                | 201×60       | 4      | 13     |
| steady       | `cold`          | 1    | 1     | **1**                | 201×1        | 9      | 9      |
| **injected** | `cold`          | 1    | 1     | **0** ← the finding  | 201×1        | 6      | 6      |
| recovered    | `cold`          | 1    | 1     | **1**                | 201×1        | 3      | 3      |
| steady       | `registry_list` | 60   | 60    | n/a                  | 200×60       | 6      | 10     |
| **injected** | `registry_list` | 60   | **0** | n/a                  | **500×60**   | 8      | 12     |
| recovered    | `registry_list` | 60   | 60    | n/a                  | 200×60       | 7      | 12     |
| steady       | `registry_get`  | 60   | 60    | n/a                  | 200×60       | 5      | 10     |
| **injected** | `registry_get`  | 60   | **0** | n/a                  | **500×60**   | 7      | 12     |
| recovered    | `registry_get`  | 60   | 60    | n/a                  | 200×60       | 5      | 11     |

`landed in ledger` is a join against `evidence_records` for the fixture tenant,
not an inference from the receipt.

**Ledger accounting closes exactly.** 183 rows after drain:

| Source                     | Rows    |
| -------------------------- | ------- |
| `known` × 3 phases         | 180     |
| `cold`, steady + recovered | 2       |
| C-2 preflight seed         | 1       |
| **`cold`, injected**       | **0**   |
| **Total**                  | **183** |

Every row is accounted for and there is exactly one hole, in exactly the place
the variable predicts it. No known-kind push was lost, no unvalidated record
was gained, and the one record that disappeared is the one valid record whose
resolution needed the registry mid-outage.

**The three numbers that carry the result.**

1. The `known` row does not move — not in status, not in landing, not in
   latency (11 / 11 / 11ms p95). The hot cache genuinely decouples.
2. The `unknown` row does not move **either**, and that is the surprise: 201
   with a receipt in all three phases, including steady state with the registry
   fully up. The design's steady state expects 400 here.
3. The `cold` row moves, and only in the ledger column. Same kind class, same
   201, same receipt — 1 landed, **0 landed**, 1 landed. The registry outage is
   invisible at the wire and total at the ledger.

**Recovery: 1 second, no restart** (slice 358 AC-7). `GRANT` issued at
07:38:09; the registry read surface answered 200 at 07:38:10, measured by
polling at 200ms. The design's Rollback field claims a restart-free recovery
and the measurement confirms it: no atlas restart, no cache rebuild, no
lingering degradation — the recovered phase is byte-for-byte a steady-state
phase, including the cold-miss kind landing again.

---

## Falsification verdicts

### The design's hypothesis, clause by clause

> "When the schema registry is unavailable, evidence push for an
> `evidence_kind` whose JSON Schema is not in the local hot-cache returns 503
> with a structured error."

**FALSIFIED.** Measured 201 with a receipt, on both the unknown kind and the
valid cold-miss kind. No 503 is returned by any surface, and no
`schema_registry_unavailable` error code exists anywhere in the codebase.

> "The push is NOT silently accepted with an unvalidated payload."

**HOLDS at the ledger, FAILS at the wire.** No unvalidated payload reached
`evidence_records` in any phase — the `unknown` class landed 0 of 180 pushes.
But the push IS accepted at the wire: 201 plus a receipt carrying a
deterministic `record_id`, with the rejection happening later in the consumer
and never reported back. "Silently accepted" is the wrong description of what
happens; "silently discarded after acceptance" is the right one, and for the
cold-miss case that is worse, because what gets discarded is valid.

> "Cached schemas continue to work (hot-cache decouples from registry
> availability for known kinds)."

**HOLDS, strongly.** 60/60 accepted and 60/60 landed during the injection, with
no latency shift. This is the clause most worth having verified and it is
verified.

### The design's steady state

> "Push for a known `evidence_kind` (cached) succeeds; push for an unknown
> `evidence_kind` returns 400 with `{error: "evidence_kind_not_found"}`."

First half **HOLDS**. Second half **FALSIFIED — with the registry up**, before
any injection. The unknown kind returns 201×60 in steady state. Two
divergences compound here, and they are worth separating:

1. **Timing.** `POST /v1/evidence:push` acks at NATS stream-commit, not after
   validation (`http.go:dispatch` → `streambuf.JetStreamPublisher.Publish`,
   the slice-015 AC-2 contract). No validation verdict is available at response
   time, so no validation status code can be returned. The design's expectation
   was written against a synchronous push model the platform does not have.
2. **Code and label.** The synchronous path that still exists
   (`DirectPublisher`, unit servers, `POST /v1/controls/:id/attest`) maps
   unknown kind to **412 `unknown_evidence_kind`** (`http.go:243-245`), not
   400 `evidence_kind_not_found`. So even the fallback path does not match the
   design's literal codes.

The design is not wrong about what _should_ happen — a pusher should learn its
record was refused. It is wrong about _where_ the platform could tell it.

### The design's expected outcomes

| Design expectation                                                              | Measured                                                                                         | Verdict       |
| ------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ | ------------- |
| Known-kind push: 200/202 (cache hit serves it)                                  | 201, 60/60, all landed                                                                           | **HOLDS**     |
| Unknown-kind during outage: 503 `{schema_registry_unavailable, correlation_id}` | 201 + receipt; no code, no 503 anywhere                                                          | **FALSIFIED** |
| …distinguishable from 400 `evidence_kind_not_found`                             | Indistinguishable: identical 201 at the wire, identical `rejected_unknown_kind` in the audit log | **FALSIFIED** |
| After restore: unknown-kind push transitions to 400                             | Still 201; nothing to transition                                                                 | **FALSIFIED** |
| Rollback restores steady state                                                  | 1s, no restart; recovered phase == steady phase                                                  | **HOLDS**     |

The indistinguishability verdict is the one with teeth, and it is not a
status-code quibble. `evidence_audit_log` for this run:

| idempotency_key                | decision                | kind                                   |
| ------------------------------ | ----------------------- | -------------------------------------- |
| `exp7oe389-steady-cold-005`    | `accepted`              | `chaos358.cold_exp7oe389_steady.v1`    |
| `exp7oe389-injected-cold-005`  | `rejected_unknown_kind` | `chaos358.cold_exp7oe389_injected.v1`  |
| `exp7oe389-recovered-cold-005` | `accepted`              | `chaos358.cold_exp7oe389_recovered.v1` |

The middle row is a registered kind. The audit trail says it was not. An
operator reading the ledger's own record of what happened is told the pusher
sent an unknown evidence kind, when what actually happened is that the
platform could not read its own registry. The loss is recorded, and recorded
wrong.

### The design's abort criteria

> "Unknown-kind push returns 2xx (falsifies the fail-fast claim). OR
> known-kind push fails (falsifies the hot-cache decoupling)."

The first criterion **fires — in steady state, before injection.** It fires on
all 180 unknown-kind pushes across all three phases. The second never fires.

Neither was treated as a reason to halt mid-run, and that call is recorded
rather than buried. An abort criterion exists to stop an escalating hazard;
this one describes the already-shipped, unchanging behavior of the push surface
in its normal state, so halting on it would have aborted the experiment before
it observed anything, and would have left the injection question unanswered.
The rollback runs on every exit path regardless (EXIT trap), so nothing was
risked by continuing. That the criterion fires pre-injection is itself
evidence for the same conclusion as V-1: it was written against a synchronous
push model.

### Slice 358's acceptance criteria

| AC    | Requirement                                                        | Verdict                                                                                           |
| ----- | ------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------- |
| AC-1  | Registry shape confirmed against slice 068                         | MET — in-process, Postgres-backed, `atlas_app` pool (C-1)                                         |
| AC-2  | Slice 335 pre-execution checklist satisfied                        | MET — 5/5 PASS before injection, blocking                                                         |
| AC-3  | Steady state captured BEFORE: known 2xx, unknown 400               | PARTIAL — captured before injection; known 2xx confirmed; **unknown is 201, not 400** (falsified) |
| AC-4  | Registry process/container stopped                                 | MET by equivalent — no container exists; backing store made unreadable, injection verified twice  |
| AC-5  | Known-kind push still succeeds                                     | MET — 60/60, all landed                                                                           |
| AC-6  | Unknown-kind returns 503, distinguishable from 400                 | **NOT MET — falsified.** 201 at the wire, `rejected_unknown_kind` in the audit log, both phases   |
| AC-7  | Registry restored; unknown-kind transitions back to 400            | Registry restored and verified (1s); the transition is vacuous — it was never 400                 |
| AC-8  | Post-experiment report at this path                                | MET — this file                                                                                   |
| AC-9  | Cross-references slices 335 and 068                                | MET — header links                                                                                |
| AC-10 | If unknown-kind returns 2xx during outage, file a critical finding | Trigger fired **literally**; see below                                                            |

**On AC-10, precisely.** The trigger fired: unknown-kind pushes returned 2xx
during the outage. But AC-10 names the hazard it guards — "this is a
ledger-quality breach" — and that breach did **not** occur. Zero unvalidated
records reached `evidence_records`, in any phase. Reporting a ledger-quality
breach here would be false.

What the run found instead is a different critical-class finding in the same
neighbourhood, pointing the other way: not bad evidence admitted, but good
evidence permanently dropped and mislabelled. That is filed as **G-1** at high
severity. AC-10's instinct — "a 2xx here means something is badly wrong" — was
correct; the thing that is wrong is not the thing it predicted.

---

## D1 — Landing in the ledger, not the receipt, is the measurement

The probe records the HTTP status of every push, and if the experiment had
stopped there it would have concluded that the registry outage changes nothing:
201 before, 201 during, 201 after, on every class. That reading would have been
completely wrong.

Because the push acks at stream-commit, the status code carries no information
about validation at all. So every push row is joined against
`evidence_records` for the fixture tenant, and the verdicts are stated on the
landed column. That is what turns a flat, uninformative status table into the
1 / **0** / 1 cold-miss result — the only place in the entire dataset where the
injection is visible on the push path.

The corollary is a warning for Experiments 1, 2 and 5's executors, and for
anyone who reads this log looking for a template: **on this platform a 2xx from
`/v1/evidence:push` is not evidence of ingestion.** Any chaos experiment that
grades the push API by status code is measuring the NATS publish, not the
platform.

**Confidence: high.** The mechanism is read directly out of `dispatch` and
`JetStreamPublisher.Publish`, the consumer's `Term()` is in the atlas logs with
the exact idempotency key, and the ledger accounting closes to the row.

## D2 — Revoking a table grant, and why that is the honest injection

An in-process service with no failure flag has no clean off switch, and the
three candidates are not equivalent:

| Candidate                                 | Rejected because                                                                                                                                                                                                             |
| ----------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `docker compose stop postgres`            | Breaks the ledger, NATS-consumer writes, auth and audit at the same time. That is Experiment 3, and it confounds the registry surface with the ledger surface — the run could not attribute any observation to the registry. |
| Patch atlas with a `--fail-registry` flag | Requires building a test image; changes the binary under test. The design forbids introducing a chaos framework, and a modified binary is a weaker claim than the shipped one.                                               |
| **`REVOKE SELECT` on the one table**      | **Chosen.** Single variable, shipped binary, reversible in one statement.                                                                                                                                                    |

The blast-radius claim is **checked rather than asserted**: preflight C-3 greps
for any reader of `EvidenceKindSchema*` outside `internal/db/dbx` and
`internal/api/schemaregistry` and FAILs the run if one exists. Today there is
none, so revoking that grant cannot break a surface other than the registry.
If a future slice adds such a reader, the check fails and the next executor is
told why rather than silently measuring a wider outage.

One property of this mechanism deserves naming because it strengthens the
result: it is a **read-path** failure, the mildest possible registry outage. The
table exists, the rows exist, the connection pool is healthy, the query is
well-formed, and one SQL privilege is missing. If the platform mishandles
_this_, it will mishandle a dead registry too.

**Confidence: high** for the mechanism and its confinement. **Medium** on
whether every registry-outage mode produces the identical verdict — a pool
exhaustion or a query timeout arrives as a different pgx error. It reaches the
same `lookupCompiled` slow path and the same discarded-error line
(`service.go:575-576`, `605`), so the same collapse to `ErrUnknownKind` is
expected, but this run measured the permission-denied mode only.

## D3 — The cold-miss probe, which the design's step list omits

Slice 358's stated reason for standing alone rather than being bundled is that
it "distinguishes hot-cache vs cold-miss behavior". The design's Method does
not contain a probe that can draw that distinction: its steps push a known kind
and an unknown kind, and neither one can.

- The known kind resolves from the process-local cache. It never reads the
  registry's store, so it cannot observe the store being gone. (Verified in
  code: for a global kind, `IsRegisteredForTenant` hits the cache and
  `lookupCompiled` hits `s.compiled` — no DB round trip on the push path.)
- The unknown kind is invalid whether the registry is up or down, so its
  rejection is uninformative about availability.

The gap between them is where every real cold miss lives: a kind that IS
registered but is not in this process's cache — a tenant-private kind
registered by an operator after boot, or any kind after a restart. So each
phase pushes exactly one such kind, registered while the registry was up and
never pushed until its own phase. One dedicated kind per phase, because the
first push hydrates the tenant cache and the kind stops being cold; reusing one
kind across phases would have measured the steady-state hydration three times.

This is an addition to the design's Method, not a reinterpretation of its
hypothesis — the hypothesis is explicitly about "an `evidence_kind` whose JSON
Schema is not in the local hot-cache", and this is the only probe class that
actually is one. Without it, this experiment's honest verdict on its central
claim would have been "not exercised".

**Confidence: high.** It is the probe that produced the only differential
signal in the run.

## D4 — Term versus Nak is where the bug actually lives

The status-code divergence is the visible symptom; the severity comes from one
line of classification. `streambuf.go:569-581`:

```go
func isPoison(d ingest.Decision) bool {
	switch d {
	case ingest.DecisionRejectedValidation,
		ingest.DecisionRejectedUnknownKind,   // <-- here
		...
```

Poison decisions get `msg.Term()` — no redelivery, ever. Non-poison errors get
`msg.NakWithDelay(2 * time.Second)` and are retried. That split is exactly
right, and `DecisionRejectedUnknownKind` is on the correct side of it _for a
genuinely unknown kind_: retrying a kind that does not exist would redeliver
forever.

The defect is upstream, in what can produce that decision.
`Service.lookupCompiled` ends its slow path with a bare
`return nil, ErrUnknownKind` (`service.go:605`) and discards the error from its
DB read entirely (`service.go:575`: `if err == nil && …`). A permission denial,
a timeout, a dead pool and an absent row all leave that function as the same
`ErrUnknownKind`. `IsRegisteredForTenant` then maps it to `false`
(`service.go:220-223`), ingest maps that to `DecisionRejectedUnknownKind`, and
the consumer Terms the message.

So a **transient infrastructure failure is laundered into a permanent semantic
verdict**, and the retry machinery that exists and would have handled it
correctly — `DecisionRejectedInternalError` is already non-poison and already
Naks — is never reached. With a 2-second Nak delay and a 1-second recovery,
this run's lost record would have been redelivered and landed. The fix is not
new machinery; it is not throwing away an error.

That is why G-1 is filed at high severity rather than as an error-message
nit, and why the fix is specified as "distinguish the error" rather than "add a
503": the 503 is the wire-level courtesy, the Nak is the data-loss fix.

**Confidence: high.** Every step is in the source, and the run's audit-log rows
show the misclassification landing on a kind that was registered.

## D5 — What the registry's own read surface does, and what it gets right

The push path is not the only surface, so `GET /v1/schemas` and
`GET /v1/schemas/{kind}/{semver}` were probed every tick. Both returned
**500 on 60/60 injected ticks** and 200 on every steady and recovered tick.

Against the design, this is another falsification: 500, not the specified
structured 503, and the body carries no `code` field and no `correlation_id`.
Both paths collapse to the same generic internal-error shape, so the
error-shape claim in the design's cross-experiment observations ("the error
returned under failure is _structured_") is not met in the sense the design
means — a client cannot branch on it.

It does get the security half right, and that is worth recording because it is
the half a GRC platform is judged on. The response body is
`{"error": <generic message>, "request_id": <uuid>}` via
`internal/api/httperr`; the SQLSTATE 42501 text
(`permission denied for table evidence_kind_schemas`) appears **only** in the
server-side log, keyed by the same request_id. No table name, no SQLSTATE, no
role name, no file path reaches the client. Slice 367's error-detail-leakage
discipline holds under dependency failure, which is exactly the condition
where leakage usually appears.

So the read surface's verdict is: **wrong status, wrong structure, correct
discretion.** G-3 files the status and structure at medium. The leakage check
needs no follow-up.

**Confidence: high** on the measurement (120 probes, unanimous) and on the body
shape (read from `httperr.go`, corroborated by the run's empty `code` column).

## D6 — On the slice-332 baseline

Slice 358's instruction is to take the steady-state baseline from slice 332's
performance audit, and to re-derive rather than assume if it has visibly
shifted.

Experiment 7's steady state is a **status-code contract**, not a throughput or
latency one — "known-kind push succeeds; unknown-kind push returns 400". Unlike
Experiments 1 and 2, it names no request rate and no latency threshold, so
there is no slice-332 load figure this experiment's verdicts depend on. Nothing
in the falsification chain moves if the rate changes.

The latency numbers were captured anyway, and the relevant slice-332 anchor is
what it publishes about this surface: slice 332 §Surface 1 records that slice
015 published **no per-push latency number**, and cites
`Plans/EVIDENCE_SDK.md` §3's stated profile of **"ack within 50ms at p95 under
typical load"**. Measured here: **p95 11ms in steady state, 11ms under
injection**, at ~4 req/s against the audit's 10–100 RPS envelope. That sits
comfortably inside the published budget with no visible shift, so **no
re-derivation was warranted**. Had the push p95 come in near or above 50ms, the
baseline would have been re-derived as its own step before injecting.

The 4 req/s rate is the probe's four classes at one tick per second, and it is
below the audit's 10 RPS envelope. That is a deliberate consequence of the
design's shape — Experiment 7 asks what the platform _returns_, not what it can
_sustain_ — and it is recorded as a limit of this run rather than smoothed over:
this experiment says nothing about registry-outage behavior under ingest load.
Experiments 1 and 2 own that question.

The steady state is captured in-run regardless. A baseline from another machine
on another date is a sanity check; the 60s window captured immediately before
injection, on the same stack, through the same probes, is the control.

**Confidence: high.** The verdicts rest on status codes and ledger landings,
neither of which is rate-sensitive at this scale.

## D7 — Two clocks, kept apart

"Recovery" could mean either of two things here and they are reported
separately rather than blended into one number:

| Clock                                    | Measured                                                           |
| ---------------------------------------- | ------------------------------------------------------------------ |
| Registry read surface answers 200 again  | **1s** after GRANT, polled at 200ms, no atlas restart              |
| Cold-miss push lands in the ledger again | Confirmed in the recovered phase: 1/1 landed, same as steady state |

The first is the design's Rollback claim and it holds literally — no restart, no
cache rebuild, no manual step. The second is the one an operator cares about,
and it also holds: the recovered phase is indistinguishable from steady state
on every class and every column.

What no clock can recover is the record lost during the window. Recovery
restores the _service_; it does not replay the Termed message. That asymmetry —
1-second service recovery, permanent data loss — is the sharpest argument for
G-1: the outage was as short and as mild as an outage gets, and it still cost a
record.

**Confidence: high** for both clocks (mechanically measured); the 1s figure is
an upper bound at 200ms polling granularity.

---

## Resilience gaps and follow-ups filed (slice 358 AC-10, issue AC "any resilience gap surfaced has a follow-up filed")

| ID  | Gap                                                                                                                                                                                                        | Severity | Follow-up      |
| --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- | -------------- |
| G-1 | A registry read failure is laundered into `ErrUnknownKind`, classified as poison, and `Term()`ed — so a transient outage permanently drops valid, acked evidence and records it as `rejected_unknown_kind` | high     | OPENENGINE-443 |
| G-2 | A pusher that receives 201 has no surface on which to discover its record was rejected downstream; there is no receipt-status endpoint                                                                     | medium   | OPENENGINE-444 |
| G-3 | The registry's read surface returns bare 500 under dependency failure, not the design's structured 503 `schema_registry_unavailable` with a correlation id                                                 | medium   | OPENENGINE-445 |

**G-1** (OPENENGINE-443) is the finding. Its severity is about durability, not error text: the
platform's constitutional claim is an append-only evidence ledger, and this is
a path where evidence the platform accepted, acked, and issued a receipt for is
destroyed by a one-second dependency blip with no retry and no operator-visible
signal that anything was lost. The mislabelling makes it worse than a silent
drop, because the audit log actively points the investigator at the pusher —
`rejected_unknown_kind` on a kind that was registered the whole time. The fix
is bounded and needs no new machinery (D4): give `lookupCompiled` a distinct
error for "could not determine", map it to the already-non-poison
`DecisionRejectedInternalError`, and let the existing Nak path redeliver.

**G-2** (OPENENGINE-444) is filed separately because it is a real contract gap
on its own merits and it survives G-1's fix. Even with correct Nak-and-retry, a record
that is genuinely invalid is Termed and the pusher — a connector, a CI job —
was told 201. It cannot poll for the verdict either: the API surface offers
`POST /v1/evidence:push` and `GET /v1/evidence`, so discovering the drop means
listing the ledger and inferring absence. For a platform whose customers
diligence the diligence tool, "we accepted your evidence" followed by no
evidence and no notification is the wrong shape. Sequenced after G-1 because
G-1 decides which rejections are permanent enough to need reporting.

**G-3** (OPENENGINE-445) is the design's literal 503 contract, filed at medium and last on
purpose. It is the wire-level courtesy on top of G-1's data-loss fix, and
closing it alone would produce a platform that reports the outage accurately on
the read surface while still dropping records on the push path. Whoever picks
it up should close G-1 first; the OE body says so.

All three are filed as children of OPENENGINE-389.

**Not filed:** the 500 body's discretion (D5) needs no follow-up — it is
correct. The `unknown_evidence_kind` 412-vs-400 divergence between the design's
literal codes and `http.go:243-245` is recorded here but not filed: it only
affects the synchronous fallback path, the design's codes were written against
a model the platform does not use, and G-2 owns the question of what a pusher
should be told.

---

## What holds, stated plainly

A falsified hypothesis is a successful experiment, and this one falsified half
of its hypothesis. It is worth being equally plain about the half that held,
because both halves were unverified before this run:

- **The hot cache genuinely decouples.** 180/180 known-kind pushes accepted and
  landed with the registry's store unreadable, at unchanged latency. An
  operator whose registry is degraded keeps ingesting every kind their
  connectors already use.
- **Nothing unvalidated entered the ledger.** 180 unknown-kind pushes, 0 rows.
  The ledger-quality invariant the experiment exists to protect was not
  breached in any phase.
- **Recovery is a single second and needs no restart**, and the recovered phase
  is indistinguishable from steady state.
- **No error detail leaked** under dependency failure — generic body,
  request_id correlation, SQLSTATE confined to the server log.

The failure is narrow and specific: the one path where a valid record needs the
registry mid-outage loses that record permanently and blames the sender.

---

## Cross-references

- Slice **335** (`docs/audits/335-chaos-experiment-design.md` §Experiment 7) —
  the design contract executed here, unmodified.
- Slice **068** (`docs/audit-log/068-schema-registry-evidence-kind-fix-decisions.md`) —
  the registry shape the design's checklist item 1 points at; still accurate
  (in-process, Postgres-backed).
- Slice **015** (`internal/evidence/streambuf`) — the ack-at-stream-commit
  contract that makes the design's synchronous status-code expectations
  unreachable, and the `isPoison` / Term-vs-Nak split where G-1 lives.
- Slice **332** (`docs/audits/332-performance-audit-report.md` §Surface 1) —
  the baseline anchor; see D6.
- Slice **367** (`docs/audit-log/367-error-detail-leakage-audit-decisions.md`) —
  the error-detail discipline that holds under this failure (D5).
- Slices **356b** / **357a** — the two prior Experiment executions; this log
  follows their format, and D1's warning about grading the push API by status
  code applies to any further experiment that drives `/v1/evidence:push`.
- Slice **335** spillover slots **354** / **355** — Experiments 1 and 2 remain
  unexecuted; both drive the push API and both should read D1 before starting.
