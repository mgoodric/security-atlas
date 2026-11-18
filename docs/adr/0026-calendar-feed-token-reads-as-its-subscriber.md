# ADR 0026: A Calendar Feed Token Reads as Its Subscriber's Live Roles, for the Feed Only

**Status:** Accepted

**Date:** 2026-11-17

**Decides:** whose access decides which gated event types an ICS calendar feed carries, and what that does and does not let the feed token do.

**Ticket:** [#2331](https://github.com/mgoodric/security-atlas/issues/2331). Decision by Matt on 2026-11-15 (issue comment) and confirmed 2026-11-17. Closes the deferred item 1 of [#2234](https://github.com/mgoodric/security-atlas/issues/2234) (PR #2342).

---

## Context

The calendar shows five gated event types: `risk`, `action_plan`, `offboarding`, `vendor` and `exception`. Each one is shown only to a caller whose OPA decision allows a read of the record type behind it (`gatedEventTypes` in `internal/api/calendar/handler.go`, #2130, #2215, #2232). The JSON calendar asks OPA about the signed-in caller.

The ICS feed (`GET /v1/calendar.ics?token=…`) has no signed-in caller. Calendar clients send only the URL token. Until this change the feed asked nobody and left every gated type out for everyone, admins included.

The token already names a person. Since #2173, `POST /v1/calendar/subscription` mints it with `credstore.IssueOwned`, so its `UserID` is the subscriber's `users(id)`. The feed uses that to apply the subscriber's module access at fetch time. The token carries no roles (`OwnerRoles` is empty), and its kinds are exactly `{calendar.read.v1}` (`Credential.CalendarOnly`).

The ticket first proposed recording the subscriber in `MintedBy`. That field is documented as attribution only ("Nothing authorizes on it"), so using it would have made one field mean two things.

## Decision

1. **The feed decides gated types as its subscriber, at every fetch.** For each gated type the ICS handler asks OPA for a type-level `read` with the subscriber as the user and **no roles of its own**. The engine merges in the subscriber's live `user_roles` rows (`authz.DBRolesResolver`) before it evaluates, as it does for every request. The rule is `calendar.ReadableTypesFor`. A role change takes effect on the next fetch, with no need to subscribe again.
2. **The subscriber is the token's owner (`UserID`), not `MintedBy`.** `MintedBy` stays attribution only. No new column and no migration.
3. **The token never gets more than its subscriber has now, and only on the feed.**
   - It holds no roles, so the decision is never wider than the subscriber's live `user_roles`.
   - Only the ICS handler calls `ReadableTypesFor`, and only for type-level reads of the gated event types. The token reads nothing else.
   - It is refused as a bearer everywhere else. Over HTTP, every `Authorization: Bearer` goes through the JWT verifier, which rejects an opaque token with 401. Over gRPC the interceptor refuses `CalendarOnly` credentials (#2477). `TestCalendarICS_TokenRefusedOutsideTheFeed` guards the HTTP side.
4. **It fails closed.** In each of these cases the feed carries no gated type and still serves the rest:
   - **No subscriber.** A token minted before #2173 has a `UserID` that names no person.
   - **No live role.** The subscriber was removed from the tenant, so they have no `user_roles` row. The feed also falls back to core modules only (#2173).
   - **Disabled subscriber.** The roles resolver returns no roles for a user whose status is not `active`, so the feed carries no gated type. In production the API-key user gate (#1915) also refuses a key whose owner is disabled, so the whole feed is refused with 401.
   - **Failed lookup.** If the role lookup or the OPA decision errors, the error is logged.
   - **No authz engine** (unit-test servers).
5. **Tenant boundary unchanged.** The decision runs in the token's tenant (`tenancy.WithTenant(cred.TenantID)`), and RLS confines both the role lookup and the event query to that tenant. A role the same person holds in another tenant does not count.

## Alternatives considered

| Alternative                                                      | Why rejected                                                                                                                                                                                                                                                                           |
| ---------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Record the subscriber in `MintedBy` and authorize on it          | The token already names its owner. Making an attribution field into an authorization input would need a carve-out ("for calendar tokens only") that every reader of `MintedBy` would have to remember.                                                                                 |
| Snapshot the subscriber's roles onto the token at subscribe time | A demoted or removed person's feed would keep showing what they can no longer read until the token expires, up to a year. Matt's decision is live roles.                                                                                                                               |
| Keep dropping every gated type from every feed                   | This was the state since #2232. It is safe, but the feed then disagrees with the JSON calendar for every role, admins included, which is the bug #2331 reports.                                                                                                                        |
| Refuse the whole feed (403) for a subscriber with no live role   | The ungated, core events carry nothing a former member could not already have seen. Serving them keeps an old subscription from erroring in the calendar client, and it matches #2173's core-modules-only fallback. A disabled user's feed is still refused outright by the user gate. |

## Consequences

- An ICS feed now shows the same gated types as the JSON calendar for the same person, and it narrows at the next fetch after a demotion.
- Subscriptions made before #2173 keep leaving the gated types out until the person subscribes again. The calendar page says so next to the subscribe button.
- Any future feed-style token that reads as its owner follows this shape: owner in `UserID`, no roles on the token, kinds limited to the one surface, the owner's live roles decided per read, and failure means fail closed.
