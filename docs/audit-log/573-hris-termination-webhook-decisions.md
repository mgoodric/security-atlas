# Slice 573 — HRIS event-driven termination-webhook profile · decisions log

Slice type: **JUDGMENT** (per-vendor signature scheme, re-read shape, dedup key,
and receiver lifecycle are subjective build-time calls). Claude made the calls,
recorded them here, and the slice ships when CI is green. This log is the
durable record for post-deployment iteration; it does NOT block merge.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. The Go `if`-init composite-literal parse error
on `ripplingParser{}.Parse(...)` was a compile error caught immediately by the
local `go test` loop, not a behavioral defect — not classified as a detection-tier
bug.)

---

## Context + the invariant-#3 confirmation

Slice 491 shipped the Rippling + BambooHR HRIS connectors as **pull-only**
(`profiles_supported = [pull]`) and flagged the event-driven leaver signal as the
highest-value follow-on (P0-491-7). This slice adds the `subscribe` profile to
both connectors.

**Invariant #3 (the platform-side wire is always push) is honored.** The webhook
receiver is a long-lived HTTP server that runs **inside the connector process**
(`atlas-rippling subscribe` / `atlas-bamboohr subscribe`), source-side. It does
**not** add any inbound API to `internal/api/` or the platform. The record still
leaves the connector via the existing `EvidenceIngestService.Push` RPC, exactly
as the pull profile does. `subscribe` is a `profiles_supported` value describing
how the connector retrieves data **from the source** (a webhook the vendor POSTs
to this process); it is not a platform-side wire change. Confirmed by:

- the receiver lives in `connectors/hris/webhook/` (the connector tree), not
  `internal/api/`;
- the receiver's only egress is `webhook.Pusher` (satisfied by `sdk.Client`);
- no migration, no new tenant-scoped table, no `schemaregistry` change (the
  evidence kind `hris.worker_lifecycle.v1` is reused unchanged).

**Prior art note.** `connectors/github/internal/githubwebhook` already established
an HMAC-verifying webhook receiver `Handler` (X-Hub-Signature-256, constant-time
compare, 1 MiB body cap, ReadHeaderTimeout 10s / ReadTimeout 30s, graceful
Shutdown). This slice's shared `connectors/hris/webhook` package generalizes that
shape for the HRIS family and adds three things github's lacked: a reusable
`Verifier` interface (so PagerDuty 540 / MDM 557 can reuse it), the
**trigger + re-read** semantics (the webhook is a trigger; the record is built
from a bounded re-read), and **dedup against the pull profile** (shared
hour-truncated idempotency key). My receiver also uses `http.MaxBytesReader`
(rejects oversized with 413) rather than github's `io.LimitReader` (silent
truncate) — a stricter, better default.

---

## Decisions made

### D1 — Per-vendor signature-verification scheme

Both vendors sign webhook deliveries with **HMAC-SHA256 over the raw request
body**, keyed by a per-subscription shared secret, hex-encoded in a vendor-specific
header. Implemented behind a reusable `webhook.HMACVerifier` (constant-time
`hmac.Equal`), parameterized per vendor:

| Vendor   | Header                 | Encoding      | Prefix | Secret env                |
| -------- | ---------------------- | ------------- | ------ | ------------------------- |
| Rippling | `X-Rippling-Signature` | lowercase hex | none   | `RIPPLING_WEBHOOK_SECRET` |
| BambooHR | `X-BambooHR-Signature` | lowercase hex | none   | `BAMBOOHR_WEBHOOK_SECRET` |

**Verification happens BEFORE any record is built** — the dominant new threat is
that anyone can POST to a webhook receiver. An unsigned delivery (`ErrUnsigned`),
a forged/wrong-secret delivery, or a tampered body (`ErrBadSignature`) is rejected
with a bare `401` (no detail leak) and never triggers a re-read or a push. Tests
assert all three rejections happen before the re-read.

**Assumption (the ambiguous part).** The exact header name + digest encoding each
vendor uses is not pinned to a live integration in this slice (no live vendor in
tests). I picked the documented/most-common shape: HMAC-SHA256 hex in a
vendor-namespaced `X-<Vendor>-Signature` header, which is the predominant webhook
signing convention (Stripe, GitHub, Shopify all follow HMAC-SHA256-hex variants).
The verifier is parameterized (`HeaderName`, `Prefix`, `Encoding`) so adjusting to
each vendor's confirmed scheme is a one-line change at the call site — no
re-architecture. `EncodingHexUpper` is already supported in case a vendor emits
uppercase hex.

Confidence: **medium** (the HMAC-SHA256-over-raw-body core is near-universal and
high-confidence; the exact header name/casing per vendor is the part to confirm
against a live tenant).

### D2 — Webhook-payload-alone vs trigger + re-read (per vendor)

**Decision: trigger + re-read for BOTH vendors.** The webhook payload is treated
as a **trigger** carrying only the affected worker id (+ event type); the
authoritative lifecycle facts are fetched via a new **read-only single-worker
re-read** (`GetWorker` → `FetchOne`) that requests the **same minimal `fields`
set** the pull profile uses. Rationale:

- the over-collection guard (P0-491-3) stays **identical** to the pull path — the
  re-read decodes only the allowed lifecycle fields into the same PII-bounded
  structs (a leak would be a compile error);
- the webhook payload shape is the least-stable part of a vendor's contract;
  re-reading from the documented directory API is more robust and avoids trusting
  a (possibly richer-than-needed) webhook body;
- it guarantees the webhook-emitted record is byte-identical in shape to a
  polled one, which is what makes the dedup key collide (D3).

The receiver tolerates a re-read that returns "worker no longer present"
(`ok=false`) by acknowledging the delivery (`200`) and emitting nothing.

Confidence: **high**.

### D3 — Dedup key extension (dedup against the pull profile)

**Decision: reuse `idem.WorkerLifecycleKey` UNCHANGED** — no new key shape. That
key is `sha256("hris.worker_lifecycle|<hris>/<worker_id>|<hour>")`, where the hour
is the UTC-hour-truncated `observed_at`. The receiver runs the re-read result
through the same `worker.Normalize` (which hour-truncates `observed_at`) and the
same `workerrecord.Build`, so a webhook-emitted record and a pull-emitted record
for the **same `(hris, worker_id, hour)`** derive the **same idempotency key**.
The append-only ledger collapses them — a termination is never double-written via
both a webhook and a subsequent poll within the hour.

`TestReceiver_DedupKeyMatchesPull` asserts the webhook key equals the pull key for
the same worker + clock, and equals the canonical `idem.WorkerLifecycleKey`.

Trade-off: the collision window is the UTC hour, not the exact event. A
termination webhook at 14:59 and a poll at 15:01 fall in different hours and would
produce two ledger rows. This is the **same** granularity the pull profile already
has (two polls in adjacent hours likewise produce two rows) and is acceptable —
the worker's terminated status is identical in both rows; the evaluator reads the
latest. A finer-grained event-id dedup would diverge the webhook key shape from
the pull key shape and is explicitly out of scope.

Confidence: **high**.

### D4 — Receiver lifecycle (start/stop, bind address/port)

**Decision:** the receiver is a long-lived `http.Server` (`webhook.NewServer`)
with gosec-G112-satisfying timeouts (ReadHeaderTimeout 10s, ReadTimeout 30s,
WriteTimeout 30s, IdleTimeout 60s), run by `webhook.Serve(ctx, srv)` which blocks
until the context is cancelled then drains with a bounded 5s graceful shutdown.
The connector's `subscribe` subcommand drives it with a SIGINT/SIGTERM-aware
context (`commandContext`), so Ctrl-C or a container stop drains cleanly.

- **Bind address:** `--listen` (default `127.0.0.1:8533` Rippling / `:8534`
  BambooHR — **loopback**). The operator fronts the receiver with a reverse proxy
  for TLS + public exposure; binding loopback by default is the safe stance (the
  receiver is not directly internet-exposed unless the operator chooses to).
- **Path:** `--path` (default `/hooks/rippling` / `/hooks/bamboohr`).
- **Body bound:** 64 KiB (`webhook.MaxBodyBytes`), enforced via
  `http.MaxBytesReader` BEFORE the body is read; oversized → `413`.

Confidence: **high**.

---

## Revisit once in use

1. **D1 — confirm each vendor's exact webhook signature scheme against a live
   tenant.** Verify the header name (`X-Rippling-Signature` / `X-BambooHR-Signature`),
   the digest encoding (hex casing), and whether either vendor prefixes the value
   (e.g. `sha256=`) or signs a canonical string other than the raw body. The
   verifier is parameterized so this is a call-site tweak. **Top of the list
   (medium confidence).**
2. **D2 — confirm the webhook envelope field paths.** The Rippling parser reads
   `data.employeeId` (falling back to `data.id`); the BambooHR parser reads the
   first `employees[].id`. Confirm against real deliveries; adjust the per-vendor
   `PayloadParser` if the envelope differs.
3. **BambooHR multi-employee fan-out.** A BambooHR webhook can carry multiple
   changed employees in one delivery; the v0 receiver acts on the **first**
   changed employee (a single termination is the dominant leaver case). A
   multi-employee fan-out (re-read + push each) is a documented follow-on
   (spillover slice **655**).
4. **Replay / timestamp-tolerance.** Neither vendor's delivery currently carries a
   verified timestamp the receiver checks, so a captured valid delivery could be
   replayed. The hour-truncated idempotency key bounds the damage (a replay within
   the hour collapses to the same ledger row), but a delivery-timestamp +
   freshness window would harden it. Revisit when the vendor signature scheme is
   confirmed (it may include a signed timestamp).
5. **Public exposure guidance.** The receiver binds loopback by default; document
   the recommended reverse-proxy + TLS termination posture in the deploy docs when
   an operator first runs `subscribe` in production.

---

## Confidence summary

| Decision                              | Confidence |
| ------------------------------------- | ---------- |
| D1 per-vendor signature scheme        | medium     |
| D2 trigger + re-read (both vendors)   | high       |
| D3 dedup key reuse (vs pull)          | high       |
| D4 receiver lifecycle + bind defaults | high       |
