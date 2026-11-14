# #1944 — OIDC sign-in links SCIM-provisioned users by email — decisions log

**Closes:** https://github.com/mgoodric/security-atlas/issues/1944 (from the
#1920 review of #1912).

## The defect

`CreateSCIMUser` leaves `idp_issuer`/`idp_subject` empty and documents email
as "the join key with a later OIDC sign-in", but `UpsertOIDC` upserted only on
`(tenant_id, idp_issuer, idp_subject)`. A SCIM user's first OIDC sign-in either
hit `users_email_per_tenant_unique` (500) or, when the IdP's email differed
only in case, inserted a second user for the same person (that constraint is
case-sensitive).

- **`detection_tier_actual`: `manual_review`** (the #1920 review).
- **`detection_tier_target`: `integration`.** The SCIM-then-OIDC flow needed
  a real-Postgres handler test; `internal/api/auth/oidc_scim_link_integration_test.go`
  now covers it.

## Decisions

### D1 — Link only to a row with an empty idp tuple. **Confidence: HIGH.**

`LinkUserIdpByEmail` binds the principal to the same-tenant row whose email
matches case-insensitively (the same rule as `GetSCIMUserByEmail`) and whose
idp tuple is empty. A row already bound to any principal is never re-bound.
The query also requires that no row holds this principal yet, so a returning
user stays on their own row. Rows created in local mode also have an empty
tuple and link the same way. The ticket names "an existing same-tenant row
with the same email and an empty idp tuple", without limiting it to
`scim_managed` rows.

### D2 — Email collision with no link is a 401, not a 500 or a second user. **Confidence: HIGH.**

When the link declines and another row holds the email (case-insensitive),
`UpsertOIDC` returns `ErrIdentityConflict` and the callback answers with the
same `401 invalid credentials` and no session that the #1912 deactivation check
uses. The detail goes to the server log only, so the response does not reveal
whether the email has an account. A unique violation on
`users_email_per_tenant_unique` from the upsert (for example, a concurrent race)
maps to the same error. Principals that already have a row are never refused by
this check, so returning users behave as before.

### D3 — Unverified email: refuse only on an explicit `email_verified=false`. **Confidence: MEDIUM.**

| `email_verified` claim | Link?                       |
| ---------------------- | --------------------------- |
| `true` / `"true"`      | yes                         |
| absent / unparseable   | yes                         |
| `false` / `"false"`    | no, so the sign-in is a 401 |

The safe-looking rule is "link only when the claim is `true`". It would break
the main SCIM pairing: Microsoft Entra ID omits `email_verified` from ID tokens
by default, and Entra is the most common SCIM provisioner. The IdP is the
tenant's own, admin-configured `oidc_idp_configs` row, already trusted for
authentication and limited by `AllowedEmailDomains`. An IdP that explicitly
reports the email as unverified is refused, because linking on that email would
let whoever controls the IdP account take over the provisioned user. Some IdPs
send the claim as a string, so the string forms are accepted. Revisit if a
tenant needs strict mode; it would be a per-IdP-config flag.

### D4 — Linking does not change status or active. **Confidence: HIGH.**

The link writes only the idp tuple and `display_name`. A deprovisioned SCIM row
(`active=false` or `status='disabled'`) still links, and then the existing
`CanSignIn` check refuses it with 401. Binding the principal there keeps a
later SCIM re-activation working.
