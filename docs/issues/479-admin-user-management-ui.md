# 479 — Admin user-management UI: assign users to tenants + roles (incl. self)

**Cluster:** Frontend / Multi-tenancy
**Estimate:** L (3d)
**Type:** JUDGMENT (UX of the assignment flow + self-assign affordance)

**Status:** `not-ready` (depends on slice 478 — the assignment API)

> Filed 2026-06-06 at maintainer request (companion to slice 478, the backend).
> Wires the slice-060 `/admin/users` scaffold to the real API and gives the
> super-admin a UI to assign users (including themselves) to tenants.

## Narrative

**Why.** `web/app/admin/users/page.tsx` is a slice-060 scaffold: it renders the
role-permission matrix + a placeholder user table, with a documented gap ("no
`/v1/admin/users` HTTP endpoint… ships in slice 060.5"). Slice 478 ships that
backend; this slice makes it usable: a super-admin needs to see who's in which
tenant and assign users (and themselves) to tenants with roles — the supported,
auditable replacement for the hand-DB-surgery that's otherwise required.

**What.** Build the user-management UI on slice 478's API:

- A **user list** (super-admin: across tenants; tenant-admin: their tenant) with
  each user's tenant + roles, paginated.
- An **assign-to-tenant** action: pick a user, a tenant, role(s) → calls
  `POST /v1/admin/users/assign`; shows success/error inline.
- A **revoke** action per membership/role.
- A first-class **"Add me to this tenant"** affordance for super-admins (the
  self-assign path) — e.g. from the user's own row or the admin/tenants page —
  so the super-admin can grant themselves access to a tenant (the demo tenant,
  or any) and then switch into it. After self-assign, prompt to re-auth (so the
  new `available_tenants` takes effect) and surface the now-visible switcher.

**Scope discipline.** UI over slice 478's API only. shadcn/ui + Tailwind +
TanStack Query, matching existing admin pages. Vitest for BFF/lib logic
(node-only per slice 069), Playwright e2e for the flow. Does NOT add backend
behavior (478 owns it), does NOT build invitation/email/SCIM UI, does NOT weaken
the membership-bounded switcher for normal users.

## Threat model

STRIDE — verdict **has-mitigations** (the real authz enforcement is server-side
in 478; the UI must not imply more authority than the server grants).

- **E — Elevation of privilege.** The UI exposes assign/revoke actions, but the
  server (478) is the enforcement point — the UI must surface 403s honestly
  (slice-225 label-honesty; no dead/over-promising buttons for actions the
  caller isn't authorized for) and must not present cross-tenant controls to a
  tenant-admin.
- **I — Information disclosure.** The cross-tenant user list is shown only when
  the API returns it (super-admin); a tenant-admin sees only their tenant.
- **R — Repudiation.** Surfaces that actions are audit-logged (server-side).
- **S/T/D.** N/A beyond the API's own controls.

## Acceptance criteria

- [ ] **AC-1.** The `/admin/users` page lists users with tenant + roles from
      `GET /v1/admin/users` (super-admin cross-tenant; tenant-admin scoped),
      paginated; the slice-060 role matrix is retained or linked.
- [ ] **AC-2.** Assign-to-tenant flow (user + tenant + role[s]) calls the API;
      success + error states render inline; the list refreshes (TanStack Query
      invalidation).
- [ ] **AC-3.** Revoke flow per membership/role with a confirm step.
- [ ] **AC-4.** **"Add me to this tenant"** self-assign affordance for
      super-admins; on success, the UI explains a re-auth is needed for the new
      tenant to appear in the switcher (and links/triggers it).
- [ ] **AC-5.** Authz-honest UI: a tenant-admin sees no cross-tenant controls; a
      403 from the API renders a clear message, not a silent failure or a dead
      button (UI-honesty harness passes).
- [ ] **AC-6.** Accessible (labels, keyboard, ARIA) per the slice-331 a11y
      lineage; the assign dialog + role selects are labelled.
- [ ] **AC-7.** Vitest for any new BFF route handler / lib logic; Playwright e2e
      for the assign + self-assign flow (preconditions established by the
      docker-compose seed per web/e2e/README).

## Constitutional invariants honored

- **#6 RLS / membership-bounded** — UI reflects the server's RLS-scoped data;
  preserves P0-192-5 (normal users' switcher unchanged).
- **AI-assist boundary** — N/A (no AI surface).

## Canvas references

- `Plans/canvas/05-scopes.md` — tenancy. The slice-060 role matrix
  (`web/components/admin/roles.tsx`) is the role reference.

## Dependencies

- **#478 (the assignment API) — REQUIRED, unmerged → this slice is `not-ready`
  until 478 lands.**
- Builds on the slice-060 scaffold (`web/app/admin/users/page.tsx`).
- Companion to slice 476 (demo reachability) — the self-assign affordance is the
  UI path that makes the demo tenant reachable; 476 may close once 478+479 land.

## Anti-criteria (P0 — block merge)

- **P0-479-1.** Does NOT enforce authz only in the UI — the server (478) is the
  gate; the UI surfaces its decisions honestly.
- **P0-479-2.** Does NOT show cross-tenant controls to a tenant-admin.
- **P0-479-3.** Does NOT auto-switch the actor's tenant after self-assign
  (explicit re-auth/switch).
- **P0-479-4.** Does NOT ship invitation/email/SCIM UI (out of scope).
- **P0-479-5.** Does NOT weaken the membership-bounded switcher for normal users.

## Skill mix (3-5)

- `grill-with-docs` — align the UI with 478's API contract + the role matrix
- `tdd` — vitest BFF/lib + Playwright assign/self-assign flow
- `security-review` — authz-honest UI (no over-promising controls)
- `simplify` — reuse existing admin-page patterns, don't reinvent
- `ship-gate` — UI-honesty harness + a11y

## Notes for the implementing agent

- Build AFTER 478 merges; consume its real API contract (don't hand-mock a
  speculative shape).
- The self-assign affordance (AC-4) is the user-facing payoff: it's the
  supported replacement for hand-DB-surgery to reach a tenant (e.g. the seeded
  demo tenant). Make the re-auth requirement explicit (the new
  `available_tenants` only takes effect on a fresh token).
- **Registration note (slice-382):** `_STATUS.md` row registered by the
  orchestrator on a chore/status branch, not this branch.
