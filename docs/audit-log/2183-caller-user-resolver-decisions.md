# #2183 — one shared caller-user resolver — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2183
**Date:** 2026-11-14

A local-login / PKCE / device-grant JWT bridges Subject `user:<uuid>` into
`cred.UserID`. Handlers that parsed `cred.UserID` raw treated a signed-in person
as a non-user. #2162 fixed `/v1/me` with a package-local `callerUserUUID`; this
change moves the resolver into `credstore.CallerUserID` and uses it everywhere.

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: manual_review
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
- Rows written before this change addressed to `user:<uuid>` stay as they are:
  they are courtesy notifications, and rewriting them is a data migration
  nobody asked for.

## D6 — audited, left alone

- `audit_notes.author_user_id` stays the raw `cred.UserID`. Private-note
  visibility filters on it by exact match; normalizing it changes who sees
  which note and belongs in its own change.
- `claims.Subject` parses (`adminsuperadmins`, `adminauthzbundle`,
  `admintenants`, `adminframeworkversions`) read the JWT, not a credential, and
  already strip the prefix.
