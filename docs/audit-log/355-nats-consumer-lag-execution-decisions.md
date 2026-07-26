# 355 — Chaos experiment execution: NATS JetStream consumer lag spike (decisions log)

**Slice:** 355
**Type:** JUDGMENT (execution slice)
**Date:** 2026-07-24
**Branch:** `open-engine/OE-383-security-atlas-355-chaos-exp-2`
**Design contract:** [`docs/audits/335-chaos-experiment-design.md`](../audits/335-chaos-experiment-design.md) §Experiment 2 — **not** redesigned here
**Baseline source:** [`docs/audits/332-performance-audit-report.md`](../audits/332-performance-audit-report.md)
**Invariant under test:** constitutional invariant #2 — ingestion and evaluation are separated stages (`CLAUDE.md`; `Plans/canvas/04-evidence-engine.md` §4.3)

---

## Status of this run

**Pre-execution phase COMPLETE. Injection NOT yet performed.**

The design's pre-execution checklist is a gate: slice 355's AC-2 and the
issue's step 2 both say do not inject until every item is signed off here.
Working the checklist surfaced **two hard blockers in the stack under test**
that had to be resolved before any injection could be meaningful. Both are
recorded below with their resolutions, and both are findings in their own
right — they are the reason this slice's first fire ends at the gate rather
than through it.

Nothing was injected. The eval consumer was never paused. The stream and all
four durable consumers are in exactly the state the pre-injection snapshot
records.

---

## Environment under test

Local docker-compose ONLY (slice 335 P0-335-2, slice 355 P0-1, issue
Boundaries). No hosted instance, no atlas-edge, no host outside this
machine's compose stack was contacted, and none is reachable by the tooling
— see D5.

| Item                    | Value                                                                |
| ----------------------- | -------------------------------------------------------------------- |
| Compose project         | `security-atlas` (local bridge network `security-atlas_default`)     |
| Compose file            | `deploy/docker/docker-compose.yml` (the shipped self-host bundle)    |
| atlas HTTP              | `http://127.0.0.1:58080` (loopback-published)                        |
| NATS client / monitor   | `127.0.0.1:54222` / `127.0.0.1:58222` (loopback-published)           |
| NATS server version     | **2.10.29** (image `nats:2.10-alpine`)                               |
| Stack state at snapshot | `atlas` healthy, `postgres` healthy, `nats` healthy, `minio` healthy |

The stack was already running when this slice started: it was brought up by
the sibling execution slice **354** (Experiment 1, DB pool exhaustion), whose
run had completed and whose worktree was clean. The stream therefore carries
354's residue (12,505 messages). That is not a contaminant for this
experiment — Experiment 2 measures _rates and deltas_ (push P95, pending
count growth, drain rate), not absolute stream depth — but it is recorded
here so the pre-injection numbers are not read as a cold-start baseline.

---

## Pre-execution checklist (slice 335 §Experiment 2)

### Item 1 — "Confirm the durable consumer's `ack_wait` is configured to a value greater than 10 minutes"

**Status: NOT SATISFIED AS SHIPPED — blocker, resolution staged.**

Measured directly from the running consumer:

```
ack_wait = 60s   (required: > 600s)
```

This is not a misconfiguration of the local stack; it is the shipped value.
`internal/eval/consumer.go:198` hardcodes `AckWait: 60 * time.Second` in the
eval subscriber's `CreateOrUpdateConsumer` call. Unlike the slice-015 ingest
consumer — whose ack window is a `Config` field with a `DefaultAckWait`
override path (`internal/evidence/streambuf/streambuf.go:69,101`) — the eval
consumer exposes no knob at all. An operator cannot raise it without a code
change.

Resolution staged: `scripts/chaos/nats-consumer-pause.sh ack-wait 15m` raises
it on the live consumer for the duration of the experiment and the run
restores 60s afterwards, honoring P0-3 (the experiment does not permanently
alter consumer configuration). The absence of a configuration surface is
filed as a follow-up (F-1 below) rather than patched here — this is an
execution slice, and widening a consumer's config surface is product work
that belongs in its own slice with its own tests.

### Item 2 — "Snapshot `evidence.evaluations` consumer config BEFORE pausing"

**Status: SATISFIED.**

Captured by `scripts/chaos/nats-consumer-snapshot.sh --out`, read-only,
before anything else touched the stack:

```json
{
  "stream": "EVIDENCE_INGEST",
  "stream_msgs": 12505,
  "stream_last_seq": 12505,
  "consumer": "evidence_eval_worker",
  "ack_wait_ns": 60000000000,
  "max_ack_pending": 1000,
  "paused": false,
  "pause_remaining": 0,
  "num_pending": 0,
  "num_ack_pending": 0,
  "num_redelivered": 0,
  "delivered_seq": 12505,
  "ack_floor_seq": 12505,
  "captured_at": "2026-07-24T21:33:42Z",
  "ack_wait_seconds": 60,
  "ack_wait_gt_10min": false
}
```

Read: the consumer is fully caught up (`ack_floor_seq == stream_last_seq ==
12505`), has zero outstanding acks, zero redeliveries, and is not paused.
That is a clean pre-injection state — the backlog this experiment builds will
be entirely attributable to the injection.

The stream carries **four** durable consumers, not one:
`evidence_ingest_worker` (slice 015 ledger writer), `evidence_eval_worker`
(slice 016 evaluation reaction), `evidence_freshness_drift_worker`, and
`risk_residual_worker` — all at ack floor 12,505. Only the second is the
experiment's variable; the other three must keep draining during the pause
window, and the sampler records all of them so a cross-consumer stall would
be visible rather than inferred.

### Item 3 — "Confirm synthetic-ingest generator uses fresh tenant / idempotency keys"

**Status: SATISFIED BY CONSTRUCTION, not yet exercised.**

The generator mints one idempotency key per request from
`(label, run-epoch, second, index)`, so no key repeats within a run or across
reruns. This matters more here than it looks: the stream is configured with
`Duplicates: 2 * time.Minute` (`streambuf.go:195`) and the publisher sets
`Nats-Msg-Id` to `tenant|idempotency_key`, so a repeated key inside the dedup
window would be collapsed **at the broker** and would silently understate the
backlog the injection is supposed to build. Fresh keys are load-bearing for
this experiment's measurement, not just hygiene.

Not yet exercised against the live API — see the open item in "What the next
fire picks up".

### Gate verdict

**Items 2 and 3 signed off. Item 1 requires the staged `ack-wait` step to run
immediately before injection.** The injection is gated on that step; it has
not been performed.

---

## Blocker discovered: the shipped stack cannot execute the design's injection

**The design's named injection mechanism does not exist on the shipped NATS
version.** Slice 335 §Experiment 2 Variable specifies:

> Perturb via `nats consumer pause <stream> <consumer>` (NATS CLI v0.1.5+)
> or via direct admin RPC call.

JetStream consumer pause (`$JS.API.CONSUMER.PAUSE`, and the `PauseUntil`
consumer-config field the CLI drives) was introduced in **NATS Server 2.11**.
`deploy/docker/docker-compose.yml` pins `nats:2.10-alpine`; the running
server reports 2.10.29. The call is refused by the server, reproducibly,
through the slice's own tooling:

```
$ scripts/chaos/nats-consumer-pause.sh pause 5s
nats-consumer-pause: INJECT pause 5s on EVIDENCE_INGEST/evidence_eval_worker at 2026-07-24T21:35:09Z
nats: error: pausing Consumers requires NATS Server 2.11
nats-consumer-pause: server does not support consumer pause (needs NATS 2.11); see scripts/chaos/compose.chaos-nats-211.yml
exit=4
```

The "direct admin RPC call" alternative in the design does not route around
this — it is the same 2.11 API surface, just called without the CLI.

This is worth stating plainly because it is a resilience finding independent
of the experiment's own hypothesis: **the platform's evaluation stage has no
operator-reachable pause primitive on the version it ships.** Any incident
runbook that says "pause evaluation, let ingest keep buffering, drain later"
— which is precisely the operational posture invariant #2 is supposed to buy
— is not executable against the shipped bundle today. Filed as F-2.

---

## Decisions

### D1 — Reconcile the design's placeholder names against the shipped names; do not treat the mismatch as a redesign

The design names the stream `atlas_eval`, the consumer `evidence-evaluator`,
and the subject `evidence.evaluations`. **None of those exist.** The shipped
shape is:

| Design (slice 335)             | Shipped (`main`)               | Source                        |
| ------------------------------ | ------------------------------ | ----------------------------- |
| stream `atlas_eval`            | stream `EVIDENCE_INGEST`       | `streambuf.DefaultStreamName` |
| subject `evidence.evaluations` | subject `evidence.ingest`      | `streambuf.DefaultSubject`    |
| consumer `evidence-evaluator`  | durable `evidence_eval_worker` | `eval.EvalConsumerDurable`    |

Slice 335 was design-only and written without the running system in front of
it; the names are placeholders for "the evaluation consumer". The _semantics_
the design specifies — a durable consumer that reacts to ingested evidence,
independent of the ledger writer, whose pause must not perturb the push API —
map exactly onto `evidence_eval_worker`. Substituting the real names honors
the design contract; inventing an `atlas_eval` stream to match the document
would be the redesign.

Recorded rather than silently corrected because a future reader comparing the
two documents will otherwise think one of them is describing a different
system.

### D2 — Resolve the 2.11 blocker with a chaos-only compose overlay, not a bundle bump

`scripts/chaos/compose.chaos-nats-211.yml` raises **only** the `nats`
service's image tag to `2.11-alpine`, applied with `up -d --no-deps nats` so
no other service is recreated and no image is rebuilt. The `nats-data` named
volume is untouched, so the stream and all four durable consumers survive the
swap.

Rejected alternatives:

- **Bump `deploy/docker/docker-compose.yml` to 2.11.** That is a product
  change with upgrade-testing obligations (JetStream file-store format,
  operator downgrade path) that an execution slice has no business making as
  a side effect of running an experiment. Filed as F-2 instead.
- **Find a 2.10-compatible pause.** There is none that preserves
  single-variable discipline. Deleting/recreating the durable loses its
  position; `max_ack_pending` throttles rather than stops; blocking the atlas
  process's NATS connection would pause the ledger writer too, perturbing
  ingest — which is the _other_ half of the hypothesis and would make the
  falsification check meaningless.
- **Declare the experiment unexecutable and stop.** The design's intent
  (pause evaluation, measure ingest) is executable; only the shipped server
  version blocks the specific call. Stopping would forfeit a verification of
  the project's most load-bearing invariant over a version pin.

The caveat this introduces is real and is not buried: the injection window
will run against NATS 2.11 while the bundle ships 2.10. For _this_ experiment
the substitution is defensible — the hypothesis concerns the atlas
application's ingest/eval separation, not NATS's own behavior, and 2.11 is
protocol-compatible for everything the atlas client does. It is stated in the
results section as a limitation regardless.

### D3 — Measure push P95 client-side, per request

The falsification check is "push API P95 must not change while the consumer
is paused". Slice 355's implementing note is explicit that it must be
captured every second so the signal cannot hide in an average.

Push latency is therefore measured as curl's own `%{time_total}` per request,
one CSV row per request, rather than read from a server-side histogram. Two
reasons: the stack's Prometheus surface is opt-in and off by default
(`ATLAS_METRICS_FALLBACK_ENABLE` defaults false in the shipped compose), and
a client-side measure is the honest one for this hypothesis — it is what an
evidence-pushing connector actually experiences. Per-request rows also mean
P95 is computed over the exact window boundaries after the fact, instead of
being locked into whatever bucketing a pre-aggregated metric chose.

### D4 — Derive eval latency from consumer ack-floor progression, at 1s resolution

The design's steady state includes "evaluation latency (push → eval-complete)
P95 < 2s". There is no end-to-end timestamp pairing a push with its
evaluation, so eval latency is derived: the sampler records
`(wall_clock, stream_last_seq, ack_floor_seq)` every second, and a message at
stream sequence _S_ is treated as evaluated when `ack_floor_seq` first
reaches _S_. Latency is the gap between the tick where `stream_last_seq`
reached _S_ and the tick where `ack_floor_seq` did.

The resolution ceiling is the 1s sample interval, which is coarse against a
2s threshold. Stated as a limitation rather than papered over: this derivation
can confidently distinguish "seconds" from "minutes" — which is what the
backlog-growth and drain-rate claims need — and should not be read as a
precise P95 against the 2s figure. Sharpening it needs an end-to-end
eval-completion timestamp, which is product instrumentation, not experiment
tooling.

### D5 — Make the local-only boundary structural, not procedural

The issue's hardest boundary is "LOCAL docker-compose ONLY; do NOT target
atlas-edge, any hosted instance, or any host outside this machine's compose
stack." Both tools enforce it by construction, and both refusals were
exercised before use:

- `nats-consumer-snapshot.sh` refuses any monitor URL whose host is not a
  loopback literal, and refuses a URL carrying a path or userinfo component
  (so `http://localhost:8222@edge.example.com/` cannot spoof the prefix).
  Verified: a non-loopback URL exits 1 without issuing a request.
- `nats-consumer-pause.sh` has **no** server-address argument at all. It
  always talks to `nats://nats:4222` from inside a container joined to a
  named local compose network, and refuses a network that does not exist on
  this host or is not a local bridge driver. Verified: a bogus network exits
  1.

A comment saying "do not point this at production" would satisfy the letter
of the boundary. An argument that cannot express a remote target satisfies it
whether or not the next operator reads the comment.

### D6 — Use the official `natsio/nats-box` image rather than installing a CLI

The NATS CLI is not on this host and the repo has no NATS client tooling.
P0-2 forbids introducing chaos-mesh or litmus as a dependency; the concern
behind it is repo dependency weight, which a `docker run --rm` of the
upstream NATS image does not add. Nothing is installed, nothing enters
`go.mod` / `package.json`, and the image is the vendor's own. This is the
design's own tooling stance — "start with docker, add framework only if
needed" (slice 335 cross-experiment observations).

---

## Detection-tier classification

- `detection_tier_actual`: **manual_review** — both blockers (eval consumer
  `ack_wait` hardcoded at 60s; consumer-pause unavailable on the pinned NATS
  version) were found by working the design's pre-execution checklist against
  the live stack.
- `detection_tier_target`: **manual_review** — neither is a code defect a
  test tier would catch. The `ack_wait` value is correct for the ingest path's
  own purposes and only becomes wrong against an operational requirement
  external to the code; the NATS version gap is a deployment-pin question. A
  chaos experiment's pre-execution checklist is the tier that is _supposed_
  to catch both, and it did.

---

## Follow-ups filed

| ID  | Finding                                                                                                                                                                                                                                                                                              | Shape                                                                                                                              |
| --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| F-1 | The eval consumer's `ack_wait` is hardcoded (`internal/eval/consumer.go:198`) with no `Config` surface, unlike the slice-015 ingest consumer. An operator cannot tune the evaluation stage's ack window without a code change.                                                                       | Slice — widen the eval subscriber's config surface to match `streambuf.Config`                                                     |
| F-2 | The shipped bundle pins `nats:2.10-alpine`, on which JetStream consumer pause does not exist. The evaluation stage has no operator-reachable pause primitive on the version the project ships — the operational posture invariant #2 is meant to enable is not executable against the shipped stack. | Slice — evaluate bumping the bundled NATS to 2.11 (JetStream file-store upgrade path, operator downgrade story, Helm chart parity) |

Both are resilience gaps surfaced by the experiment, per the issue's step 9.
Neither is a falsification of the hypothesis — the hypothesis has not been
tested yet.

---

## What the next fire picks up

The tooling and the checklist are done. The remaining work is the run itself:

1. **Resolve the push-API bearer.** The static
   `ATLAS_BOOTSTRAP_TOKEN` credential returns 401
   (`authorization must be \`Bearer <token>\``) against
`GET /v1/evidence`on this stack, and`ATLAS_TEST_MODE` is empty so the
   e2e JWT-mint endpoint is not mounted. The generator needs a working
   credential before it can offer load. This is the one open dependency.
2. Apply `scripts/chaos/compose.chaos-nats-211.yml` (D2) and confirm the
   stack returns healthy.
3. Run `nats-consumer-pause.sh ack-wait 15m` — closes checklist item 1.
4. Capture the steady-state window BEFORE injection at the slice-332
   baseline of 10 ingest/s.
5. Pause for the design's 10 minutes, sampling push latency per request and
   consumer state per second throughout.
6. Resume; measure drain time to zero pending.
7. Restore `ack_wait` to 60s and revert the NATS overlay.
8. Evaluate the falsification check explicitly — **if push P95 moves while
   the consumer is paused, stop and report it as a falsified constitutional
   invariant** (slice 355 AC-9, issue "If blocked"). A falsified hypothesis
   is a successful experiment.

### On the slice-332 baseline

The issue says to re-derive the baseline if it has visibly shifted. It has
not shifted, because **slice 332 never measured one**: that audit was
explicitly read-only and states "No live load was generated." Its 10 req/s
figure is a _design parameter_ — the realistic v1 sustained rate for evidence
push — not an observed measurement. The 10/s offered rate is therefore taken
as specified, and the steady-state latency figures this experiment records
will be the project's first measured ingest baseline rather than a comparison
against a prior one. Worth saying out loud so a later reader does not go
looking for slice-332 numbers to diff against.

---

## Cross-references (AC-8)

- Slice **335** — [`docs/audits/335-chaos-experiment-design.md`](../audits/335-chaos-experiment-design.md) §Experiment 2, the design contract
- Slice **332** — [`docs/audits/332-performance-audit-report.md`](../audits/332-performance-audit-report.md), the baseline parameters
- Slice **015** — the JetStream ingest buffer (`internal/evidence/streambuf/`)
- Slice **016** — the evaluation reaction consumer (`internal/eval/consumer.go`)
- Slice **354** — [`docs/audit-log/354-db-pool-exhaustion-execution-decisions.md`](354-db-pool-exhaustion-execution-decisions.md), sibling execution slice; brought up the stack under test
- `Plans/canvas/04-evidence-engine.md` §4.3 — the ingest/eval separation this experiment verifies
