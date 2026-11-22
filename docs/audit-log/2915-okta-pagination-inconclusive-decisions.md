# #2915 — Okta Link pagination, long-running inconclusive, cc6_1 smart cards (decisions log)

Follow-up from [#2901](https://github.com/mgoodric/security-atlas/issues/2901)
(PR #2913).

- detection_tier_actual: manual_review (the first-page-only reads were found in review of #2913)
- detection_tier_target: contract

A fixture shaped from Okta's documented `Link` headers is the contract-tier
net for the pagination defect, and this PR adds those fixtures.

## Sources

| Fact                                                                                                                                                                                                                                                                  | Source                                                                                                                                                                                             |
| --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Pagination uses a cursor ("opaque to the client") in `after`/`before`. Clients follow the `Link` values "instead of constructing your own URLs". The list ends when no `next` link comes back. The example sends `rel="self"` and `rel="next"` as two `link:` headers | developer.okta.com/docs/api/#pagination and /docs/reference/core-okta-api/#pagination, read 2026-11-22                                                                                             |
| `smart_card_idp` is an authenticator key                                                                                                                                                                                                                              | `AuthenticatorKeyEnum` in `okta/okta-management-openapi-spec` `dist/current/management-minimal.yaml` (master, fetched 2026-11-22). The enum also lists `security_key`, `webauthn`, `yubikey_token` |

Nothing here was checked against a live Okta tenant.

## D1 — one pager for every Okta list call

A new `connectors/okta/internal/oktapage` package holds `List[T]`. The seven
list reads (`/policies?type=MFA_ENROLL`, `/groups`, `/apps`,
`/apps/{id}/groups`, `/policies/{id}/rules`, `/users`,
`/users/{id}/factors`) all go through it. It matches the GitHub connector's
`getPages` (#2928) on three rules:

- the `next` URL is followed as given;
- a `next` URL on another scheme or host is refused, so the SSWS token
  never leaves the configured org;
- the chain stops with an error after `MaxPages` (1,000).

`APIError` moved into the package. Each client package keeps its name as a
type alias, so `errors.As(err, &oktapolicy.APIError{})` still matches.

The factors call still maps a 404 to "no factors". The 404 check now reads
the `APIError` status.

## D2 — "inconclusive since" is computed from the ledger; no migration

`control_evaluations` is append-only, and the scheduler appends a row every
run (hourly by default). A cell's run therefore starts at its earliest
evaluation after its latest non-inconclusive one, or at its first
evaluation if it has never been anything else. `ListInconclusiveStreaks`
computes this with one window pass over the history of the controls it is
given.

A stored `inconclusive_since` column would need its own write on every
evaluation and a backfill. It could also drift from the ledger it
summarises.

The query reads every row of the history of the controls passed to it.
Both callers pass only controls already known to be inconclusive:

- the state endpoint passes one;
- needs-attention passes the controls whose rollup is inconclusive.

If a tenant's ledger makes this slow, a lookback bound or a summary column
is the follow-up.

## D3 — the control's date is the earliest cell's run

The control rollup is worst-of, and inconclusive ranks just below fail. So
no cell of an inconclusive control is failing, and the control has been
inconclusive at least as long as any of its cells' current runs.
Needs-attention takes the earliest of those runs. That date can be earlier
than when the rollup itself turned inconclusive, for example when another
cell was failing until recently. That case is rare enough that it does not
justify a per-instant rollup replay.

The control page's strip shows the same earliest date, under the state
badge. `GET /v1/controls/{id}/state` carries the exact `inconclusive_since`
of each cell.

## D4 — threshold: 7 days, a constant

`needsattention.InconclusiveDays = 7`. One or two days of inconclusive is
normal:

- a daily connector between pulls;
- a freshness window rolling over before the next push;
- a connector upgrade (cc6_1 is inconclusive until `okta.mfa_policy.v2`
  arrives).

A week means the control has no signal and nobody has noticed. That is the
case the ticket names, cc6_1 on a tenant still running the v1 connector.

The value is a constant, like `DueSoonDays` and `policy.AckGraceDays`, not
an environment variable. No operator has asked to tune it, and a constant
keeps the band the same on every deployment.

## D5 — band placement: after source_missing, before overdue

The new band `inconclusive` is ranked 4th. Like `source_missing`, it says a
signal has gone, and it is listed ahead of the calendar's dated work for
the same reason: a control with no evidence cannot show up as failing, so
the failing band cannot cover for it. It is a `control` item, so the
`?type=` filter needs no change.

## D6 — cc6_1: smart cards are noted, not counted

`smart_card_idp` is now named in cc6_1's Not-checked paragraph. A policy
that allows only a smart card fails the check. Counting it would mean:

- mapping `smart_card_idp` to a phishing-resistant value in the connector;
- changing the SQL check;
- deciding whether an IdP-delegated smart card belongs in the same set as
  WebAuthn.

That is a behaviour change, outside the scope of a note. The ticket allowed
either option. The description change re-hashes the stock manifest
(`scripts/gen-stock-control-hashes.sh`), so installs update in place.
