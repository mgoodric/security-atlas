# 2586 — Policy acks: enum-gate admin import, roster from live roles, refuse machine actors: decisions log

Issue: mgoodric/security-atlas#2586, follow-ups from the review of #2580
(#2524).

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found reviewing #2580.
- **detection_tier_target:** integration.
  `TestAcknowledgmentRate2586_RosterIsWhoMayAcknowledge` makes every
  candidate try to acknowledge and asserts the denominator equals the number
  accepted. Against the old `api_keys` roster it fails (everyone rate 0/2,
  viewer rate 0/2); with the `user_roles` roster it passes.
  `TestImportHTTP2586_AudienceEnum` fails against the old import (200, both
  files imported) and passes now.

## Decisions

### D1 — import rejects a free-text audience, with a per-file report; no legacy override

The ticket offered an explicit legacy override or a reject with a report. The
import now fails the file, and its reason names every unrecognised value once
(quoted, in file order) plus the allowed set. It goes through the existing
per-file report, so `partial` behaves as for any other bad file: without it,
nothing is written (422); with it, the other files commit (200).

No override: an imported free-text audience matches no member, so the policy
would never be acknowledged by anyone, and the admin would have to change the
audience anyway. Rejecting at import puts that choice before the policy is
published rather than after. A draft that already carries a legacy value
keeps the #2524 PATCH allowance; that is unchanged.

### D2 — the roster is live `user_roles` joined to active `users`

`CountRequiredRoleUsersForVersion`, `CountFreshAcksForVersion` and the two
subqueries in `ListPoliciesWithAckRate` now count distinct `users.id` with a
`user_roles` row the audience names (any row for `everyone`) and
`users.status = 'active'`. That is what `authz.DBRolesResolver` reads into a
caller's `TenantRoles`: the same `user:` prefix handling and the same
non-active exclusion. `policy.WithEveryone` makes a caller a member exactly
when those roles are non-empty. A `user_roles` row naming no users row (a key,
bootstrap-admin) is not a person and is not counted, the same rule as
`CountActiveTenantAdmins` (#2261).

The numerator applies the same predicate, so an ack by someone off the roster
is recorded but never counted, and the numerator cannot exceed the
denominator.

### D3 — what the roster deliberately leaves out

These still acknowledge but are not on the roster. Both are outside "a member
in the audience", and neither is changed here:

- **The `IsAdmin` wildcard** (super-admin JWT, `is_admin` key) in
  `rolesIntersect` and `ListPendingAcksForUser`. #2524 left it unchanged on
  purpose. The old roster counted `is_admin` key holders as audience members;
  the new one does not, so `TestAcknowledgmentRate_AC6` goes from 2/4 to 2/3
  and the #2524 everyone rate from 3/5 to 3/4. The admin in those fixtures
  holds no live role.
- **A bound key's legacy free-text label** (`authz.AckRoleLabels`), which can
  still match a pre-#2524 free-text audience. Such a policy's rate shows 0
  members; the UI already flags its audience as unrecognised.

If the wildcard should go too (an ack by someone not in the audience means
nothing), that is a separate change to the ack gate and the pending list.

### D4 — machine actors get 403 before `Record`

`Acknowledge` now refuses any credential for which `credstore.CallerUserID`
finds no person (an unbound key, the bootstrap key, an empty user id) with 403
before it parses the id or touches the store. Before, an unbound key with a
live label passed the audience check for an `everyone` policy and failed in
`Record`. 403, not 401: the caller is authenticated, it is just not a person
who can attest. An anonymous request is still 401.
