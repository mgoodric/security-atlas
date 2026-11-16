# #2085 — `<Person>` resolver: decisions

detection_tier_actual: manual_review
detection_tier_target: integration

Review caught the wrong owner column (B1, `minted_by` for `issued_by`)
and the directory cache outliving a tenant switch (S1). The integration
test, which seeded only `minted_by`, should have pinned the owner column;
it now seeds both and asserts a key with no `issued_by` has no owner.
Earlier, the integration test caught `/v1/people` belonging to no module
(`featureflag.Modules`); the unit-tier `TestEveryRouteBelongsToAModule`
guard catches that too.

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
- API keys as `{id, owner_id}`, the owner being `api_keys.issued_by`;
  no token, prefix or last4.

The admin endpoints are unchanged. Each list is bounded at 5000 rows. A
tenant is a 50-150-person company, so the bound keeps the scan finite
without paginating a directory the page needs whole. Each list is read
with `LIMIT 5001`; one that overflows is trimmed and logged
(`people list truncated`), since past the bound some actors read
"Unknown".

## D2 — API keys render as "API key (owner: Name)"

Keys have no label column. The ticket's "API key '<name>'" has nothing
to fill it, and last4 is part of the secret, so it is not a name. A key
reads "API key (owner: <name>)", or "API key" when its owner does not
resolve. Labels are #2384.

The owner is `api_keys.issued_by`. `minted_by` records the admin who
minted the key for attribution, and `apikeystore.IssueInput` documents
that it is not the owner, so a key with no `issued_by` reads plain "API
key"; `minted_by` is never a fallback. The first cut read `minted_by`;
review caught it (B1).

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

## D4 — one fetch per visit, dropped on a tenant switch or sign-out

`<Person>` reads the directory through TanStack Query under one key with
a 30-second `staleTime` (`lib/people-cache.ts`), so every `<Person>` on
a page shares one request and the next visit fetches again. While it
loads, the component shows "…", never the ID. Names render as React text.

The browser QueryClient is a singleton that outlives both a tenant switch
(the switcher only calls `router.refresh()`) and a sign-out (a server
action that redirects in place), so a cached directory could resolve
names against the previous tenant. Review caught it (S1). The directory
is now dropped:

- on an in-tab switch, in the switcher's `onPick`;
- on a sibling tab's switch, in the switcher's broadcast handler, and in
  every mounted `<Person>`'s subscription to the same broadcast;
- on sign-out, by `SignOutForm` on submit.

`lib/people-cache.test.ts` pins that a broadcast tenant switch, or a
forget, makes the next read fetch again.

## D5 — scope split

The dispatcher capped the change near 25 files. This PR covers the pages
the Playwright criterion names: action plans (list and detail), audit
periods ("Created by" on the list, "Frozen by" on the detail page) and
personnel checklist items. Evidence and activity actors, dashboard
activity, board-pack actors, exceptions, risk history, org-mapping
overlays and the checklist approver move in #2385.
