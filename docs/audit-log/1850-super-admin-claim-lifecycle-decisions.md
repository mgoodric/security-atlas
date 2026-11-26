# #1850 — super_admin claim lifecycle — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/1850 (from the
#1833 review of #1757).

## The defects

| Path                      | Defect on `main`                                                                                                                                                                                                                       |
| ------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `oauth/device_approve.go` | Snapshot set `SuperAdmin: cred.IsAdmin`. A tenant admin's credential has `IsAdmin`, so approving a device code would mint a platform-global token once that credential's UserID parses as a UUID.                                      |
| `oauth/token.go` exchange | Copied `super_admin` from the presented token without checking `super_admins`. A demoted user could re-exchange indefinitely and keep the claim and the cross-tenant shortcut.                                                         |
| `adminsuperadmins` Demote | Deleted the row but left live tokens carrying the claim for up to `AccessTokenLifetime` (1h).                                                                                                                                          |
| `oauth/device_approve.go` | Stored `cred.UserID` verbatim. Under a JWT session that is `user:<uuid>`, which the UUID column rejects, so a JWT-authenticated approval returned 500. Found while writing the docs item, which points operators at `atlas-cli login`. |

- **`detection_tier_actual`: `manual_review`** (the #1833 review).
- **`detection_tier_target`: `unit`** for the claim logic, `integration` for the
  table read and the UUID column. The device-approve integration test asserted
  the bug (`want true (inherited from IsAdmin)`), so the tier existed but
  encoded the wrong expectation.

## Decisions

### D1 — One live check, `internal/auth/superadmin`. **Confidence: HIGH.**

All three paths call `superadmin.Confirm(ctx, checker, subject)`, which re-reads
`super_admins` and fails closed (nil checker, non-UUID subject, query error all
return false). `cmd/atlas` builds one `DBChecker` on the BYPASSRLS pool the
`DBUserResolver` already uses to _set_ the claim, so the claim is granted and
re-checked from the same source.

The query matches the subject's users.id directly OR any active users row with
the same `(idp_issuer, idp_subject)`. A super_admins row is keyed on one
tenant's users.id, and a token minted in another tenant carries that tenant's
users.id, so a direct-only match would wrongly strip the claim from a
multi-tenant super_admin. This mirrors `DBUserResolver.lookupSuperAdmin`.

### D2 — Demote: drop the claim per request, not revoke JTIs. **Confidence: MEDIUM-HIGH.**

The ticket asks to revoke the user's outstanding JTIs on demote. atlas does not
record issued JTIs (`oauth_revoked_tokens` holds only revoked ones), so
enumerating them would need a new issued-token table written on every mint.
Revoking would also log the user out of every tenant they still belong to,
which the demote should not do.

Instead the JWT middleware, for any token carrying `atlas:super_admin`,
re-reads `super_admins` and clears the claim (and the bridged credential's
`IsAdmin`) when it no longer holds. The token stays valid for its tenants; only
the claim goes, from the next request. This closes the 1h window with no
migration. The cost is one indexed lookup per request, and only on
super_admin tokens.

Token exchange applies the same check, so a demoted user can neither carry the
claim forward nor use the super_admin cross-tenant shortcut.

### D3 — Nil checker in the middleware skips the re-check. **Confidence: MEDIUM.**

`Server.AttachSuperAdminChecker` is wired only by `cmd/atlas`. Unit and
integration servers that mint super_admin JWTs with synthetic subjects keep
working unchanged. The exchange and device-approve paths fail closed on a nil
checker instead, because a missing wiring there would otherwise grant the
claim. In production all three are wired from the same checker.

### D4 — The test-mode JWT issuer records the super_admin grant. **Confidence: HIGH.**

The Playwright harness mints its tokens through the test-only
`/v1/test/issue-jwt`, which defaults to `super_admin: true`. With the D2
re-check, those claims were dropped (no `super_admins` row backs them) and
49 e2e specs lost admin access. The fix is in the test issuer, the mint
that sets the claim in test mode: when it mints a super_admin token it
inserts the subject into `super_admins` (`ON CONFLICT DO NOTHING`), the
test-mode analogue of the production mint reading the claim from that
table. The insert lives only in the issue-jwt handler, behind the same
`ATLAS_TEST_MODE` gate (checked again inside `grantTestSuperAdmin`).
Production wiring is unchanged, and e2e keeps exercising the real
re-check.

Rejected: skipping the middleware re-check when `ATLAS_TEST_MODE=1`. That
is a test-mode carve-out in production wiring, and e2e would never run the
demote path.

**Default subject change.** The issuer's default subject was
`test-admin:<tenant>`, which is not a UUID and so cannot hold a
`super_admins` row. It is now a deterministic UUIDv5 of the tenant
(`testSubjectFor`). No spec asserts on the old string. Handlers that parse
the subject as a UUID now see a UUID with no `users` row instead of a
parse failure, and both end in the same not-found paths. A super_admin
request with an explicit non-UUID `user_id` is refused with 500 rather
than minted with a claim the middleware would drop.
