# 582 — Notification-channel digest scheduler (fan-out driver)

**Cluster:** Backend
**Estimate:** M (1-3d)
**Type:** code (scheduler / cron driver over the existing channel sinks)
**Status:** `blocked` (depends on #543 — Slack/webhook channels — and #445 — email)

## Narrative

Slice 445 (email) and slice 543 (Slack + webhook) each shipped the delivery
SUBSTRATE: a per-user opt-in + a `DeliverDigest(ctx, userID, recipientUserID)`
primitive that builds, claims, and delivers one user's minimum-disclosure
digest. Neither slice shipped the DRIVER that walks all opted-in users on a
schedule and calls `DeliverDigest` for each — today the primitive is invoked
only by tests and (eventually) a caller.

This slice adds that driver: a scheduled job (the slice-metrics scheduler
pattern, `internal/metrics/scheduler`) that, once per digest period, enumerates
opted-in (tenant, user) pairs per channel and invokes the channel's
`DeliverDigest` under each user's tenant context. The claim-before-send
idempotency already in each channel makes the walk safe to re-run (a second
pass the same UTC day is a no-op).

Scope discipline: still a delivery driver, not a producer. It reads the opt-in
tables (`email_channel_optin`, `slack_channel_optin`, `webhook_channel_optin`)
and drives the existing sinks — it never writes a notification and never widens
disclosure.

## Dependencies

- **#543** (Slack + webhook channels) and **#445** (email) — provide the
  `DeliverDigest` primitives + opt-in tables this driver enumerates.

## Anti-criteria (P0)

- Does NOT produce notifications (delivery driver only).
- Does NOT widen per-channel disclosure (drives the existing minimum-disclosure
  builders unchanged).
- Does NOT bypass the per-channel opt-in (default opted-OUT honored) or the
  claim-before-send idempotency.
- Each per-user delivery runs under that user's OWN tenant context (RLS); the
  walk never crosses tenants.

## Notes

Parent: slice 543 decisions-log "What this slice does NOT do" (the per-user
fan-out driver is the named follow-on). The honest-interval discipline applies:
name the digest period explicitly (per-UTC-day), do not market it as
"continuous".
