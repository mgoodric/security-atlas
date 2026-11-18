# #2331: ICS feed shows gated event types by the subscriber's live roles

Builds on `docs/audit-log/2234-calendar-followups-decisions.md` (D1, which
deferred this here). The trade-off is recorded in
[ADR 0027](../adr/0027-calendar-feed-token-reads-as-its-subscriber.md).

detection_tier_actual: none
detection_tier_target: none

## D1: The subscriber is the token's owner, not `MintedBy`

The ticket proposed `IssueMinted(..., mintedBy)`. By the time this was
built, #2173 already minted the token with `IssueOwned`, so `UserID` is
the subscriber's `users(id)` and the feed already reads module access
from it. Reusing that keeps `MintedBy` attribution only, as its doc
comment says, and needs no migration. The dispatcher's brief for this
ticket asked for exactly that reuse.

## D2: A person with no live role still gets the core feed

A removed subscriber (no `user_roles` row) gets the ungated events of the
core modules and no gated type. The calendar client keeps working, and it
shows nothing a former member could not already have seen. A disabled
subscriber is refused outright in production by the API-key user gate
(#1915). Without that gate (the test server), the roles resolver's
`status = 'active'` check gives the same no-gated-types answer. The ADR
records both outcomes.

## D3: A failed role lookup is logged and fails closed, not a 500

The dispatcher's brief says a role lookup error gives no gated types.
`ReadableTypesFor` returns the fail-closed filter along with the error,
and the handler logs it and serves the rest of the feed. A calendar
client cannot surface a 500, and retrying it would not help.

## D4: The decision input carries no roles of its own

`authz.BuildInput` reads a credential off the request, but the ICS request
has none. The handler builds the input directly: the subscriber's id, no
roles, `is_machine_actor=false` and `is_super_admin=false`. The engine's
resolver then adds the live `user_roles`. The token cannot add a role
because it supplies none.

## D5: HTTP refusal of the token is already structural

I first added a `CalendarOnly` refusal to `httpAuthMiddlewareWithExemptions`.
That middleware is no longer mounted: every `Authorization: Bearer` goes
through `jwtmw`, which answers an opaque token with 401. I reverted the
edit. `TestCalendarICS_TokenRefusedOutsideTheFeed` pins the behaviour on
`/v1/calendar`, `/v1/risks`, `/v1/vendors`, `/v1/me` and the subscribe
route. It passed before the fix too, so it is a regression guard, not the
falsifier for this change.

## D6: UI note has no date

Feeds minted after #2173 (2026-11-16) gain the gated types without
re-subscribing. Only older ones stay fail-closed. The page cannot know
which kind a person has, so the note says "an older feed URL … if yours is
missing them, re-subscribe" and gives no cutoff date.
