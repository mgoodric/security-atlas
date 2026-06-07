# 508 — SCIM 2.0 user-lifecycle provisioning (deprovisioning on IdP offboard)

**Cluster:** Auth
**Estimate:** L (4-5d)
**Type:** JUDGMENT (SCIM-attribute -> atlas-user/role mapping)
**Status:** `ready`

## Narrative

**WHY.** Today user lifecycle is partial. The platform authenticates humans via
an external IdP (OIDC RP) and mints atlas JWTs; slices 478/479 added super-admin
user<->tenant assignment + UI. But there is **no automated deprovisioning**: when
an employee is offboarded in the org's IdP (Okta / Entra / etc.), their
security-atlas access is not automatically revoked — the operator must manually
remove them. For a GRC platform whose whole value proposition is "diligence the
diligence tool," a stale account that survives an employee's departure is exactly
the access-review finding the platform is supposed to catch. Slice 374 (access
-review cadence, merged) makes review a _process_; SCIM makes deprovisioning
_automatic_, closing the window between offboard and revocation.

Slice 431 (external-IdP OIDC setup guide) **explicitly scoped SCIM out** ("Multi
-IdP, SCIM provisioning, and group-to-role mapping are out of scope; note them as
future work if they surface"). This is that future-work slice for SCIM. (Group
-to-role mapping is its sibling, slice 509.)

**WHAT this slice ships.**

1. **SCIM 2.0 service-provider endpoints** (RFC 7644): `/scim/v2/Users` (Create,
   Get, List/filter, Replace, Patch, Delete) and `/scim/v2/ServiceProviderConfig`
   - `/ResourceTypes` + `/Schemas` discovery. Minimum viable: the `User` resource
     with the core schema + the enterprise extension's `active` flag (the
     deprovision signal).
2. **SCIM bearer-token auth.** A per-tenant SCIM provisioning credential
   (separate from human JWTs and from push API keys), admin-issued, scoped to the
   SCIM endpoints only, revocable. Composes with the existing push-credential
   issuance pattern (open decision: push-credential UX — this reuses that shape).
3. **SCIM-attribute -> atlas-user mapping.** `userName` / `emails` map to the
   atlas user identity; `active=false` triggers deprovision (account disabled,
   sessions invalidated, tenant assignments revoked — not hard-deleted, so the
   audit trail of their prior actions survives invariant #2).
4. **Deprovision = disable, not delete.** A deprovisioned user is marked inactive
   and loses all access immediately, but their historical actor records remain
   (audit integrity). Re-provisioning (`active=true` again) re-enables.
5. **Append-only SCIM audit trail.** Every provision/deprovision writes an
   audit-log row (who/what/when via the SCIM credential identity).

**SCOPE DISCIPLINE — what's deliberately out.**

- **Group-to-role mapping** — slice 509. This slice provisions the _user_; 509
  maps IdP _groups_ to atlas roles. SCIM `Group` resource is 509's.
- **Multi-IdP federation** — slice 509 / future. SCIM here targets the one
  primary IdP per tenant (matching slice 431's documented one-primary-IdP model).
- **SCIM for the connector / machine identities** — SCIM is for human-user
  lifecycle only; connector credentials stay on the push-credential path.
- **Just-in-time (JIT) provisioning via OIDC claims** — a different mechanism
  (provision-on-first-login); SCIM is the push-from-IdP model. JIT is a possible
  future complement, not bundled.

## Threat model (STRIDE)

SCIM is an **inbound provisioning surface authenticated by a long-lived bearer
token that can create and delete users** — a high-value target.

**S — Spoofing.** A stolen SCIM token could let an attacker provision rogue
accounts or deprovision legitimate ones. **Mitigation:** the SCIM credential is
per-tenant, scoped to SCIM endpoints only (cannot call platform APIs), admin
-issued, and revocable; it is distinct from human JWTs and push keys (blast-radius
containment). Token presented over TLS only.

**T — Tampering.** A SCIM `Patch` could escalate a user (e.g. flip an admin
flag). **Mitigation:** SCIM maps only identity + `active` here; role assignment is
NOT SCIM-controllable in this slice (roles come from slice 509's group mapping or
manual admin assignment) — so a SCIM token cannot grant itself or others
elevated roles. The attribute allow-list is enforced server-side.

**R — Repudiation.** Provision/deprovision must be accountable. **Mitigation:**
every SCIM mutation writes an append-only audit-log row carrying the SCIM
credential identity, the target user, and the operation.

**I — Information disclosure.** SCIM List/filter could enumerate the tenant's
users. **Mitigation:** RLS confines every SCIM query to the credential's tenant;
the SCIM token cannot read across tenants; error responses do not distinguish
"user in another tenant" from "not found."

**D — Denial of service.** A compromised token could mass-deprovision.
**Mitigation:** deprovision is reversible (disable, not delete) so a malicious
mass-deprovision is recoverable; rate limiting on the SCIM endpoints; the audit
trail makes a malicious sweep immediately visible.

**E — Elevation of privilege.** The SCIM credential must not be a backdoor to
platform access. **Mitigation:** scoped to SCIM endpoints only; cannot mint a
human session; cannot assign roles (this slice); revocable.

## Acceptance criteria

- [ ] **AC-1.** `/scim/v2/Users` supports Create / Get / List(filter) / Replace /
      Patch / Delete per RFC 7644 with the core `User` schema + `active` flag.
- [ ] **AC-2.** Discovery endpoints (`ServiceProviderConfig`, `ResourceTypes`,
      `Schemas`) return spec-conformant documents (integration test validates a
      real IdP's discovery probe shape — Okta/Entra).
- [ ] **AC-3.** SCIM auth uses a per-tenant, SCIM-scoped, admin-issued, revocable
      bearer credential distinct from human JWTs and push keys.
- [ ] **AC-4.** `active=false` deprovisions: account disabled, sessions
      invalidated, tenant assignments revoked — NOT hard-deleted; historical actor
      records survive (integration test asserts prior audit rows remain).
- [ ] **AC-5.** Every SCIM mutation writes an append-only audit-log row.
- [ ] **AC-6.** SCIM queries are RLS-confined to the credential's tenant
      (two-tenant integration test asserts no cross-tenant enumeration).
- [ ] **AC-7.** A SCIM token cannot assign or escalate roles (role assignment is
      out of SCIM's attribute allow-list in this slice).

## Anti-criteria (P0 — block merge)

- **P0-508-1.** Does NOT hard-delete a deprovisioned user (audit integrity,
  invariant #2).
- **P0-508-2.** Does NOT let the SCIM credential call platform APIs or mint a
  human session (scope containment).
- **P0-508-3.** Does NOT let SCIM assign/escalate roles (defer to slice 509).
- **P0-508-4.** Does NOT read or mutate across tenants (RLS-confined).

## Dependencies

- **#478 / #479** (super-admin user<->tenant assignment + UI) — `merged`. The
  user/tenant model SCIM provisions into.
- **#187-#192** (auth-substrate-v2: AS / JWT / sessions) — `merged`. Session
  invalidation on deprovision plugs into this.
- **#374** (access-review cadence) — `merged`. SCIM is the automation that makes
  the manual review's findings rarer.
- **Push-credential issuance UX** (open decision) — SCIM credential reuses that
  scoped-revocable-key shape; coordinate so the two credential types share UX.

## Canvas references

- `Plans/canvas/09-tech-stack.md` (OIDC RP + RBAC/ABAC via OPA; auth model)
- `docs/issues/431-external-idp-oidc-setup-guide.md` (scoped SCIM out as future
  work — this slice picks it up)

## Constitutional invariants honored

- **#6** RLS tenant isolation — SCIM queries are tenant-confined.
- **#2** append-only ledger — deprovision disables, never deletes, so the actor's
  historical records survive.
- **AI-assist boundary** — N/A (no AI surface).
