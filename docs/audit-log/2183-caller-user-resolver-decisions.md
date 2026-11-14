# #2183 — one shared caller-user resolver — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2183
**Date:** 2026-11-14

A local-login / PKCE / device-grant JWT bridges Subject `user:<uuid>` into
`cred.UserID`. Handlers that parsed `cred.UserID` raw treated a signed-in person
as a non-user. #2162 fixed `/v1/me` with a package-local `callerUserUUID`; this
change moves the resolver into `credstore.CallerUserID` and uses it everywhere.

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: manual_review (the first commit); integration (the self-demotion guard and the audit-notes HTTP authz gap, both surfaced by new tests)
- `detection_tier_target`: integration

The bug was found in review of #2162, not by a test: the integration suites for
metrics, tenants and policy acks minted JWTs with a bare-uuid Subject, which no
real login produces. The new tests use `user:<uuid>`, and each fails on the
pre-fix handlers.

---

## D1 — the helper lives in `credstore`, not `jwtmw`

`jwtmw` imports `credstore` (it builds the bridged `Credential`), so a helper in
`jwtmw` taking a `Credential` would be a cycle. `credstore` already held the same
rule as `MinterUserID`; that function now delegates to `CallerUserID`.

## D2 — what resolves

`user:<uuid>` and a key bound to a user (bare uuid, not its own ID) resolve.
An unbound key (`UserID == ID`, even when the id is uuid-shaped), `bootstrap-admin`,
`oauth_client:<id>`, a non-uuid after the prefix and the nil uuid do not.
Authorization inputs (`resolveRoles`, `authz.BuildInput`) keep the raw
`cred.UserID`; only identity-for-records reads the resolver.

## D3 — the guard is a Go test, not a new lint recipe

`TestNoRawCredUserIDParse` (in `internal/api/credstore`) walks `internal/`,
`cmd/` and `pkg/` and fails on `uuid.Parse` / `uuid.MustParse` of a
`cred.UserID` / `Cred.UserID`, directly or through a prefix stripper. It runs in
`Go · build + test` with no CI wiring. It relies on the repo's naming convention
for credential variables; `TestRawCredUserIDParse_Pattern` pins what it catches
and what it leaves alone (`req.UserID`, `caller.UserID` in the policy store,
which now receives an already-resolved id).

## D4 — status codes for non-user callers

- Metrics manual input: still 400, message now "manual input requires a user
  credential" (the old "not a UUID" text was wrong for a `user:` subject).
- Policy acknowledge from a non-user key: was a 500 (parse error in the store),
  now the store's `ErrAckMissingUser`, which the handler already maps to 401.
- `/v1/me/acknowledgments` for a non-user: unchanged empty 200 envelope.

## D5 — notifications are addressed to the bare users(id)

`notifications.recipient_user_id` is TEXT. Staleness and offboarding write the
bare uuid, and the email / Slack / webhook digests look it up by
`userID.String()` from UUID-keyed opt-in rows. Audit-note replies were the odd
producer out: they addressed the prior authors' raw `cred.UserID`, so a
`user:<uuid>` author got a row no digest delivered. Meanwhile
`/v1/me/notifications` matched the raw `cred.UserID`, so the same person
missed every bare-uuid notification. Added at the dispatcher's request on the
same PR.

- One canonical form, the bare uuid: it is what the majority of producers and
  every channel already use, so no data migration or channel change is needed.
- Readers (`List`, `MarkRead`; `unread_count` rides on `List`) resolve the
  caller with `CallerUserID`. A non-user gets an empty 200 envelope from `List`
  (as `/v1/me/acknowledgments` does) and a 404 from `MarkRead`. It was a 401
  only for an empty `UserID`.
- The audit-note producer maps authors through the new
  `credstore.SubjectUserID` (a stored principal string, not a live credential),
  dedupes one person seen in both forms, and drops the note's own author under
  either form. Authors that are not users get no notification, since no reader
  could ever see it.
- Channel opt-in already resolved through the shared helper (`parseCredIDs`,
  updated in the first commit).
- Rows written before this change are rewritten by migration
  `20261021000000` (D7).

## D6 — `internal/identity` holds the prefix rule

The `user:<uuid>` rule is needed below the API layer (the auditor attrs
resolver in `internal/audit/auditor`), where importing `internal/api/credstore`
would invert the layering. `identity.UserID` is a dependency-free function that
both `credstore.CallerUserID` and the resolver call, so there is one copy of the
rule. `CallerUserID` adds the one check a bare string cannot make: an unbound
key whose own id is uuid-shaped is not a user.

## D7 — migration `20261021000000` normalizes stored principals

`notifications.recipient_user_id` and `auditor_assignments.user_id` are
rewritten from `user:<uuid>` to the lower-cased bare uuid. Only a canonical
uuid suffix qualifies; the nil uuid, `key_...`, `oauth_client:...` and the e2e
fixture's email id are left alone. A grant held in both forms collapses to the
bare row before the UPDATE, so the `auditor_assignments` primary key cannot
collide. The migration is idempotent; its test applies it twice.

The down migration is a no-op. The rewrite does not record which rows it
touched, and a rewritten row is indistinguishable from one written bare, so
re-adding the prefix would corrupt rows that never had it.

## D8 — auditor assignments key on the bare users(id)

Both readers resolve to it: `/v1/me/audit-period(s)` through `CallerUserID`,
and `auditor.DBAttrsResolver` through `identity.UserID`. Only the resolver's
lookup key changes; the raw `cred.UserID` still flows into `resolveRoles` and
`BuildInput`. A principal that names no user hydrates no audit periods, even
when an assignment row carries that exact string. An empty `UserID` on
`/v1/me/audit-period(s)` keeps its 401 (AC-4).

Found while testing, outside #2183: the HTTP authz middleware never sets
`input.resource.attrs.audit_period_id` for `POST /v1/audit-notes`, so the
`auditor_period_matches` write rule cannot match over HTTP for any auditor,
bare or prefixed subject (verified: both get 403). The end-to-end test asserts
the write at `Engine.Decide` with the period supplied, and the gap is reported
on PR #2186 for its own ticket.

## D9 — other callers moved to the resolver

- `adminframeworkversions` `actorID` takes the credential and resolves it with
  `CallerUserID`; it parsed `jwtmw.SubjectUserID(subject)` on a string the
  grep guard could not see.
- The `adminusers` self-demotion guard compared the raw `cred.UserID` with the
  bare target id. For a local-login admin that never matched, so the admin
  could drop their own admin role with no confirmation (the new test returns
  200 on the old code).

## D10 — audited, left alone

- `audit_notes.author_user_id` stays the raw `cred.UserID`. Private-note
  visibility filters on it by exact match; normalizing it changes who sees
  which note and belongs in its own change.
- `claims.Subject` parses (`adminsuperadmins`, `adminauthzbundle`,
  `admintenants`, `adminframeworkversions`) read the JWT, not a credential, and
  already strip the prefix.
