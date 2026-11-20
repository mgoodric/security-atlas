# Issue #2760 — Audited IdP-config delete; group-derived roles scoped by source

Split from #2731. No product path deleted an OIDC IdP config, and
`ListGroupDerivedRoles` was not scoped by source: a reconcile through one
source revoked roles another source had derived, and a deleted config's
roles stayed with any user who had no other source.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: integration

The unscoped diff was found in review (#2731), not by a test. The new
`TestDerive_OtherSourceDoesNotRevoke_2760` was run against an unscoped
variant of the query and fails there ("SCIM reconcile revoked
[grc_engineer]"), so it is the test that would have caught it.

## D1 — A per-source table, not a column on `user_roles`

**Decision.** `user_role_group_sources (tenant_id, user_id, role,
idp_config_id NULL)` holds one row per source that derives a role. NULL is
SCIM, as in `oidc_idp_group_mappings`. The resolver diffs only its own
source's rows; the `user_roles` group-derived row goes when no source row
for it remains.

**Why.** `user_roles` keys on `(tenant, user, role)`, so one row cannot say
that two sources derive it. A source column there would need the PK
widened, which every role reader and the admin PATCH would feel. A side
table leaves `user_roles` and its readers unchanged.

FKs, both `ON DELETE CASCADE`: to `user_roles (tenant_id, user_id, role)`,
so the admin PATCH that replaces a user's role set drops stale source rows;
and to `oidc_idp_configs (tenant_id, id)`, so a config deleted by operator
SQL does not leave source rows naming it.

## D2 — Backfill from `group_role_audit_log`

**Decision.** Each existing group-derived role gets a source row for every
source whose latest audit entry for that `(tenant, user, role)` is a grant.
A role with no grant on record, or granted by a config that no longer
exists, gets none.

**Why.** The resolver has audited every grant and revoke since slice 509,
so the log is the derivation history. A role with no source row is never
revoked by a reconcile; that is the safe direction (no role is removed on
a guess), and an admin can remove it through the users API. A role that
the old unscoped diff kept for a second source without auditing that
source's grant gets the second source back at its next reconcile.

## D3 — The endpoint is `DELETE /v1/admin/sso`

**Decision.** The delete is on the existing single-config surface and
removes the `primary` config, like GET and PATCH. There is no
`/{id}` route.

**Why.** The v1 SSO surface is one config per tenant (slice 062). A by-id
route would need a list route the UI does not have.

## D4 — Refuse a delete that strands the last admin

**Decision.** The delete takes the admin-roster lock
(`users.LockAdmins`) and runs `guard.Check` after the revocations; if the
tenant had an active admin and would have none, it returns 409 and rolls
back.

**Why.** The resolver suppresses the last admin's revoke (AC-5). A delete
cannot suppress just one revoke and still delete the config, because the
role would keep no source. Refusing the delete is the same rule, applied
to the whole delete.

## D5 — Every removed derivation is audited, kept roles too

**Decision.** `RevokeConfigRoles` writes a `group_role_audit_log` revoke
row for every source row it removes, with `detail.reason =
"idp_config_delete"` and `detail.role_removed` saying whether the user
lost the role (false when another source or a manual grant keeps it). The
`me_audit_log` row's `after` carries `config_id`, `mapping_count`,
`revoked_role_count` and `affected_user_count`; `before` is the config.

**Why.** The ticket asks for each revocation to be audited. Recording the
kept ones too means the audit log shows that the config stopped deriving
them, which is what "why does this user have this role?" needs.

## D6 — Counts on GET for the confirm

**Decision.** `GET /v1/admin/sso` returns `mapping_count` and
`derived_role_count`, computed the way the delete computes them. The web
confirm shows them; the DELETE response returns the actual counts, shown
after the delete.

**Why.** No extra preview route. The counts are two small indexed counts
per GET.

## D7 — A login racing the delete

**Decision.** The delete reads the config `FOR UPDATE`. A reconcile that
records a derivation against it takes an FK `KEY SHARE` lock on the same
row: if it commits first its row is revoked by the delete; if it waits it
then fails the FK and that sign-in errors.

**Why.** A sign-in through a config being deleted is not expected to
succeed, and neither order leaves a role with a source row naming a
deleted config.
