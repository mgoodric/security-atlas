# Slice 540 — PagerDuty event-driven (webhook) profile — decisions log

Type: JUDGMENT (profile shape + webhook-receipt security + dedup choices).

- detection_tier_actual: none
- detection_tier_target: none

No bug surfaced during the slice. The receiver, verifier, and cmd wiring were
built test-first; every acceptance criterion has a green unit test and the
coverage gate passed on first run. (The one test failure during development — a
false-positive in my own "NOT continuous monitoring" assertion caused by a
newline splitting the phrase — was a test-authoring slip, not a product bug; it
was caught at the `unit` tier in the same edit cycle and is not a detection-tier
event.)

## Decisions made

### D1 — Per-vendor receiver, NOT reuse of `connectors/hris/webhook` helpers

**Options:** (a) reuse the slice-573/655 shared `connectors/hris/webhook`
`HMACVerifier` + bounded-server helpers directly; (b) build a PagerDuty-specific
receiver under `connectors/pagerduty/internal/webhook` mirroring the proven
shape.

**Chosen:** (b). The HRIS shared package is **not vendor-agnostic enough to reuse
as-is**: its `Verifier` interface returns `worker.HRIS`, its `WorkerFetcher` and
`PayloadParser` are typed against `worker.RawWorker`, and its receiver hard-wires
the HRIS trigger+re-read+fan-out pipeline. PagerDuty's pipeline is different
(map-the-payload, no re-read, single incident per delivery, a different
multi-signature header scheme). Forcing a shared abstraction now would either
contort the HRIS package or leak HRIS types into the PagerDuty connector. I
**mirrored the proven shape** instead — the same receive→verify-first→build→push
order, the same gosec-G112 bounded `http.Server`, the same `Serve(ctx)` graceful
shutdown, the same `MaxBytesReader→413` body cap, the same constant-time
`hmac.Equal` — without importing the HRIS package. github + hris each built a
per-vendor receiver for the same reason; this is the established pattern, not a
new one.

**Confidence:** high. A genuine cross-connector shared webhook-receiver
abstraction (github + hris + pagerduty all mirror the same ~8 idioms) is worth
its own refactor slice once a third independent vendor shape exists to factor
against — filed as spillover (see below). Doing it inside slice 540 would be a
premature abstraction.

### D2 — Map the webhook payload to the summary shape; do NOT trigger+re-read

**Options:** (a) treat the webhook as a bare trigger (incident id + event type)
and re-read the incident via the read-only REST API to build the exact summary
shape (the HRIS pattern); (b) map the v3 webhook `data` block's structured
summary fields directly to `incidents.Incident` and build the record from that.

**Chosen:** (b). The PagerDuty v3 webhook `event.data` block **already carries the
exact structured summary fields** the pull profile decodes — id / number / status
/ urgency / service{id,summary} / created_at / resolved_at. Unlike the HRIS case
(where the webhook envelope is genuinely a thin trigger and the authoritative
lifecycle facts live behind a re-read), here a re-read would fetch the same
structured fields the delivery already contains, at the cost of an extra REST
round-trip per delivery **and** requiring the read-only REST token in the
receiver process. Mapping directly is simpler, faster, and means the `subscribe`
profile needs **only** the webhook signing secret — no REST token at all, a
smaller credential surface.

The free-text guard is identical to the pull profile and **structural**: the
`wireDelivery` decode struct has **no** `title` / `description` / `body` /
`notes` field, so `json.Unmarshal` discards the incident free-text at the decode
boundary exactly as the pull client's `apiIncidents` struct does. A test
(`TestServeHTTP_SummaryOnly_NoFreeText`) injects a free-text sentinel into the
payload's title/description/body and asserts it never reaches the emitted record,
and that the banned keys never appear in the payload.

**Confidence:** high. The only revisit trigger is if PagerDuty changes the v3
`data` block to omit a field the summary needs — at which point a bounded re-read
for the missing field (not the free-text) would be the fix.

### D3 — `X-PagerDuty-Signature` scheme: HMAC-SHA256 over raw body, multi-`v1=`, accept-any

**The scheme:** PagerDuty v3 signs each delivery with HMAC-SHA256 over the **raw
request body** keyed by the per-subscription signing secret. The header value is
one or more **comma-separated `v1=<hex>`** signatures. PagerDuty emits **multiple
`v1=` signatures during a signing-secret rotation** (one per currently-active
secret) so a subscriber can roll the secret without dropping deliveries.

**Chosen handling:** the verifier computes HMAC-SHA256(body, secret) **once**,
then walks each comma-separated entry, ignores non-`v1=` schemes (forward-compat
if PagerDuty adds a `v2=`), hex-decodes each `v1=` digest, and **accepts iff any
one matches** via constant-time `hmac.Equal`. Empty/absent header →
`ErrUnsigned`; present-but-none-match → `ErrBadSignature`; both map to a bare
`401`. The compare is over the raw bytes `io.ReadAll` produced (the exact bytes
the signature covers), verified by `TestVerify_RoundTripOnReadAllBytes`.
`TestServeHTTP_MultiSignatureRotation_Accepts` proves the accept-any-of-N rotation
path; `hmacverify_test.go` covers malformed-hex, unknown-scheme-only,
wrong-signature, valid-among-unknown, and tampered-body.

**Confidence:** high for the scheme (HMAC-SHA256 over raw body, `v1=` hex,
multi-sig rotation is PagerDuty's documented v3 behavior). **Revisit:** confirm
against a live PagerDuty v3 subscription's actual header bytes once the maintainer
wires a real subscription — specifically that the digest is lowercase hex and the
separator is a bare comma (the verifier already trims whitespace tolerantly).

### D4 — Receiver lifecycle + bind config

**Chosen:** `--listen` defaults to **loopback** `127.0.0.1:8474` (TLS terminated
by a reverse proxy that forwards the verbatim body — the signature is over the
raw body, so the proxy must not re-encode it); `--path` defaults to
`/webhooks/pagerduty`. The `http.Server` carries the gosec-G112 Slowloris
timeouts (ReadHeader/Read/Write/Idle). `Serve(ctx)` blocks until SIGINT/SIGTERM
(via `signal.NotifyContext`, mirroring the rippling/bamboohr subscribe commands),
then drains with a bounded 5s graceful shutdown. The signing secret is read from
`PAGERDUTY_WEBHOOK_SECRET` (env only, never a flag).

**Confidence:** high. Loopback-default + reverse-proxy-for-TLS is the exact
posture the rippling (`127.0.0.1:8533`) and bamboohr subscribe receivers ship.

### D5 — Cross-profile dedup via the unchanged slice-489 idempotency key

**Chosen:** the receiver reuses `pdrecord.BuildIncident` **unchanged**, which
derives `incidentKey = sha256("pagerduty.incident_summary|<incident_id>|<hour>")`
from the hour-truncated `observed_at`. A webhook-emitted record and a pull-emitted
record for the same incident in the same UTC hour therefore carry the **same**
idempotency key and collapse to one ledger row.
`TestCrossProfileDedup_WebhookKeyMatchesPullKey` builds the same incident through
both the webhook path and the pull `pdrecord.BuildIncident` path and asserts the
keys are byte-equal.

**Confidence:** high. The key is computed in exactly one place (`pdrecord`),
shared by both profiles by construction.

### D6 — Log-injection hardening (b245 CodeQL lesson)

Every user-tainted value reaching a log sink is `%q`-formatted. The receiver logs
exactly one statement on the emit-failure path: `log.Printf("pagerduty/webhook:
emit incident %q failed: %q", id, err)` — both the user-provided incident `id`
**and** the error string (which can embed the id via the build/push wraps) are
`%q`-escaped, so a crafted id with an embedded newline cannot forge a log line
(CWE-117). `TestLogInjection_CraftedIDIsEscaped` asserts a raw newline never
reaches the log and the escaped form does. `TestSecretNeverLogged` asserts the
signing secret never appears in any log output across forged + push-failure +
success deliveries.

**Confidence:** high.

## Revisit once in use

1. **D3 — live header bytes.** Confirm the verifier against a real PagerDuty v3
   subscription's actual `X-PagerDuty-Signature` bytes (lowercase-hex assumption,
   comma separator, exact `v1=` prefix). The verifier is tolerant (whitespace-
   trimmed, unknown-scheme-skipping) but has not been run against live traffic.
2. **D2 — v3 `data` field completeness.** If a future PagerDuty v3 payload omits a
   summary field the record needs (e.g. `service.summary` on some event types),
   add a bounded re-read for that specific structured field — never the free-text.
3. **Rate-limit / coalescing (threat-model D).** v0 bounds body size + verifies-
   first + sets server timeouts, which sheds the dominant flood cheaply (an
   unverified POST costs one HMAC). It does **not** add an application-level rate
   limiter or per-incident coalescing window. If a real subscription produces a
   thundering-herd of lifecycle events for one incident, a short coalescing window
   (or relying on the hour-bucket idempotency key to collapse the writes, which it
   already does at the ledger) is the revisit. Filed-adjacent to the dedup key.
4. **Multi-event-type fan-out.** v0 emits one `incident_summary` record per
   delivery regardless of `event_type` (triggered/acknowledged/resolved/… all map
   to the same summary record, distinguished by the `status` field). If a future
   consumer needs a distinct evidence shape per lifecycle transition (e.g. an
   acknowledgement-latency record), that is a new evidence kind — out of scope
   here. Filed as spillover (see below).

## Confidence summary

| Decision                       | Confidence                         |
| ------------------------------ | ---------------------------------- |
| D1 per-vendor receiver         | high                               |
| D2 map-payload (no re-read)    | high                               |
| D3 signature scheme + rotation | high (scheme) / revisit live bytes |
| D4 lifecycle + loopback bind   | high                               |
| D5 cross-profile dedup key     | high                               |
| D6 log-injection hardening     | high                               |
