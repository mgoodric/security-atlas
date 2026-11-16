# #2085 — `<Person>` resolver: decisions

detection_tier_actual: integration
detection_tier_target: unit

The integration test caught `/v1/people` belonging to no module
(`featureflag.Modules`), which the module gate turns into a deny. The
unit-tier `TestEveryRouteBelongsToAModule` guard caught it as well, once
it ran; it should have been run before the integration tier.

## D1 — a new tenant-scoped `GET /v1/people`, not the admin lists

The user and credential lists (`/v1/admin/users`, `/v1/admin/credentials`)
are admin-only, so every other role (owner, contributor, auditor, viewer)
would have seen "Unknown" everywhere. And for a super-admin,
`/v1/admin/users` goes cross-tenant through the BYPASSRLS pool, keyset-paged
over every tenant. Matt approved a new read (via the dispatcher):

- any signed-in role (OPA admit in `defaults.rego`, exact path, role
  required);
- app pool under the caller's RLS tenant, plus an explicit `tenant_id`
  predicate, never `authPool`, so a super-admin sees only the tenant they
  are signed in to;
- users as `{id, display_name, status}`, with a blank display name falling
  back to the email's local part on the server, so no email address
  leaves;
- API keys as `{id, owner_id}` (`minted_by`); no token, prefix or last4.

The admin endpoints are unchanged. Each list is bounded at 5000 rows. A
tenant is a 50-150-person company, so the bound keeps the scan finite
without paginating a directory the page needs whole.

## D2 — API keys render as "API key (owner: Name)"

Keys have no label column. The ticket's "API key '<name>'" has nothing
to fill it, and last4 is part of the secret, so it is not a name. A key
reads "API key (owner: <name>)", or "API key" when its minter does not
resolve. Labels are #2384.

## D3 — resolution rules

`resolvePerson` (`web/lib/person.ts`) is pure, so vitest covers every
case without a DOM (vitest is node-only, Q-3):

- `user:` is stripped once and the rest must be a non-nil UUID, as
  `identity.UserID` does;
- active user → name and initials;
- `disabled` user → "Former user" (the only non-active `users.status`);
- a key ID → the key form; a `user:` subject never resolves as a key;
- anything else (a `jwt:` credential, another tenant's user, a deleted
  row, a failed or refused directory fetch) → "Unknown", the ID in the
  tooltip only.

A user whose membership was revoked (row deleted) reads "Unknown", not
"Former user": nothing distinguishes a deleted row from a foreign ID.

## D4 — one fetch per page

`<Person>` reads the directory through TanStack Query under one key with
`staleTime: Infinity`, so every `<Person>` on a page shares one request.
While it loads, the component shows "…", never the ID. Names render as
React text.

## D5 — scope split

The dispatcher capped the change near 25 files. This PR covers the pages
the Playwright criterion names: action plans (list and detail), audit
periods ("Created by" on the list, "Frozen by" on the detail page) and
personnel checklist items. Evidence and activity actors, dashboard
activity, board-pack actors, exceptions, risk history, org-mapping
overlays and the checklist approver move in #2385.
