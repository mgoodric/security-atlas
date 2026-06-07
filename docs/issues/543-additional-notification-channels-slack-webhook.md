# 543 — Additional notification channels (Slack / webhook)

**Cluster:** Backend
**Estimate:** M (1-3d)
**Type:** JUDGMENT (channel abstraction shape + per-channel disclosure + secret handling)
**Status:** `blocked` (depends on #445 — email delivery substrate — merged first)

## Narrative

Slice 445 shipped EMAIL only and explicitly excluded SMS / Slack / webhook
(P0-445-6). Some operators route their alerting through Slack or a generic
webhook (PagerDuty, a SIEM, an internal bot) rather than email. This slice adds
a SECOND (and optionally THIRD) delivery channel behind the SAME thin
abstraction slice 445 established: a `Provider`-style interface, a per-user
opt-in, and the existing notification store as the sole source.

Scope discipline carried from 445: this is still a **delivery sink**, not a
producer. The minimum-disclosure discipline is even MORE load-bearing for Slack
/ webhook than for email, because a webhook target is often a shared channel or
a third-party system — the payload must carry summary counts + a deep-link only,
never notification details.

The hard JUDGMENT this slice owns: (1) whether to generalize the 445 `Provider`
into a multi-channel registry NOW or keep one-interface-per-channel (445
deliberately did NOT build a plugin system — this slice revisits that with a
second real consumer in hand); (2) per-channel disclosure shape (Slack
Block Kit vs a flat webhook JSON); (3) the per-channel secret/credential
handling (Slack bot token / incoming-webhook URL / generic webhook bearer — all
env-or-per-user-config, never logged, with the SAME no-user-controlled-target
guard as 445's no-user-controlled-recipient).

## Threat model

Adds an OUTBOUND-to-third-party surface the email channel did not have: a
webhook URL / Slack channel is a target the operator configures.

- **I — Information disclosure (DOMINANT).** A Slack channel or webhook is
  lower-trust than the user's private inbox (often shared / third-party).
  **Mitigation:** summary counts + deep-link only; NEVER notification details;
  the same minimum-disclosure body discipline as 445, audited per channel.
- **S — Spoofing / SSRF.** A webhook URL is a target. If the URL were
  user-controlled free-text it could point at an internal service (SSRF).
  **Mitigation:** the webhook target is operator/admin-configured (env or a
  validated config surface), not arbitrary per-user free-text; the host is
  validated against an allowlist or restricted scheme/port; the target is never
  derived from notification content.
- **Tampering / injection.** Slack mrkdwn / Block Kit and webhook JSON must
  escape interpolated values for their context (the 445 CRLF/HTML guard's
  analog per channel).
  **Mitigation:** per-channel context-correct escaping; closed type-label map
  reused from 445.
- **Secret handling.** Slack token / webhook bearer env-only, never logged
  (445 D9 analog).

## Acceptance criteria

- [ ] At least one additional channel (Slack OR generic webhook) delivers the
      same minimum-disclosure digest from the existing notification store.
- [ ] Per-user opt-in for the new channel; default opted-out.
- [ ] The target (webhook URL / Slack channel) is operator-configured, never
      user-controlled free-text; SSRF guard on webhook host.
- [ ] Per-channel context-correct escaping; secrets env-only + never logged.
- [ ] Cross-tenant isolation proven (integration test); minimum-disclosure
      proven (no details in payload).
- [ ] Decisions log (channel-abstraction-NOW-vs-later call) + changelog entry.

## Anti-criteria (P0 — block merge)

- **P0-543-1.** Does NOT over-disclose to a lower-trust channel (summary +
  deep-link only).
- **P0-543-2.** Does NOT allow a user-controlled / SSRF-able target.
- **P0-543-3.** Does NOT default users opted-in.
- **P0-543-4.** Does NOT produce notifications (delivery sink only).
- **P0-543-5.** Does NOT log channel secrets.

## Dependencies

- **#445** (email delivery substrate) — establishes the channel abstraction +
  opt-in + notification-store-as-source pattern this generalizes.

## Notes

Parent: slice 445 ("Follow-on slices: ... additional channels (Slack/webhook)";
P0-445-6). Filed as a spillover during the 445 build.
