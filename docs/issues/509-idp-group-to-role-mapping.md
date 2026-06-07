# 509 — IdP group-to-role mapping (claims/SCIM-group -> atlas role assignment)

**Cluster:** Auth
**Estimate:** M (2-3d)
**Type:** JUDGMENT (group->role mapping precedence + conflict resolution)
**Status:** `ready`

## Narrative

**WHY.** Role assignment today is **manual**: an admin assigns each user their
atlas role(s) per tenant. Organizations that run identity centrally express
authorization through IdP **groups** ("SecurityTeam", "Auditors", "Engineering")
and expect downstream apps to derive roles from group membership — so that a
single IdP group change reprovisions access everywhere. Without group-to-role
mapping, every role change in security-atlas is a manual step the org's IdP
already encoded, which both adds toil and drifts from the IdP source of truth
(an access-review finding waiting to happen — exactly what slice 374 polices).

Slice 431 (external-IdP setup guide) **explicitly scoped group-to-role mapping
out** as future work. This is that slice. It is the authorization-derivation
sibling of slice 508 (SCIM user lifecycle) — 508 provisions the _user_, 509 maps
their _groups_ to atlas _roles_.

**WHAT this slice ships.**

1. **Group-mapping configuration** (`oidc_idp_group_mappings`, per tenant): an
   admin-managed table mapping an IdP group (by group id or name, per the IdP) to
   one or more atlas roles, scoped to a tenant.
2. **Two derivation sources, one resolver.**
   - **OIDC claim path:** roles derived from a `groups` claim in the OIDC token
     at login (when the IdP emits group claims).
   - **SCIM Group path:** the SCIM `/scim/v2/Groups` resource (slice 508's
     sibling endpoint) maintains group membership push-from-IdP; roles re-derive
     when membership changes.
     Both feed one **mapping resolver** so OIDC-claim-derived and SCIM-derived
     roles cannot disagree on the mapping logic (single source of truth).
3. **Precedence + conflict resolution.** Explicit, documented rules: manual
   admin-assigned roles vs. group-derived roles (group-derived is the default
   source; manual assignment is an explicit override flag so it survives a group
   re-derivation); a user in multiple mapped groups gets the **union** of mapped
   roles; an unmapped group contributes nothing (fail-closed — no implicit role).
4. **Multi-IdP per tenant (the federation half slice 431 deferred).** The
   mapping table is keyed by `(tenant, idp_config_id, group)` so a tenant with
   more than one IdP config maps each IdP's groups independently. (This lifts
   slice 431's one-primary-IdP limitation for the role-derivation path.)
5. **Append-only role-change audit trail.** Every group-derived role grant/revoke
   writes an audit-log row capturing the triggering group + source (OIDC vs SCIM).

**SCOPE DISCIPLINE — what's deliberately out.**

- **User provisioning** — slice 508 (SCIM `User`). This slice consumes the
  provisioned user; it maps groups to roles.
- **Fine-grained ABAC attribute mapping.** Roles are the coarse RBAC layer
  (canvas: RBAC coarse + ABAC fine via OPA); this slice maps groups to RBAC roles
  only. ABAC attribute derivation from IdP claims is a separate, larger design.
- **A group-management UI inside atlas.** Groups are owned by the IdP; atlas reads
  them. This slice ships the _mapping_ admin UI (group -> role), not a group editor.
- **Auto-creating roles from group names.** Roles are a fixed atlas vocabulary;
  an unmapped group fails closed (no implicit role), never auto-creates a role.

## Threat model (STRIDE)

Group-to-role mapping is an **authorization-derivation** surface — a mapping bug
or a forged group claim grants real privilege. This is a fail-closed-critical
surface.

**S — Spoofing.** A forged `groups` claim in an OIDC token could grant an
attacker a privileged role. **Mitigation:** group claims are trusted only from a
validated OIDC token (signature + issuer + audience + nonce already enforced by
the auth substrate, slices 187-192 + 365); the SCIM-group path is authenticated
by the scoped SCIM credential (slice 508). No unauthenticated group input is
accepted.

**T — Tampering.** Editing the mapping table is a privilege-granting action.
**Mitigation:** mapping CRUD is admin-only; every edit writes an append-only
audit-log row (before/after of the group->role mapping); a mapping change that
would grant admin via a group is loggable and reviewable.

**R — Repudiation.** "Why does this user have this role?" must be answerable.
**Mitigation:** every group-derived role grant records the triggering group +
source in the audit log; the derived-vs-manual distinction is explicit on the
user's role record.

**I — Information disclosure.** Group claims may reveal org structure.
**Mitigation:** group mappings are tenant-scoped (RLS); a user's derived roles are
visible only to admins of their tenant.

**D — Denial of service.** A mass group-membership change (e.g. SCIM removes a
whole group) could mass-revoke roles. **Mitigation:** revocation is reversible
(re-add the group membership re-derives the role); the audit trail makes a
malicious sweep visible; manual-override roles survive group re-derivation so a
group change cannot strand the last admin (combined with a last-admin guard).

**E — Elevation of privilege (PRIMARY).** The whole surface is privilege
derivation. **Mitigation:** unmapped groups contribute **nothing** (fail-closed —
no implicit privilege); the resolver takes the union of _mapped_ roles only; a
last-admin guard prevents a group re-derivation from removing the final admin
(so the tenant can never be locked out).

## Acceptance criteria

- [ ] **AC-1.** `oidc_idp_group_mappings` migration is idempotent + reversible;
      keyed by `(tenant, idp_config_id, group)`; RLS tenant-scoped.
- [ ] **AC-2.** OIDC-claim-derived and SCIM-Group-derived roles flow through one
      mapping resolver (integration test asserts identical mapping logic for both
      sources).
- [ ] **AC-3.** An unmapped group contributes no role (fail-closed); a user in
      multiple mapped groups gets the union (integration test).
- [ ] **AC-4.** Manual admin-assigned roles survive a group re-derivation via an
      explicit override flag (integration test).
- [ ] **AC-5.** A last-admin guard prevents group re-derivation from removing the
      final tenant admin (integration test).
- [ ] **AC-6.** Multi-IdP: a tenant with two IdP configs maps each IdP's groups
      independently (integration test against two configs).
- [ ] **AC-7.** Every group-derived role change writes an append-only audit-log
      row capturing triggering group + source (OIDC vs SCIM).
- [ ] **AC-8.** Mapping CRUD is admin-only (403 below admin).

## Anti-criteria (P0 — block merge)

- **P0-509-1.** Does NOT grant any role from an unmapped group (fail-closed).
- **P0-509-2.** Does NOT trust group input from an unvalidated token.
- **P0-509-3.** Does NOT allow group re-derivation to remove the last admin.
- **P0-509-4.** Does NOT auto-create atlas roles from group names.

## Dependencies

- **#508** (SCIM user-lifecycle provisioning) — sibling; provides the SCIM `User`
  - `Group` substrate the SCIM-group path consumes. Whichever lands first, the
    SCIM `Group` resource is 509's to add if 508 ships User-only.
- **#187-#192** (auth-substrate-v2) + **#365** (OIDC nonce) — `merged`. The
  validated-token guarantee the OIDC-claim path relies on.
- **#478 / #479** (user<->tenant assignment) — `merged`. The role model this maps
  into.
- **#431** (external-IdP setup guide) — `ready`/doc; scoped group-to-role + multi
  -IdP out as future work (this slice picks them up).

## Canvas references

- `Plans/canvas/09-tech-stack.md` (RBAC coarse + ABAC fine via OPA; OIDC RP)
- `docs/issues/431-external-idp-oidc-setup-guide.md` (deferred group->role +
  multi-IdP)

## Constitutional invariants honored

- **#6** RLS tenant isolation — mappings + derived roles are tenant-scoped.
- **Fail-closed authz** — unmapped groups grant nothing; last-admin guard holds.
- **AI-assist boundary** — N/A (no AI surface).
