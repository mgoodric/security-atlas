# 476 — Make seeded demo data reachable by the operator who loads it

**Cluster:** Frontend / Backend / Multi-tenancy
**Estimate:** M (1–2d)
**Type:** JUDGMENT (access-grant shape + super-admin switch UX)

**Status:** `ready`

> Filed 2026-06-06 from live maintainer use: after the demo seed succeeded on
> the self-host edge box, the operator had **no way to reach the demo data**.
> Parent context: the demo-seed support thread (companion to PR #1028's 409
> messaging fix).

## Narrative

**Why (the confirmed gap).** "Load demo data" (`/v1/admin/demo/seed`, slice
205/278) creates a **separate `demo` tenant** and populates it (50 controls /
20 risks / 200 evidence — verified live). But the operator who clicked Seed
**cannot navigate to it**:

- The tenant switcher (`web/components/auth/tenant-switcher.tsx:262`) renders
  nothing when you have ≤1 tenant, and its list comes from the JWT's
  `atlas:available_tenants[]`.
- `available_tenants` is the operator's **memberships**, enumerated by
  `(idp_issuer, idp_subject)` (`internal/api/oauth/user_resolver.go`), and the
  switcher/picker are deliberately bounded to it (P0-192-5: never show a tenant
  you're not a member of — _even for a super-admin_).
- The demo seed does **not** add the seeding operator to the demo tenant, so
  `available_tenants` stays `[home]` → switcher hidden → demo data unreachable.
- **Local-auth operators (self-host default) are the worst case:** their
  identity has empty `idp_issuer/idp_subject` and is effectively single-tenant
  by construction, so there is no clean membership path at all today.

So a core onboarding action produces data the operator can't see — and there is
no super-admin "switch to any tenant" affordance to escape it (the picker is
membership-bounded by design).

**What (the deliverable).** Make seeded demo data reachable by the operator,
**without** weakening the P0-192-5 membership-bounded picker for normal users.
Resolve the JUDGMENT call between (and the slice picks/justifies one, possibly
both):

1. **Seed grants access** — the demo seed adds the seeding operator's identity
   as a member (with an appropriate role) of the `demo` tenant, so it appears
   in their switcher after re-auth. Must handle the local-auth (empty-IdP /
   single-tenant) case explicitly — this is the load-bearing hard part.
2. **Super-admin switch-to-any-tenant** — a super-admin-only affordance (token
   exchange, RFC 8693) to switch into any tenant the super-admin flag already
   authorizes, surfaced in the UI (e.g. from the admin/tenants management page),
   _separate from_ the membership-bounded switcher.

**Scope discipline.** v0 = make the demo tenant reachable by the operator who
seeded it on a self-host (local-auth) deployment, end-to-end (seed → switch →
see data). Does NOT build a general cross-tenant user-management UI (that's the
separate slice-060.5 gap — see Dependencies), does NOT weaken the
membership-bounded picker for non-super-admins, does NOT auto-switch the
operator (they choose).

## Threat model

STRIDE — verdict **has-mitigations** (this touches tenant access — the highest-
care surface).

- **E — Elevation of privilege (core).** Granting access / a switch affordance
  must NOT let a non-super-admin reach a tenant they aren't entitled to. Option
  2 is gated strictly on the existing `atlas:super_admin` claim + token-exchange
  rules; Option 1 grants the _seeding operator_ (already an admin/super-admin)
  access to the _demo_ tenant only. The P0-192-5 membership bound for normal
  users is preserved.
- **I — Information disclosure.** No cross-tenant data bleed — switching is an
  explicit, audited tenant context change; RLS still scopes every read to the
  active tenant.
- **R — Repudiation.** The access grant / super-admin switch is audit-logged
  (who gained access to / switched into the demo tenant).
- **S/T/D.** N/A beyond existing auth controls.

## Acceptance criteria

- [ ] **AC-1.** After seeding demo data, the operator who seeded it can reach
      the demo tenant from the UI (switcher shows it, or a super-admin switch
      affordance does) — proven end-to-end (seed → switch → demo dashboard shows
      the 50/20/200 dataset).
- [ ] **AC-2.** Works for a **local-auth** operator (empty-IdP identity) — the
      load-bearing case; the chosen mechanism explicitly handles it.
- [ ] **AC-3.** The P0-192-5 membership-bounded picker is UNCHANGED for
      non-super-admin users (no tenant they aren't entitled to appears).
- [ ] **AC-4.** The access grant / switch is audit-logged.
- [ ] **AC-5.** Switching is explicit (operator action), never automatic.
- [ ] **AC-6.** If Option 1 (seed-grants-access) is chosen, re-seed/teardown
      interplay is sound (teardown removes the grant; re-seed is idempotent).
- [ ] **AC-7.** A test proves AC-1/AC-2 (integration or e2e) + AC-3 (a normal
      user still can't see the demo tenant).
- [ ] **AC-8.** Decisions log records the Option-1-vs-2 choice + the local-auth
      handling + confidence.

## Constitutional invariants honored

- **#6 RLS tenant isolation** — switching changes the active tenant context;
  every read stays RLS-scoped; no cross-tenant bleed.
- **Tenancy is multidimensional / membership-bounded** — preserves the
  deliberate P0-192-5 bound for normal users; only super-admin / the seeding
  operator gains the demo reach.

## Canvas references

- `Plans/canvas/05-scopes.md` — tenancy + scope.
- `Plans/canvas/01-vision.md` — the solo-operator persona (who loads demo data
  to evaluate the tool — the exact journey this unblocks).

## Dependencies

- Relates to the **slice-060.5 gap** (no `/v1/admin/users` user/role
  management UI ever shipped — `web/app/admin/users/page.tsx` is a scaffold).
  A general cross-tenant user-management UI is a separate, larger slice; this
  slice solves the narrower demo-reachability journey. No unmerged technical
  dep → `ready`.
- Companion to PR #1028 (the 409 "already loaded" messaging fix) — same demo
  support thread.

## Anti-criteria (P0 — block merge)

- **P0-476-1.** Does NOT let a non-super-admin reach a tenant they aren't
  entitled to (P0-192-5 preserved for normal users).
- **P0-476-2.** Does NOT auto-switch the operator's tenant.
- **P0-476-3.** Does NOT build the general cross-tenant user-management UI
  (slice-060.5 scope) — stay on the demo-reachability journey.
- **P0-476-4.** Does NOT bypass RLS or token-exchange rules for the switch.

## Skill mix (3-5)

- `grill-with-docs` — align on the membership model + the local-auth identity
  reality (empty-IdP / single-tenant)
- `database-designer` — the access-grant rows (member + role) under RLS, or the
  super-admin switch path
- `tdd` — AC-2 (local-auth reach) + AC-3 (normal user still bounded)
- `security-review` — the elevation surface (the core risk)
- `ship-gate` — verify P0-192-5 is preserved for non-super-admins

## Notes for the implementing agent

- Confirmed live: `admin@example.com` is a super_admin with home = bootstrap
  tenant, empty `idp_issuer/idp_subject`; the `demo` tenant
  (`ad0e6b3c-…`) has its own fictional users and zero members matching the
  operator's identity. `users` has no role column — roles live in `user_roles`;
  identity = `(idp_issuer, idp_subject)`; local creds are stored separately
  (no `password_hash` on `users`).
- The empty-IdP local identity is why a naive "insert a users row in demo"
  hand-grant is unsafe (it could over-match every empty-IdP user) — the slice
  must define the local-auth membership semantics deliberately.
- **Registration note (slice-382):** `_STATUS.md` row registered by the
  orchestrator on a chore/status branch, not this `docs/476` branch.
