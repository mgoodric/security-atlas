# 584 — Settings UI toggles for Slack + webhook notification channels

**Cluster:** Frontend
**Estimate:** S (0.5-1d)
**Type:** code (web settings surface over existing opt-in routes)

**Status:** `blocked` (depends on #543 — Slack/webhook channels — merged first)

## Narrative

Slice 543 shipped the backend opt-in routes for the new channels —
`GET/PUT /v1/me/slack-channel` and `GET/PUT /v1/me/webhook-channel` (the
slice-445 `/v1/me/email-channel` shape) — but did not surface them in the web
settings page. Today a user can only flip the new opt-ins via the API.

This slice adds the Slack + webhook delivery toggles to the
Settings → Notifications surface in `web/`, alongside the existing email-delivery
toggle (slice 445's UI). Each toggle is a per-user master opt-in (default off),
reading/writing the slice-543 routes via the BFF, with the same disclosure copy
the email toggle uses ("summary counts + a deep-link only; never notification
details"). A toggle for a channel the deployment has not configured
(env unset → inert) should render disabled with an explanatory note, mirroring
how the email toggle behaves when SMTP is unconfigured.

## Dependencies

- **#543** (Slack + webhook channels) — provides the `GET/PUT /v1/me/{slack,
webhook}-channel` routes this UI drives.

## Anti-criteria (P0)

- Does NOT let a user configure another user's opt-in (the routes already bind
  tenant + user from the credential; the UI only ever calls the self routes).
- Does NOT expose the operator-configured channel target/secret in the UI (the
  toggle is opt-in only; the Slack URL / webhook URL / tokens are server env).

## Notes

Parent: slice 543 decisions-log "What this slice does NOT do". Add a Playwright
e2e assertion for the new toggles per `web/e2e/README.md` if the settings page
already has an e2e spec.
