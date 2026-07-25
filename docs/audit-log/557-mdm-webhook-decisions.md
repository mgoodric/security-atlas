# Slice 557 — MDM event-driven (webhook) profile decisions log

Type: JUDGMENT. This records the subjective build-time calls made for slice 557
(`subscribe` profile + source-side webhook receivers for Jamf + Intune). It does
not block merge; it is the durable record the maintainer iterates from once the
product runs against live Jamf / Intune tenants.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. One dedup-correctness subtlety — the Jamf
webhook id must match the pull profile's numeric REST id — was caught at design
time by reading the pull client, not by a failing test; classified as a design
call, not a defect.)

## Decisions made

### D-JAMF-1 — Jamf credential scheme: shared-secret header (constant-time), NOT body-HMAC

Jamf Pro webhooks do NOT HMAC-sign the request body. The operator configures a
static credential on the Jamf webhook (a Basic-auth header or a custom header
value), and Jamf replays it verbatim on every delivery. We therefore implemented
a thin `mdmwebhook.SharedSecretVerifier` that constant-time compares
(`crypto/subtle.ConstantTimeCompare`) the value carried in a configured header
against the held secret. The header defaults to `X-Jamf-Webhook-Secret`
(`JAMF_WEBHOOK_HEADER` overrides it for operators who use `Authorization`/Basic).
The secret is `JAMF_WEBHOOK_SECRET` (env only, never a flag, never logged). A
missing or wrong credential rejects 401 BEFORE any record is built.

- Options considered: (a) reuse the shared `HMACConfig.Verify` — rejected, Jamf
  does not sign the body, so there is no HMAC to verify; (b) Basic-auth-only —
  rejected as too narrow (Jamf's UI also supports a custom header); (c) a
  configurable shared-secret header (chosen) — covers both Basic (`Authorization`)
  and a custom header via one config axis.
- Assumption (no live Jamf in tests): the exact header name an operator picks is
  deployment-specific; the default name + the `JAMF_WEBHOOK_HEADER` override make
  it a one-line operator config, not a code change. Confidence: **medium** —
  confirm the documented default against a live Jamf webhook config.

### D-JAMF-2 — Jamf webhook id prefers `jssID` (not UDID) for cross-profile dedup

The Jamf PULL profile uses the numeric Jamf REST `id` (the JSS computer id) as the
device id. For a webhook-emitted record to collapse with a pull-emitted one in the
ledger, the webhook path MUST derive the same id. Jamf webhooks carry both `jssID`
(== the numeric REST id) and `udid`; we prefer `jssID` and fall back to `udid`
only when `jssID` is absent. Confidence: **medium** — verify the webhook `jssID`
equals the inventory `id` against a live tenant (the dedup test pins key equality
given equal ids; this decision is about which webhook field == the pull id).

### D-INTUNE-1 — validationToken handshake lives in the Intune ADAPTER, before `Handle`

Microsoft Graph sends a `validationToken` query parameter when it creates/renews a
change-notification subscription; the receiver must respond 200 with the token
echoed as `text/plain` within ~10s and must NOT process it as a delivery. The
shared `webhookrecv.Handle` is verify-first-then-build and assumes a real POST
delivery, so the handshake does not fit cleanly inside it. Per the slice directive
("prefer letting the Intune ADAPTER own that branch rather than changing the
shared package"), a `validationHandler` wraps the `mdmwebhook.Receiver`: it
intercepts a `validationToken` request FIRST (echo 200, no clientState check, no
record), then delegates every real delivery to the shared verify-first skeleton.
The shared package was NOT modified. Confidence: **high** — the handshake is a
well-documented Graph contract; the echo is byte-verbatim and bounded.

### D-INTUNE-2 — clientState is a custom one-method Verifier, not a body-HMAC

Graph does not HMAC-sign notification bodies; authenticity is the per-subscription
`clientState` value echoed in each notification. We implemented
`mdmwebhook.ClientStateVerifier` (the shared `Verifier` interface is one method, so
a clientState check fits) that constant-time compares the body's `clientState`
against the held secret (`INTUNE_WEBHOOK_CLIENT_STATE`). A Graph delivery is a
BATCH (`value[]`); the extractor requires EVERY notification in the batch to carry
the same matching clientState and rejects a partially-forged batch wholesale.
Confidence: **high**.

### D-PAYLOAD — map the webhook/notification payload, do NOT trigger+re-read (both vendors)

The slice gave a per-vendor choice: map the payload if it carries enough for the
posture-summary, else trigger a read-only single-device re-read. Neither connector
has a single-device read method today, and adding one would expand the read
surface. Both vendors' deliveries carry the posture fields: Jamf computer-webhook
events carry the inventory event; Graph rich notifications carry `resourceData`
posture properties. **Decision: map the payload directly** into the same
`devposture.RawDevice` the pull profile produces, never reading beyond the
posture-summary. When a real payload omits a posture field it is simply absent
(stable-optional convention) — honest under-reporting, never over-collection, and
no new read method / scope. Confidence: **medium** — confirm the live
Jamf-webhook-event and Graph-rich-notification field sets match the modelled keys;
if a vendor's notification proves to carry only a device id in practice, a future
slice can add a read-only single-device re-read using the SAME slice-490 field set
(this slice deliberately does not, to avoid widening the read surface speculatively).

### D-DEDUP — confirmed by test, idem package reused unchanged

`connectors/mdm/idem.DevicePostureKey` is reused UNCHANGED; the webhook receiver
builds via `devrecord.Build` (UNCHANGED), which derives the key. The shared-package
test `TestServeHTTP_DedupWithPull_SameIdempotencyKey` asserts the webhook-emitted
record's key is byte-identical to `idem.DevicePostureKey(mdm, device, hour)` (what
the pull profile derives). Confidence: **high**.

### D-LIFECYCLE — bind config: loopback default, per-vendor port, reverse-proxy TLS

Each `webhook` subcommand binds loopback by default (`127.0.0.1:8476` Jamf,
`127.0.0.1:8477` Intune; `--listen`/`--path` override) and drains gracefully on
SIGINT/SIGTERM via the shared `Serve(ctx)`. TLS is terminated at a reverse proxy
in front of the process (Graph requires HTTPS with a valid cert). Confidence:
**high** — mirrors the pagerduty (slice 540) / hris (slice 573) subscribe
receivers.

### D-SHARED — built onto `connectors/shared/webhookrecv` as its first external consumer; shared package untouched

The MDM receivers are a thin adapter (`connectors/mdm/mdmwebhook`) onto the slice-656
shared package: `NewServer`/`Serve`/`Handle`/`Verifier`. The shared package's
existing tests stay green (it was just refactored byte-identical in 656); no shared
file was modified. The validationToken non-record path is owned by the Intune
adapter rather than bent into the shared seam (D-INTUNE-1). A spillover for a
first-class shared "validation handshake" hook was considered and NOT filed: a
single Graph-style consumer does not yet justify generalizing the seam (file it
when a second validation-handshake connector — e.g. another Graph resource —
arrives). Confidence: **high**.

## Revisit once in use

1. **D-JAMF-1 (medium):** confirm the documented default webhook header name +
   shared-secret scheme against a live Jamf Pro webhook configuration; adjust the
   default / docs if operators predominantly use Basic-`Authorization`.
2. **D-JAMF-2 (medium):** confirm a live Jamf webhook's `jssID` equals the
   computers-inventory `id` (so webhook+pull dedup actually collapses in
   production, not just in the equal-id unit test).
3. **D-PAYLOAD (medium):** confirm the live Jamf-webhook-event field set and the
   Graph rich-notification `resourceData` field set carry the modelled posture
   fields. If a vendor's notification in practice carries only a device id, file a
   follow-on to add a read-only single-device re-read using the SAME slice-490
   field set (never beyond the posture-summary).
4. **D-INTUNE-2 (high):** confirm Graph's notification batch shape +
   `clientState` placement against a live subscription; the extractor's
   uniform-batch requirement is conservative (rejects a mixed batch) — revisit if
   Graph legitimately mixes subscriptions in one delivery.
5. **Subscription lifecycle (not in scope):** this slice ships only the RECEIVER.
   Creating/renewing the Graph subscription (Graph subscriptions expire and need
   periodic renewal) and registering the Jamf webhook are operator setup steps,
   documented in the subcommand help. A future slice could add a `subscribe`
   management helper if operators want the connector to self-manage the
   subscription lifecycle.

## Confidence summary

| Decision    | Confidence |
| ----------- | ---------- |
| D-JAMF-1    | medium     |
| D-JAMF-2    | medium     |
| D-INTUNE-1  | high       |
| D-INTUNE-2  | high       |
| D-PAYLOAD   | medium     |
| D-DEDUP     | high       |
| D-LIFECYCLE | high       |
| D-SHARED    | high       |
