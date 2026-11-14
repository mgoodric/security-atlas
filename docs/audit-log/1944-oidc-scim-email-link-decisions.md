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

### D1 — Link only an unbound SCIM row with no local password and no super_admin grant. **Confidence: HIGH.**

`LinkUserIdpByEmail` binds the principal to a same-tenant row only when all of
these hold:

- the email matches case-insensitively, the same rule as `GetSCIMUserByEmail`;
- the idp tuple is empty, so a row already bound to a principal is never re-bound;
- `scim_managed = true`;
- the row has no `local_credentials` row and no `super_admins` grant;
- no row holds this principal yet, so a returning user stays on their own row.

The first version also linked local-mode rows. The #2007 review blocked that
(B1). A tenant admin can point `PATCH /v1/admin/sso` at an IdP they control and
assert the seeded local super_admin's email, which took over that account while
its password kept working. Self-registration IdPs, and IdPs where users edit
their own email, open the same path. That contradicted `oidc-setup.md`, where
SSO is additive and the local account keeps working. A local or super_admin row
now keeps the D2 401. The password and super_admin exclusions also apply to
SCIM rows: SCIM provisioning does not make a row safe to claim through an IdP.

### D2 — Email collision with no link is a 401, not a 500 or a second user. **Confidence: HIGH.**

When the link declines and another row holds the email (case-insensitive),
`UpsertOIDC` returns `ErrIdentityConflict` and the callback answers with the
same `401 invalid credentials` and no session that the #1912 deactivation check
uses. The detail goes to the server log only, so the response does not reveal
whether the email has an account. Two unique violations map to the same error:
`users_email_per_tenant_unique` from the upsert, and `users_idp_principal_unique`
from the link when a concurrent sign-in bound the same principal first. Each
would otherwise be a 500. Principals that already have a row are never refused by
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
send the claim as a string, so the string forms are accepted.

**Open with Matt (#2007 review, B2).** The review recommends the reverse:
strict by default, with a per-IdP opt-out for Entra. Entra's `email` claim is
unverified, the nOAuth pattern, and an explicit-false check only stops IdPs that
report it honestly. Until he decides, the policy is `users.UnverifiedEmailPolicy`.
Its zero value, `LinkUnlessUnverified`, is the rule above, and
`LinkOnlyIfVerified` is the strict rule. A per-IdP column only has to set the
field on `UpsertOIDCInput`.

### D4 — Linking does not change status or active. **Confidence: HIGH.**

The link writes only the idp tuple and `display_name`. A deprovisioned SCIM row
(`active=false` or `status='disabled'`) still links, and then the existing
`CanSignIn` check refuses it with 401. Binding the principal there keeps a
later SCIM re-activation working.

### D5 — Audit each link in `me_audit_log`. **Confidence: HIGH.**

A link decides who can sign in as the user from then on, so it writes one
`me_audit_log` row (`oidc_identity_link`, added by migration `20260910000000`)
in the same transaction. `user_id` is the linked user. `after` holds
`idp_issuer` and `idp_config_id` and never the email. A repeat sign-in writes
nothing. A refused sign-in writes no audit row, because nothing changed; the
server log line records `idp_issuer` and `idp_config_id`, without the email.
`me_audit_log` already feeds the unified audit log, so tenant admins see links
next to the other identity events.

### D6 — Multi-IdP caveat: any of the tenant's IdPs can claim a SCIM row. **Confidence: MEDIUM.**

SCIM credentials are not bound to an IdP config. With two OIDC IdPs configured,
IdP B can claim a row that SCIM provisioned for IdP A's user, on the first
sign-in with that email. The D1 exclusions still apply, and the
`oidc_identity_link` audit row records which config made the claim. The fix is
to scope the link by IdP config once SCIM tokens are IdP-bound, which needs that
binding first. Until then, a tenant that runs SCIM should configure one OIDC IdP.
