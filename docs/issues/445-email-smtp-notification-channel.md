# 445 — Email/SMTP notification channel (delivery substrate)

**Cluster:** Backend
**Estimate:** S (0.5-1d)
**Type:** JUDGMENT (template copy + provider abstraction shape)
**Status:** `ready`

## Narrative

Notifications today are **in-app only**: `internal/audit/notifications` writes
to the `/v1/me/notifications` store, surfaced in the web app. But a solo
security leader does **not live in the app** — they live in their inbox. The
staleness digest (slice 439), audit-sample-due reminders, and board-prep
reminders are all worthless if the operator has to open the app to discover
them. The platform needs an **email delivery substrate** so notifications can
reach the operator where they actually are.

This slice ships a **thin SMTP/email-provider abstraction** plus an **opt-in
per-user email channel** that delivers from the **existing notification store**
(it does not invent a parallel notification source — it is a delivery sink for
notifications already written by slices 029 / 439 / future producers), plus
**one digest email template**. Self-host configuration is via environment
variables (SMTP host/port/credentials), matching the single-VM self-host target.

**Scope discipline.** A **delivery substrate**, not a notification producer:
this slice reads the existing notification store and delivers selected
notifications to email. It ships **one** template (the digest). It does **not**
ship SMS / Slack / webhook channels (follow-ons), does **not** ship a rich
notification-preferences UI beyond the per-user email opt-in toggle, and does
**not** change how notifications are _produced_. **Follow-on slices:** wiring
slice 439's staleness digest to email delivery; per-notification-kind email
preferences; additional channels (Slack/webhook). Slice 439's in-app surface
ships independently of this slice; this slice is what lets 439 reach the inbox.

## Threat model (STRIDE)

The email channel transmits tenant-confidential notification content over SMTP
to operator inboxes. Dominant risks: **SMTP credential handling**, **PII /
over-disclosure in email bodies**, and **template injection / open-relay
abuse** via operator/notification-derived content.

**S — Spoofing.** The email channel sends FROM the deployment's configured
sender; it does not accept inbound mail.
**Mitigation:** the SMTP sender identity is operator-configured (env); the channel
is send-only (no inbound mail server). The opt-in email address per user is the
user's verified account email (not arbitrary free-text), so a user cannot
redirect another user's notifications to an attacker address.

**T — Tampering / template injection (PRIMARY).** Notification content
(evidence kinds, control codes, operator-entered text) is interpolated into the
email body/subject. A malicious value could inject headers (CRLF in the
subject ⇒ header injection / added recipients) or HTML/script into an HTML body.
**Mitigation:** all interpolated values are escaped for the template context
(HTML-escaped in an HTML part; CRLF-stripped from header fields like
subject/recipient); recipient is the user's account email only (no
user-controlled recipient field); no notification content can introduce a new
header or recipient (open-relay / injection guard).

**R — Repudiation.** Whether a digest email was sent should be traceable.
**Mitigation:** the delivery attempt + outcome (sent/failed) is recorded against
the notification (or a delivery log); idempotency prevents double-send of the
same digest.

**I — Information disclosure (PRIMARY).** Email is a lower-trust channel than the
authenticated app; an email body must not over-disclose. Also: a misconfigured
recipient or a cross-tenant notification could send Tenant A's content to the
wrong inbox.
**Mitigation:** the email body carries the **minimum** — summary counts + a
deep-link back into the authenticated app for detail — NOT raw evidence
payloads, S3 URLs, or confidential control text. Delivery is strictly to the
**notification's own tenant-scoped target user**; the channel reads the
notification under the same tenant context that produced it (no cross-tenant
fan-out). An integration test proves Tenant A's notification never emails
Tenant B's user.

**D — Denial of service.** A flood of notifications could generate an email
storm; a slow/unreachable SMTP server could block.
**Mitigation:** email delivery is digest-batched (not per-notification spam) and
rate-limited per user; SMTP sends have a timeout; delivery failures are logged
and retried with backoff, not retried hot.

**E — Elevation of privilege.** No new role; the opt-in is per-user and only
affects that user's own delivery.
**Mitigation:** a user can only opt their own account in/out; no path lets a user
configure delivery for another user.

## Acceptance criteria

**Backend — provider abstraction + channel**

- [ ] **AC-1.** A thin email-provider abstraction lands (an interface with an
      SMTP implementation), configurable via environment variables (host, port,
      sender, credentials) for self-host.
- [ ] **AC-2.** An opt-in per-user email channel delivers selected notifications
      from the **existing** `/v1/me/notifications` store (it does not produce
      notifications).
- [ ] **AC-3.** Delivery is strictly to the notification's tenant-scoped target
      user's account email (no user-controlled recipient field).
- [ ] **AC-4.** One digest email template ships, rendering a summary + a
      deep-link back into the authenticated app (minimum-disclosure body).
- [ ] **AC-5.** Delivery is idempotent (a given digest is not double-sent) and
      rate-limited per user.

**Backend — safety**

- [ ] **AC-6.** All interpolated notification values are escaped for context:
      HTML-escaped in the HTML part; CRLF-stripped from header fields (header-
      injection / open-relay guard).
- [ ] **AC-7.** The email body carries no raw evidence payloads / S3 URLs /
      confidential control text — summary + deep-link only.
- [ ] **AC-8.** Delivery outcome (sent/failed) is recorded; SMTP sends have a
      timeout + backoff retry (no hot retry).

**Frontend / config**

- [ ] **AC-9.** A per-user opt-in toggle for the email channel exists (minimum
      preference surface); default is opted-out (operator opts in).
- [ ] **AC-10.** Self-host docs document the SMTP env config.

**Tests**

- [ ] **AC-11.** Integration test: an opted-in user's digest is delivered via a
      test SMTP sink; outcome recorded.
- [ ] **AC-12.** **Header-injection test:** a notification value containing CRLF
      cannot inject a header or extra recipient (threat-model T).
- [ ] **AC-13.** **Cross-tenant test:** Tenant A's notification never emails
      Tenant B's user (threat-model I).
- [ ] **AC-14.** Unit test: the template escapes HTML in interpolated values.
- [ ] **AC-15.** Integration test: idempotency — re-running delivery does not
      double-send.

**Docs / JUDGMENT artifact**

- [ ] **AC-16.** A decisions log
      (`docs/audit-log/445-email-channel-decisions.md`) records the provider-
      abstraction shape, the minimum-disclosure-body call, and the "Revisit once
      in use" list.
- [ ] **AC-17.** A changelog entry.

## Constitutional invariants honored

- **#6 — Tenant isolation via RLS.** Delivery reads notifications under their
  own tenant context; cross-tenant email proven absent.
- **Self-host target.** SMTP config via env; runs on the single-VM self-host
  shape (canvas §10.1).
- **Minimum-disclosure discipline.** Email bodies carry the minimum; detail lives
  behind the authenticated app.

## Canvas references

- `Plans/canvas/07-metrics.md` — notifications + reminders surfaces.
- `Plans/canvas/10-roadmap.md` §10.1 — self-host (single-VM, env-configured).
- `Plans/canvas/08-audit-workflow.md` — audit-sample-due reminders (a future
  email-channel consumer).

## Dependencies

- **#029** (Audit-Hub notifications / `/v1/me/notifications` store) — `merged`.
  The notification source this channel delivers from.
- **#439** (staleness digest) — **not yet merged**; a downstream _consumer_ of
  this channel, NOT a dependency of it. This slice ships independently; 439's
  email delivery is a 439 follow-on that uses this substrate.

## Anti-criteria (P0 — block merge)

- **P0-445-1.** Does NOT allow a user-controlled recipient address — delivery is
  to the account email only (threat-model S/I).
- **P0-445-2.** Does NOT allow header injection / open-relay via notification
  content (CRLF-stripped — threat-model T; proven by AC-12).
- **P0-445-3.** Does NOT email Tenant A's content to Tenant B's user
  (threat-model I — proven by AC-13).
- **P0-445-4.** Does NOT over-disclose — email body is summary + deep-link, no
  raw evidence / S3 URLs / confidential control text.
- **P0-445-5.** Does NOT produce notifications — it is a delivery sink for the
  existing store.
- **P0-445-6.** Does NOT ship SMS / Slack / webhook channels — email only;
  follow-ons.
- **P0-445-7.** Does NOT default users to opted-in — opt-in is explicit.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (integration-first; header-injection + cross-tenant
tests load-bearing) · `security-review` (SMTP creds + injection + disclosure) ·
`simplify` · `changelog-generator`.

## Notes for the implementing agent

- **Phase-2 grill output:** this is a _substrate_, not a feature — its job is to
  let other notifications reach the inbox. Keep the provider abstraction thin
  (one interface + SMTP impl); do not over-build a multi-provider plugin system
  in v0.
- **JUDGMENT calls you own:** the digest template copy, the minimum-disclosure
  body shape, and the rate-limit defaults. Record in the decisions log.
- The header-injection guard (AC-12) is the classic email-channel footgun — the
  subject line is the most common injection vector; strip CRLF there
  unconditionally.
- Detection-tier: `none` unless a bug surfaces; an injection gap caught in
  review would be `target=integration, actual=manual_review`.
