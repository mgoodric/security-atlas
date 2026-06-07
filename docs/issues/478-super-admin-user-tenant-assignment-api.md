# 478 — Super-admin user↔tenant↔role assignment API (incl. self-assignment)

**Cluster:** Backend / Multi-tenancy / Auth
**Estimate:** L (3d)
**Type:** JUDGMENT (membership-write shape + local-auth identity semantics + authz)

**Status:** `ready`

> Filed 2026-06-06 at maintainer request: "user management slices that let the
> super-admin assign users to tenants, including the super-admin themselves."
> Revives the deferred slice-060.5 backend gap (the `/admin/users` page is a
> scaffold with no API). The backend half; the UI is slice 479. Subsumes the
> mechanism slice 476 needed (demo-data reachability falls out of self-assign).

## Narrative

**Why.** There is no way to manage who belongs to which tenant. A user's tenant
membership is a `users` row keyed by `(idp_issuer, idp_subject)` per tenant
(`internal/api/oauth/user_resolver.go` `enumerateMemberships`), and a user's
capabilities come from `user_roles` (the slice-035 enum: `admin`,
`grc_engineer`, `control_owner`, `auditor`, `viewer`). But there is **no admin
API to create those rows** — `/admin/users` (slice 060) is an explicit scaffold;
the `/v1/admin/users` read+write surface (slice 060.5) never shipped. Concrete
consequences hit live: a super-admin can't add a user to a second tenant, can't
assign roles, and — the immediate trigger — **can't add themselves to a tenant
they need to reach** (e.g. the seeded `demo` tenant, or any tenant they
super-administer but aren't a member of, so the membership-bounded tenant
switcher [P0-192-5] never surfaces it).

**What.** A super-admin-gated REST surface to read and write user↔tenant↔role
assignments:

- **List** users (super-admin: across tenants; tenant-admin: within their
  tenant) with their tenant + roles.
- **Assign** a user identity to a tenant with one or more roles (creates the
  per-tenant `users` membership row + `user_roles` rows). Idempotent.
- **Revoke** a user's membership/role in a tenant.
- **Self-assign**: a super-admin assigns _themselves_ to a tenant (the same
  assign path; explicitly supported + tested) → the tenant appears in their
  `available_tenants` on next token issuance → reachable in the switcher.

The hard part (the JUDGMENT call): **local-auth identity semantics.** Local
operators have empty `(idp_issuer, idp_subject)` and are single-tenant by
construction today. The slice must define how a local user becomes a member of a
second tenant (e.g. a stable synthetic local-identity key per user, or a
membership table decoupled from the IdP tuple) WITHOUT letting the empty-tuple
over-match (the demo-reachability hazard from 476). This is the load-bearing
design decision.

**Scope discipline.** Backend API + the membership/role write + the
`available_tenants` refresh path, super-admin gated (tenant-admin gets the
within-tenant subset). Does NOT build the UI (slice 479). Does NOT build user
invitation/email flows, SCIM, or password reset. Does NOT change the OPA role
semantics (reuses the slice-035 enum + gate). Does NOT weaken P0-192-5 for
non-super-admins.

## Threat model

STRIDE — verdict **HOLD-pending-review-worthy** (this is a tenant-access
elevation surface — the highest-care area; the ACs below are the mitigations).

- **E — Elevation of privilege (THE core).** Assigning a user to a tenant /
  granting a role is privilege grant. _Mitigations/ACs:_ every write is gated on
  `atlas:super_admin` (cross-tenant) or tenant-`admin` (within-tenant only,
  never cross-tenant); the OPA gate is enforced server-side, not just UI;
  assigning a role you don't have / to a tenant you don't administer is denied;
  no privilege escalation beyond the actor's own authority. Self-assignment is
  bounded to what the super-admin flag already authorizes (no new global power —
  it makes existing global authority navigable).
- **T — Tampering / integrity.** Membership + role writes must be RLS-correct
  (the right tenant_id) and not corrupt the identity model — esp. the local-auth
  empty-tuple hazard (must NOT create a row that makes every empty-tuple user a
  member). Atomic (membership + roles in one tx).
- **R — Repudiation.** Every assign/revoke/self-assign is audit-logged (actor,
  target user, tenant, role, before/after) — the slice-142 super_admin_audit_log
  - me_audit_log pattern.
- **I — Information disclosure.** The cross-tenant user list is super-admin only;
  a tenant-admin sees only their tenant's users; RLS enforced on every read.
- **S — Spoofing.** New authenticated endpoints; no unauth surface; reuse the
  JWT auth + OPA.
- **D — Denial of service.** List endpoints are paginated/bounded (no full-table
  scan); assign is a bounded write.

## Acceptance criteria

- [ ] **AC-1.** `GET /v1/admin/users` — super-admin: paginated list across
      tenants with each user's tenant + roles; tenant-admin: scoped to their
      tenant. Bounded (no unbounded scan).
- [ ] **AC-2.** `POST /v1/admin/users/assign` (or equivalent) — assign a user
      identity to a tenant with role(s); creates the per-tenant membership +
      `user_roles` rows atomically; idempotent on re-assign.
- [ ] **AC-3.** Revoke — remove a user's membership/role in a tenant.
- [ ] **AC-4.** **Self-assign**: a super-admin assigns themselves to a tenant;
      after re-auth the tenant is in their `available_tenants` and reachable in
      the switcher. Proven end-to-end (assign self → demo tenant → switch → see
      the demo dataset).
- [ ] **AC-5.** **Local-auth identity** is handled deliberately: a local
      (empty-IdP) operator can be assigned to a second tenant without the
      empty-tuple over-matching every local user. Decisions log records the
      chosen mechanism.
- [ ] **AC-6.** Authz: cross-tenant writes require `super_admin`; within-tenant
      writes require tenant-`admin`; an actor cannot grant a role / reach a
      tenant beyond their authority (OPA-enforced, tested with a denied case).
- [ ] **AC-7.** Every assign/revoke/self-assign writes an audit row.
- [ ] **AC-8.** RLS-correct: integration test proving a tenant-admin cannot list
      or assign outside their tenant, and writes land under the right tenant_id.
- [ ] **AC-9.** P0-192-5 preserved: a non-super-admin's switcher still shows only
      their memberships.
- [ ] **AC-10.** Unit + integration tests for the assignment logic, the
      local-auth path (AC-5), the authz denials (AC-6), and self-assign (AC-4).

## Constitutional invariants honored

- **#6 RLS tenant isolation** — every read/write RLS-scoped; cross-tenant only
  for super-admin via the explicit gate.
- **RBAC+ABAC via OPA** — reuses the slice-035 role enum + the OPA gate; the API
  is a write surface over the existing model, server-enforced.
- **Membership-bounded navigation (P0-192-5)** — preserved for normal users; the
  switcher stays membership-bounded (this slice just provides the supported way
  to _create_ memberships).

## Canvas references

- `Plans/canvas/05-scopes.md` — tenancy. `Plans/canvas/09-tech-stack.md` —
  AuthN/AuthZ (OIDC RP + RBAC/ABAC via OPA + the OAuth AS).

## Dependencies

- Revives **slice 060.5** (the deferred `/v1/admin/users` backend; `/admin/users`
  scaffold is slice 060). Reuses slice 035 (role enum + OPA gate), slice 142/143
  (super_admin + tenant-create), slice 192 (DBUserResolver / available_tenants).
- **Subsumes the mechanism slice 476 needed** (demo-data reachability): once a
  super-admin can self-assign to any tenant, the demo tenant is reachable — so
  476 reduces to "verify demo reachability via self-assign + (optional) the seed
  hinting the operator to self-assign." Note this in the 476 row at reconcile.
- Blocks slice 479 (the UI). No unmerged technical dep → `ready`.

## Anti-criteria (P0 — block merge)

- **P0-478-1.** Does NOT let any actor grant access/roles beyond their own
  authority (super-admin cross-tenant; tenant-admin within-tenant only).
- **P0-478-2.** Does NOT create a local-auth membership row that over-matches
  the empty `(idp_issuer, idp_subject)` tuple (the 476 hazard).
- **P0-478-3.** Does NOT weaken P0-192-5 for non-super-admin switcher/picker.
- **P0-478-4.** Does NOT bypass the OPA gate / enforce authz only in the UI.
- **P0-478-5.** Does NOT ship invitation/email/SCIM/password flows (out of
  scope) and does NOT auto-switch the actor's tenant.
- **P0-478-6.** Does NOT use vendor-prefixed test fixture tokens.

## Skill mix (3-5)

- `grill-with-docs` — nail the identity/membership model (esp. local-auth) +
  the role/OPA reuse
- `database-designer` — membership + `user_roles` writes under four-policy RLS;
  the local-auth identity key
- `tdd` — AC-4 self-assign, AC-5 local-auth, AC-6 authz denials, AC-8 RLS
- `security-review` — the elevation surface (the dominant risk)
- `ship-gate` — verify the OPA gate is non-bypassable + P0-192-5 intact

## Notes for the implementing agent

- Confirmed live: `users` columns = id, tenant_id, email, display_name, status,
  idp_issuer, idp_subject, created_at, updated_at, time_zone, demo_only (NO role
  column — roles in `user_roles`; NO password_hash — local creds stored
  separately). `available_tenants` = `SELECT DISTINCT tenant_id FROM users WHERE
idp_issuer=$1 AND idp_subject=$2 AND status='active'` (user_resolver.go).
- The empty-IdP local identity is the crux of AC-5/P0-478-2 — resolve it in the
  grill before writing the assign path. A synthetic stable per-user local key
  (vs the empty tuple) is one candidate; a dedicated memberships table is
  another. Record the decision + why.
- **Registration note (slice-382):** `_STATUS.md` row registered by the
  orchestrator on a chore/status branch, not this branch.
