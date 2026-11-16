# 2428 — Handler gates read live tenant roles: decisions log

Issue: mgoodric/security-atlas#2428 (folds in the population route of #2425).
`jwtmw` sets `cred.IsAdmin` and `cred.IsApprover` from the `atlas:super_admin`
claim only. A tenant `admin` or `grc_engineer` who signed in normally (local
login, OIDC, PKCE, device grant) carried neither flag, so every handler gate
written as `cred.IsApprover || cred.IsAdmin` or `cred.IsAdmin` refused them,
while OPA, which merges the live `user_roles` rows (#2239), admitted them. E2E
never caught it because the shared test bearer is a super-admin.

The model was decided by Matt (2026-11-16) on the dispatcher's QUESTION
thread; D1 to D4 record it.

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found while building #2115
  (PR #2419), which had to mint its e2e approver as super_admin.
- **detection_tier_target:** integration.
  `TestTenantGates2428_RealJWTRoleMatrix` (real ES256 JWTs through jwtmw and
  authzmw, real Postgres) fails with 63 refusals when the helpers read only
  the flags, and passes with them reading the live roles.

## Decisions

### D1 — helpers over flags; IsAdmin, IsApprover and jwtmw unchanged

Handler gates call `authz.IsTenantAdmin` / `authz.IsTenantApprover`. jwtmw
does not derive `IsAdmin` from tenant roles, because `IsAdmin` also carries
install-wide meaning: `install_reset`, `install_state` and the tenants
handler's cross-tenant fallback read it.

### D2 — the roles are the live user_roles, not the JWT claim

authzmw records the roles the resolver returned for the decision on the
request's credential (`credstore.Credential.TenantRoles`, in memory only).
It replaces any value the credential arrived with, and it excludes the
roles derived from the credential itself: an unflagged machine key derives
`grc_engineer` for OPA and must not become an approver. A JWT's role claim
(`OwnerRoles`) no longer counts for these gates, because it outlives a
revoke until the token expires (#2381).
`TestTenantGates2428_RevokedGrantRefusedWhileTokenLive` pins this.

An API key's `owner_roles` label still counts (`admin` for tenant admin,
`grc_engineer` for approver), as `authz.IsTenantAdmin` already allowed
before. Dropping it would have narrowed access for existing keys, which is
outside this ticket.

### D3 — approver = admin or grc_engineer where OPA agrees; admin only elsewhere

Approver is admin or grc_engineer (by flag or live role), matching
`grc_engineer.rego`'s approve grant. Five resources are admin-only in OPA
(grc_engineer is not in their writable set): `oscal` (component-claim
disposition and SCF anchor), `board` (narrative generate/approve),
`questionnaires` (every write), `mcp` (write-proposal confirm/reject) and
`action-plans` (verify). Their gates are `IsTenantAdmin`, so the handler
equals OPA and nothing widens. Before #2428 these handlers admitted the
approver flag, which OPA derives as grc_engineer and denies, so the layers
already disagreed. Letting grc_engineer approve there is a separate rego
decision; the handlers' old refusal text ("grc_engineer role required")
shows that was the original intent.

Audit-period writes (create, freeze, attach a population) are admin-only in
OPA too, so `auditperiods.canWrite` is `IsTenantAdmin`. The control-state
read keeps admin or grc_engineer.

### D4 — #2216 D2 kept /admin super-admin only

`/v1/admin/*` handler gates and `/v1/me` `is_admin` stay on `cred.IsAdmin`.
OPA's `admin.rego` admits a tenant admin there, but #2216 D2 made `/admin`
super-admin only, and the handler checks are what hold that. This covers
adminusers, adminsso, adminscim, admingroupmappings, admincreds,
adminauditlog, adminvendors and adminauditperiods exports, admindemo,
adminframeworkversions, and `/v1/admin/policies/import`. Also unchanged:
`install_reset`, `install_state`, `tenants`, `admin/service`, `schemaregistry`
and `features`. Opening `/admin` to tenant admins would be its own ticket.

`/v1/me` gains `is_tenant_admin` and `is_approver`. The action-plan Verify
button keys off `is_tenant_admin` (D3: OPA admits admin only there).

### D5 — gates already reading tenant roles were left alone

`risks.hasTaxonomyAdmin`, `posturetargets.hasTargetAdmin` and
`mappingoverlays.hasOverlayWrite` already admitted admin or grc_engineer from
the JWT claim. Moving them to the live roles is the same #2381 hardening,
but it is not the bug filed here, so they are unchanged. The risks package's
private `isTenantAdmin` duplicate is folded into `authz.IsTenantAdmin`.

### D6 — pre-existing pairs where the handler is narrower than OPA

`TestTenantGates2428_HandlerAgreesWithOPA` asserts every switched route's
gate admits exactly the roles OPA admits, except where OPA admits a role
#2428 does not touch: auditor on the dashboard export and the audit-workspace
reads, and control_owner on walkthrough writes. They are pinned in
`handlerNarrower2428`. The handler is the narrower layer there, so nothing
widens. A handler admitting what OPA refuses fails the test.

## Workarounds removed

- `web/e2e/framework-scope-wizard.spec.ts` (#2419): the approver JWT is no
  longer minted with `super_admin: true`; the fixture's tenant `admin`
  user_roles row is what approves.
- `internal/api/auditor_workflow_2157_integration_test.go` (#2411): the
  population attach goes over HTTP as a tenant admin JWT, and auditor and
  viewer are refused, instead of a direct `UPDATE populations`.
