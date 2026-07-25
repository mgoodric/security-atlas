# Slice 655 — BambooHR webhook multi-employee fan-out · decisions log

Slice type: **JUDGMENT** (the parser-contract change shape, the per-employee
partial-failure behavior, and the per-delivery fan-out cap are subjective
build-time calls). Claude made the calls, recorded them here, and the slice ships
when CI is green. This log is the durable record for post-deployment iteration; it
does NOT block merge.

- detection_tier_actual: none
- detection_tier_target: none

(No behavioral bug surfaced during the slice. The interface rename rippled to the
two parser impls + the test helper; every break was a compile error caught
immediately by the local `go build` / `go test` loop, not a behavioral defect — not
classified as a detection-tier bug.)

---

## Context + the invariant-#3 confirmation

Slice 573 shipped the shared source-side HRIS webhook receiver
(`connectors/hris/webhook`) with the `subscribe` profile for Rippling + BambooHR.
The v0 BambooHR receiver acted on the **first** changed employee in a delivery's
`employees[]` array (a single termination is the dominant leaver case) and recorded
the multi-employee fan-out as a documented follow-on (573 decisions log Revisit
#3 → spillover slice 655). This slice is that follow-on's resolution.

**Invariant #3 (the platform-side wire is always push) is honored — and is
unchanged from slice 573.** This slice touches only the SOURCE-side parse →
re-read → push pipeline inside the connector. Confirmed by:

- the only files changed are `connectors/hris/webhook/webhook.go` (the receiver),
  the two per-vendor parser impls (`connectors/{bamboohr,rippling}/cmd/.../cmd_subscribe.go`),
  their tests, and the BambooHR README — **ZERO `internal/api/` changes**;
- the record still leaves the connector via the existing `webhook.Pusher`
  (`sdk.Client` → `EvidenceIngestService.Push`); no inbound platform API is added;
- no migration, no new tenant-scoped table, no `schemaregistry` / `DefaultSeed`
  change (the evidence kind `hris.worker_lifecycle.v1` is reused unchanged).

The signature-verify-FIRST flow, the slice-491 over-collection guard (the
minimal-`fields` re-read), and the `idem.WorkerLifecycleKey` dedup key are all
reused **unchanged** — this slice only changes _how many_ workers a single
delivery fans out to, not _what_ each per-worker step does.

---

## Decisions made

### D1 — Parser-contract change shape (single-id → []id) + how Rippling stays single

**Decision:** change the shared `PayloadParser` interface method from
`ParseWorkerID(body) (string, bool, error)` to
`ParseWorkerIDs(body) ([]string, error)`.

- The old `(string, bool)` two-value (id + "is there an actionable worker") folds
  naturally into a single slice: an **empty slice** IS "no actionable worker" (the
  former `ok=false`), so the interface gets simpler, not wider.
- The **BambooHR parser** now returns every non-blank `employees[].id` (the
  fan-out). The **Rippling parser** returns its single affected worker as a
  **one-element slice** (or an empty slice for an unrelated event). Rippling's
  envelope is structurally single-worker, so the receiver's fan-out loop is a
  no-op for it and Rippling behavior is byte-for-byte identical to the
  pre-fan-out single-worker path. A dedicated test
  (`TestRipplingParser_StaysSingleWorker`) pins "Rippling returns ≤1 id".

**Options considered:**

- _(chosen)_ rename the one interface method to return `[]string`. Cleanest:
  one contract, both vendors conform, the receiver loops. Minimal-diff: the only
  cost is the rename rippling to two impls + the test helper.
- _(rejected)_ keep `ParseWorkerID` and add a second `ParseWorkerIDs` method.
  Two methods for one concept; every impl must implement both or the receiver
  must branch on a capability check. More surface, no benefit.
- _(rejected)_ leave the interface single-id and do the fan-out inside the
  BambooHR parser by... it can't — the parser only sees the body; the re-read +
  push live in the receiver. A BambooHR-only fan-out would have to duplicate the
  re-read/push loop in the cmd layer, diverging BambooHR from the shared receiver.
  The contract change keeps the fan-out in ONE place (the shared receiver) for
  both vendors.

Confidence: **high**.

### D2 — Per-employee partial-failure behavior

**Decision:** the receiver fans the trigger+re-read+push over each id and counts
failures, but **never aborts the loop on a single failure**:

- a worker the source no longer returns (`ok=false`) emits nothing and is **not**
  a failure (the delivery can still ack `200`);
- a transient re-read error or push error for one worker is **logged and
  counted**, and the loop continues to the remaining workers;
- if the failure count is `> 0` after the whole loop, the delivery returns
  `502` (so the vendor retries the _whole_ delivery); the workers that already
  succeeded stay in the ledger and **dedup-collapse on the retry** (same
  `idem.WorkerLifecycleKey` per worker + hour), so the retry is idempotent for
  them and only re-attempts the failed ones.

This satisfies the anti-criterion exactly: one bad employee neither silently drops
the whole delivery NOR fails the healthy employees. The healthy ones are pushed;
the bad one is signalled for retry.

**Options considered:**

- _(chosen)_ push successes, log+count failures, `502` if any failed.
- _(rejected)_ fail-fast (abort the loop on the first error, `502`). Would drop
  every employee after the failing one — exactly the anti-criterion violation.
- _(rejected)_ always `200`, swallow failures. Would silently drop a terminated
  worker's record with no retry — the worst outcome for a deprovisioning-evidence
  signal.

The hour-truncated dedup key is what makes "`502` → vendor retries the whole
delivery" safe: a retry within the hour re-pushes the successes to the same key
(ledger collapses) and re-attempts only the genuinely-failed ones.

Confidence: **high**.

### D3 — Per-delivery fan-out cap (+ in-delivery dedup)

**Decision:** the receiver de-duplicates repeated ids within a delivery
(first-seen order preserved) and bounds the distinct-worker fan-out at
`webhook.MaxFanOut = 100`. Over-cap ids are dropped with a logged warning.

- **Dedup within a delivery:** a delivery that repeats an employee id must
  re-read + push that worker only once (`TestReceiver_DedupsRepeatedIDsInDelivery`).
- **Cap:** a BambooHR bulk status change is realistically a handful to a few
  dozen employees; 100 is well above any genuine single-delivery change and bounds
  a hostile or runaway delivery from triggering an unbounded fan-out of re-reads
  (each re-read is a real outbound API call). The body is already size-bounded at
  64 KiB (slice 573 `MaxBodyBytes`), which independently caps how many ids a
  delivery can physically carry; the explicit `MaxFanOut` is the belt to that
  suspenders and makes the bound legible at the loop.

**Options considered:**

- _(chosen)_ dedup + cap at 100, drop the remainder with a log line, ack `200`.
- _(rejected)_ no cap. The 64 KiB body bound already limits the count, but an
  explicit cap documents intent and defends against a future larger body bound.
- _(rejected)_ cap → `413`/`400` (reject the whole delivery). Over-aggressive:
  the first 100 are legitimate changed workers worth emitting; rejecting the
  delivery drops them all.

Confidence: **medium** (the _behavior_ is high-confidence; the specific number
100 is a judgment to confirm against real BambooHR bulk-change delivery sizes — a
one-constant change if a real tenant ever delivers more).

---

## Revisit once in use

1. **D3 — confirm the `MaxFanOut = 100` cap against real BambooHR bulk-change
   deliveries.** If a real tenant's bulk status change (e.g. a reduction-in-force
   or a mass re-org) routinely exceeds 100 changed employees in one delivery,
   raise the constant (or page the fan-out). One-line change. **Top of the list
   (medium confidence).**
2. **D2 — confirm the `502`-retry semantics against BambooHR's actual webhook
   retry policy.** The partial-failure design assumes BambooHR retries a non-2xx
   delivery and that the retry is idempotent via the hour-truncated dedup key.
   Confirm BambooHR's retry cadence + backoff so a persistently-failing worker
   doesn't cause an unbounded retry storm (the dedup key bounds ledger writes, not
   re-read API calls).
3. **D1 — confirm the BambooHR `employees[].id` envelope path against real
   deliveries** (carried over from slice 573 Revisit #2). The fan-out reads every
   `employees[].id`; if the real envelope nests the id differently or carries the
   change-type per employee (so a non-termination change could be filtered), the
   parser adjusts.

---

## Confidence summary

| Decision                                        | Confidence |
| ----------------------------------------------- | ---------- |
| D1 parser-contract change (single → []id)       | high       |
| D2 per-employee partial-failure behavior        | high       |
| D3 per-delivery fan-out cap + in-delivery dedup | medium     |
