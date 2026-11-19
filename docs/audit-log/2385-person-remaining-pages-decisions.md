# #2385 — `<Person>` on the remaining pages: decisions

detection_tier_actual: none
detection_tier_target: none

Follow-up to #2085. The dashboard activity feed, the activity page and
risk history already rendered through `<Person>` on `main` (#2138, #2086),
so this change covers the rest of the list plus the raw IDs a sweep for
`{x.*_by}` / `{x.actor}` turned up.

## D1 — super admins are named by the super-admin list, not `/v1/people`

`/v1/people` is tenant-scoped by design (#2085 D1), and a super admin's
users row lives in their own tenant. `GET /v1/admin/super-admins` already
returned `display_name` + `email` from a LEFT JOIN under the session
tenant's RLS, which is NULL for every super admin of another tenant.

The list now fills a NULL name from the BYPASSRLS `authPool`, the pattern
`adminusers`, `admintenants` and `meapi.NewTenants` already use for
super-admin cross-tenant reads: a primary-key-bounded
`SELECT id, display_name, email FROM users WHERE id = ANY($1)` over the
listed super admins only. The endpoint stays super-admin-only (handler
check plus OPA), and the columns are the ones it already returned for the
session tenant. A super admin can already list every tenant's users via
`/v1/admin/users`, so no new reach is granted.

`<Person>` takes an optional `directory` prop; the page builds one from
the list (`superAdminDirectory`), so the row and the demote dialog render
through `<Person>` and the user ID moves to the tooltip. A blank display
name falls back to the email's local part, the `/v1/people` rule.

## D2 — `freeText`: a field that holds a typed name or a reference

A board pack's `published_by` is the name the approver typed at publish
(`publish-footer.tsx`, "Approver name"); an evidence record's `actor_id`
is a connector name (`aws-config`), a service account (`ci.test`) or,
for a human attestation, the caller's principal (`credstore.PrincipalOf`).
Run through `<Person>` unconditionally, a typed name would read
"Unknown".

`<Person freeText>` shows a value as written unless it is
reference-shaped (`isActorReference`): a UUID, a `user:` or `jwt:`
subject, a `key_…` credential, `bootstrap-admin`, `system[:…]` or
`oauth_client:…`. Those resolve as before and never show raw. The
Source filter's option labels, which must be strings, use the same rule
through `actorText`.

Changing publish to record the caller instead of a typed name would be a
product change to the AI-assist approval record and is out of scope.

## D3 — org-mapping authors switch from the server name to `<Person>`

`OverlayProvenance.created_by_name` (#2286) is resolved server-side and
reads an API key as "API key …<last4>", where `<Person>` reads
"API key '<label>' (owner: <name>)" without the last four (#2384). The
author now renders through `<Person>` like every other actor; the field
is left on the wire.

## D4 — the Playwright spec seeds real rows, no route mocks

`fixtures/e2e/person-names-2385.sql` seeds a user who requested an
exception (as a `user:` subject), ten `exception_audit_log` rows by that
user (the dashboard feed shows the ten newest events, so other specs'
writes between the seed and the visit cannot push all of them out), and
a published board pack whose `published_by` is the user's ID.
