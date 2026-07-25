# 541 — Wire the staleness digest (slice 439) to email delivery

**Cluster:** Backend
**Estimate:** S (0.5-1d)
**Type:** JUDGMENT (digest cadence + which staleness notifications email-eligible)
**Status:** `blocked` (depends on #445 — email delivery substrate — merged first; and #439 — staleness digest — merged first)

## Narrative

Slice 445 shipped the email delivery **substrate** — a thin SMTP provider, a
per-user opt-in, and an idempotent daily digest that delivers UNREAD
notifications from the existing `/v1/me/notifications` store to the operator's
account email. It deliberately did NOT change how notifications are produced
(P0-445-5) and did NOT wire any specific producer to email.

Slice 439 produces the evidence-staleness digest as in-app notifications. The
solo operator wants those staleness reminders in their inbox, not only in the
app. This slice closes that loop: it ensures slice 439's staleness
notifications are picked up by the slice 445 digest (they already will be, since
445 delivers ALL unread notifications for an opted-in user) and adds the
**scheduled delivery tick** that calls `email.Channel.DeliverDigest` on a
cadence (the 445 substrate provides `DeliverDigest`; it does NOT yet ship a
scheduler/cron that calls it for every opted-in user — 445 left the tick to its
consumers).

The hard JUDGMENT this slice owns: the **delivery tick cadence** (when to sweep
all opted-in users and fire their digest), and whether the staleness digest
warrants its OWN email template distinct from the generic 445 digest (445 ships
ONE generic digest template; a staleness-specific template — "N controls have
stale evidence" — may read better but adds a template surface; default is to
reuse the generic digest).

## Threat model

Inherits the slice 445 threat model verbatim (the delivery path is unchanged).
The new surface is the **scheduler**: a sweep over all opted-in users.

- **I — Information disclosure.** The sweep must run each user's delivery under
  that user's own tenant context (slice 445 already enforces this in
  `DeliverDigest`; the scheduler must not batch across tenants in one tx).
  **Mitigation:** per-user, per-tenant delivery calls; reuse the 445 RLS-scoped
  path; an integration test proves the sweep never cross-delivers.
- **D — Denial of service.** A platform-wide sweep could fan out a large number
  of SMTP sends. **Mitigation:** the 445 per-user idempotency key already bounds
  to one digest per user per period; the scheduler honors the same key and
  paces sends (no unbounded burst).

## Acceptance criteria

- [ ] A scheduled delivery tick sweeps opted-in users and calls
      `email.Channel.DeliverDigest` per user under that user's tenant context.
- [ ] Slice 439's staleness notifications appear in the delivered digest for an
      opted-in user (integration test).
- [ ] The sweep never cross-delivers across tenants (integration test).
- [ ] Cadence is documented + env-configurable; default chosen + recorded.
- [ ] Decisions log + changelog entry.

## Anti-criteria (P0 — block merge)

- **P0-541-1.** Does NOT change the slice 445 minimum-disclosure body or the
  header/CRLF guards.
- **P0-541-2.** Does NOT cross-deliver across tenants.
- **P0-541-3.** Does NOT default opted-out users into delivery (445 opt-in is
  authoritative).

## Dependencies

- **#445** (email delivery substrate) — provides `email.Channel.DeliverDigest`.
- **#439** (staleness digest) — produces the staleness notifications.

## Notes

Parent: slice 445 ("Follow-on slices: wiring slice 439's staleness digest to
email delivery"). Filed as a spillover during the 445 build.
