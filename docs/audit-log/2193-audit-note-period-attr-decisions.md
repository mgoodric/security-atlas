# Issue #2193 — Auditors write audit notes over HTTP

The auditor.rego write rule for `audit-notes` compares
`input.resource.attrs.audit_period_id` with the auditor's assigned periods.
Nothing set that attribute: `authz.BuildInput` always produces empty resource
attrs, and the comment claiming "the handler" set it was wrong, since the
handler runs after the decision. Every auditor got 403 on
`POST /v1/audit-notes`.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: integration

## D1 — Hydrate in the authz middleware, through a hook

**Decision.** `authzmw.MiddlewareWithResourceAttrs` takes a
`ResourceAttrsFunc` whose result is set on `input.resource.attrs` before
`engine.Decide`. `authzmw.Middleware` is that with a nil func, so every other
caller and route is unchanged. The server wires one func,
`auditnotes.PeriodResourceAttrs`.

**Why.** The period is in the request body, and only the middleware sees the
request before the decision. A hook keeps body parsing for one route out of
the generic middleware, and the route's own package owns the body shape.

## D2 — Validate the period, fail closed

**Decision.** The hydrator acts only on `POST /v1/audit-notes` with a tenant
on the context. It sets `audit_period_id` only when `period.Store.Get` finds
the period in the caller's tenant (an RLS-scoped read). A malformed body, a
non-UUID id, or an unknown or other-tenant period leaves the attribute unset,
so the rule has nothing to match and denies. A lookup error is a 500.

**Why.** The attribute is an authorization input, so it is set only from a
validated value. An auditor sending a malformed body gets 403 rather than the
handler's 400; other roles do not use the attribute and still reach the
handler's 400.

## D3 — Read the body once, replay it whole

**Decision.** The hydrator reads at most 1 MiB, then puts those bytes back in
front of the unread remainder. A note body past 1 MiB yields no attribute
(deny for an auditor) but reaches the handler intact.

## D4 — No rule changed

The rego rules are untouched apart from the corrected comment. The `user:`
subject resolving to its bare users(id) for the assignment lookup is #2186's
`identity.UserID` change, which this fix relies on for local-login auditors.
