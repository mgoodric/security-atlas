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

## D5 — audited, left alone

- `notifications` `ListForRecipient` / `MarkRead` match `recipient_user_id` as
  TEXT against `cred.UserID`; there is no parse to fix. Whether producers write
  the bare uuid or the `user:` form is a separate question, not this ticket.
- `claims.Subject` parses (`adminsuperadmins`, `adminauthzbundle`,
  `admintenants`, `adminframeworkversions`) read the JWT, not a credential, and
  already strip the prefix.
