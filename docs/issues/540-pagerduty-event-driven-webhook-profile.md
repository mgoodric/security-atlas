# 540 — PagerDuty connector: event-driven (webhook) profile

**Cluster:** Connectors
**Estimate:** L (3-5d)
**Type:** JUDGMENT (profile shape + webhook-receipt security + dedup choices)
**Status:** `blocked` (depends on #489 — base PagerDuty connector — merged first)

## Narrative

Slice 489 shipped the PagerDuty connector **pull-only** on a named honest
interval (recommended 24h). It deliberately deferred an **event-driven profile**
(P0-489-7). PagerDuty offers incident-lifecycle webhooks (v3 webhook
subscriptions: `incident.triggered`, `incident.acknowledged`,
`incident.resolved`, `incident.escalated`, …), which would let the connector
emit incident-summary evidence as incidents happen rather than on a daily poll.

This slice adds an event-driven `subscribe` profile that RECEIVES PagerDuty
webhook deliveries and emits `pagerduty.incident_summary.v1` records (the same
summary-only shape slice 489 froze — id / number / urgency / status / service /
timestamps; never free-text). It must register `profiles_supported` HONESTLY:
`subscribe` describes how the connector retrieves data FROM the source (it
receives a webhook); the platform-side wire is still always push (invariant #3).

The hard JUDGMENT this slice owns is **webhook-receipt security**: PagerDuty
webhooks are signed (HMAC over the body with a per-subscription secret). The
connector must verify the signature before processing, reject unsigned/forged
deliveries, and dedup against the pull profile (the same incident must not
double-write the ledger via both a webhook and a subsequent poll — the slice 489
idempotency key already collapses same-incident/same-hour writes, but the cross-
profile interaction must be confirmed).

## Threat model

Source-credential-heavy PLUS an inbound-untrusted-data surface (the webhook
receiver) that the pull-only base connector did not have.

- **S — Spoofing (DOMINANT — new surface).** Anyone can POST to the webhook
  endpoint. **Mitigation:** verify the PagerDuty webhook HMAC signature against
  the per-subscription secret before processing; reject unsigned/invalid
  deliveries. The signing secret is source-side config, never logged.
- **T — Tampering.** sha256 content-hash per emitted record; ingest validates.
  Signature verification also covers in-transit tampering of the delivery.
- **R — Repudiation.** Register-per-run/subscription + stable `actor_id`
  (`connector:pagerduty:incidents@<version>`) + `observed_at` granularity.
- **I — Information disclosure.** A webhook body carries incident fields; emit
  the SUMMARY shape only (the slice 489 boundary) — never the incident title/body
  even if the webhook includes it.
- **D — Denial of service.** The public receiver is a flood target; bound body
  size, rate-limit, verify-then-process, and shed unverified load cheaply.
- **E — Elevation of privilege.** Read-only source token for any pull
  backfill; the webhook receiver has no platform privilege beyond push
  (invariant #3).

## Acceptance criteria (sketch — refine at pickup)

- [ ] A webhook-receive `subscribe` profile lands; `profiles_supported` is
      registered honestly (`subscribe`), platform wire still push.
- [ ] PagerDuty webhook HMAC signature verification before processing; forged /
      unsigned deliveries rejected (the dominant security AC).
- [ ] Emits `pagerduty.incident_summary.v1` — the slice 489 summary-only shape;
      never incident free-text even when the webhook body includes it.
- [ ] Cross-profile dedup confirmed (webhook + poll of the same incident collapse
      to one ledger row).
- [ ] Pushes to the single `IngestEvidence` (`Push`) API — no wire change.
- [ ] Tests cover signed-delivery accept + forged-delivery reject + emit shape;
      no live PagerDuty.
- [ ] A test asserts no incident free-text enters a record; a test asserts the
      signing secret + token are never logged.
- [ ] README + decisions log + changelog.

## Anti-criteria (P0)

- **P0.** No platform-side wire change (push only — invariant #3).
- **P0.** No processing of an unverified/forged webhook delivery.
- **P0.** No incident free-text in a record (summary-only — the slice 489
  boundary holds).
- **P0.** No token / signing secret logged or transmitted into the platform.
- **P0.** Profile name is honest: `subscribe` reflects genuine webhook receipt,
  not a relabeled poll.

## Dependencies

- **#489** (base PagerDuty connector) — provides the connector scaffold, the
  `pagerdutyauth` + `pdhttp` packages, the `pagerduty.incident_summary.v1` shape,
  and the IR-evidence family.
