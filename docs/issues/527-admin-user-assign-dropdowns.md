# 527 — Admin user-assign dialog: user + tenant dropdowns (replace raw UUID inputs)

**Cluster:** Frontend
**Estimate:** S (0.5–1d)
**Type:** JUDGMENT (dropdown data-source + tenant-admin pinning UX)

**Status:** `ready`

> Filed 2026-06-07 at maintainer UX request on the just-shipped slice 479: "For
> the assign-user-to-tenant pop-up, would be nice to have a drop down for user
> and tenant." Enhancement to slice 479; web-only; consumes existing APIs.

## Narrative

**Why.** Slice 479 shipped the admin user-management UI, but its
"assign-to-tenant" dialog (`web/app/admin/users/page.tsx`) takes the **user id**
and **tenant id** as raw UUID text `Input`s (validated against a `UUID_PATTERN`).
That is correct but operator-hostile: a super-admin must copy/paste two UUIDs to
assign anyone, with no way to see who/which-tenant they're choosing and no guard
against a typo'd-but-well-formed UUID pointing at the wrong entity. The maintainer
asked for dropdowns.

**What.** Replace the two UUID text inputs in the assign dialog with **dropdowns**:

1. **User dropdown** — populated from the user list the page already loads
   (`GET /api/admin/users` → BFF → `GET /v1/admin/users`); each option shows a
   human label (display name + email) with the user id as the value. No new fetch
   is required — reuse the loaded list.
2. **Tenant dropdown** — populated from `GET /v1/admin/tenants` (the slice-142/143
   admin-tenants surface, already mounted at `internal/api/httpserver.go:975`,
   `adminBearer` tier) via a new BFF route (or the existing admin BFF pattern);
   each option shows the tenant name with the tenant id as the value.

**Tenant-admin pinning (authz-honest).** A tenant-admin's session is
within-tenant only — they must NOT see a cross-tenant tenant dropdown
(P0-479-2). For a tenant-admin the tenant field is **pinned** to their own
session tenant (shown read-only / pre-selected, not a chooser); only a
super-admin gets the populated tenant dropdown. The user dropdown for a
tenant-admin lists only their tenant's users (the within-tenant list shape the
page already receives).

**Scope discipline.** UI-only enhancement to the 479 assign dialog. Reuses the
existing 478 assign/revoke API + the existing `GET /v1/admin/tenants` list — NO
backend behavior change, NO new endpoint (only a thin BFF passthrough for the
tenants list if one does not already exist). Keeps the self-assign affordance,
the role checkbox group, the confirm-on-revoke, and all slice-479 authz-honesty
behavior unchanged. Does NOT add a typeahead/search-combobox (a plain select is
the v0; a searchable combobox for very large user/tenant lists is a follow-on if
the operator hits a scale limit). Does NOT change the revoke flow.

## Threat model

STRIDE — verdict **has-mitigations** (a UI convenience over data the actor is
already authorized to read; the server (478) stays the sole authority).

- **E — Elevation of privilege (the core).** The tenant dropdown must not become
  a way for a tenant-admin to discover or target tenants they don't administer.
  _Mitigation/AC:_ the populated cross-tenant tenant dropdown renders ONLY for a
  super-admin (gated on the same response-shape signal slice 479 uses —
  `cross_tenant`); a tenant-admin's tenant is pinned to their session tenant and
  is not a chooser (P0-479-2 preserved). The server (478) re-checks authority on
  the assign call regardless of what the UI sends — the dropdown changes nothing
  server-side.
- **I — Information disclosure.** The dropdowns surface user labels
  (display-name/email) and tenant names. _Mitigation/AC:_ both come from
  endpoints the actor is already authorized to call (`GET /v1/admin/users` and
  `GET /v1/admin/tenants`, both admin-gated); the super-admin already sees these
  lists. No new field is exposed beyond what those endpoints already return; the
  tenant-admin path uses only the within-tenant user list (no cross-tenant
  leakage).
- **S — Spoofing.** No new auth surface; the BFF tenants route (if added)
  forwards the existing bearer and passes the upstream status through verbatim
  (the slice-479 BFF pattern). No unauthenticated endpoint.
- **T — Tampering.** The selected ids are still validated (well-formed UUID) and
  the server is the gate; a hand-crafted request is exactly as constrained as it
  was with text inputs (478 owns validation). The dropdown narrows, not widens,
  the input space.
- **R — Repudiation.** Unchanged — 478 still audit-logs every assign/revoke
  (this is a UI input change only).
- **D — Denial of service.** The tenants list + users list are bounded
  admin reads (already paginated/bounded by 478/143). A pathologically large
  tenant/user count would make a plain `<select>` long, not unbounded — the
  follow-on searchable-combobox note covers that scale case.

## Acceptance criteria

- [ ] **AC-1.** In the assign dialog, the **user** field is a dropdown populated
      from the already-loaded `GET /api/admin/users` list; each option label
      shows display-name + email, value = user id. No second fetch added.
- [ ] **AC-2.** The **tenant** field is a dropdown populated from
      `GET /v1/admin/tenants` (via a BFF route forwarding the bearer); each option
      label shows the tenant name, value = tenant id. (Add the shadcn `Select`
      primitive at `web/components/ui/select.tsx` if not present, or reuse an
      existing select/combobox primitive — engineer's call, recorded in the
      decisions log.)
- [ ] **AC-3.** For a **tenant-admin** (within-tenant shape), the tenant field is
      **pinned** to their session tenant (read-only / pre-selected), NOT a
      cross-tenant chooser (P0-479-2); the user dropdown lists only their tenant's
      users.
- [ ] **AC-4.** For a **super-admin** (cross-tenant shape), the tenant dropdown
      lists all tenants from `GET /v1/admin/tenants`.
- [ ] **AC-5.** The selected user id + tenant id POST to the existing
      `/v1/admin/users/assign` exactly as before; success/error inline states +
      TanStack invalidation behavior from slice 479 are unchanged.
- [ ] **AC-6.** The self-assign affordance, the role checkbox group, and the
      revoke-with-confirm flow are unchanged.
- [ ] **AC-7.** Authz-honest (slice-225/479): a tenant-admin sees no cross-tenant
      tenant chooser; a 403 from the assign call still renders inline (UI-honesty
      advisory stays green).
- [ ] **AC-8.** Accessible: both dropdowns are labelled (`<label htmlFor>` or the
      Select primitive's ARIA), keyboard-navigable, per the slice-331/363 a11y
      lineage.
- [ ] **AC-9.** Vitest covers any new BFF tenants route handler + the
      option-mapping/pinning lib logic (node-only per slice 069); the Playwright
      `admin-users.spec.ts` is updated to drive the dropdowns (assign +
      self-assign flows) with BFF-mocked data.
- [ ] **AC-10.** Decisions log
      (`docs/audit-log/527-admin-user-assign-dropdowns-decisions.md`): the
      Select-primitive choice, the tenant-admin pinning UX, and the
      `detection_tier_actual` / `detection_tier_target` header.
- [ ] **AC-11.** A changelog entry.

## Constitutional invariants honored

- **#6 RLS / membership-bounded (P0-192-5).** The cross-tenant tenant dropdown is
  super-admin only; a tenant-admin stays pinned to their own tenant — the UI
  reflects the server's RLS-scoped authority and never widens it.
- **AI-assist boundary** — N/A (no AI surface).

## Canvas references

- `Plans/canvas/05-scopes.md` — tenancy + the membership-bounded model.
- Slice 479 (`docs/issues/479-admin-user-management-ui.md`) — the dialog being
  enhanced; slice 478 — the assign API; slice 142/143 — the admin tenants list.

## Dependencies

- **#479** (admin user-mgmt UI) — `merged`. The dialog this enhances.
- **#478** (user↔tenant assignment API) — `merged`. The assign endpoint.
- **#142/#143** (super_admin mgmt + tenant create/list) — `merged`. The
  `GET /v1/admin/tenants` source for the tenant dropdown.
- No unmerged technical dependency → `ready`.

## Anti-criteria (P0 — block merge)

- **P0-527-1.** Does NOT show a cross-tenant tenant chooser to a tenant-admin —
  pinned to their session tenant (P0-479-2 preserved).
- **P0-527-2.** Does NOT add backend behavior or a new authorization path — the
  server (478) stays the sole gate; this is UI input shape + a thin tenants-list
  BFF passthrough only.
- **P0-527-3.** Does NOT remove the self-assign affordance, the role picker, or
  the revoke-confirm from slice 479.
- **P0-527-4.** Does NOT weaken slice-479 authz-honesty (403s surfaced inline; no
  dead/over-promising controls).
- **P0-527-5.** Does NOT use vendor-prefixed test fixture tokens — neutral
  `test-*` only.

## Skill mix (3-5)

- `grill-with-docs` — align with the slice-479 dialog + the `GET /v1/admin/tenants`
  contract + the cross_tenant response-shape signal.
- `tdd` — vitest (BFF tenants route + pinning/option-mapping) + the updated
  Playwright assign/self-assign flow.
- `security-review` — the tenant-admin pinning (P0-527-1) is the load-bearing
  authz-honesty check.
- `simplify` — reuse the existing admin-page + BFF patterns; a plain select, not a
  bespoke combobox.

## Notes for the implementing agent

- **Grill output (design-time):** the page already loads the user list (reuse it
  for AC-1, no new fetch). The tenant list needs `GET /v1/admin/tenants` — check
  whether a BFF route already exists (`web/app/api/admin/tenants/route.ts`); if
  not, add a thin one mirroring `web/app/api/admin/users/route.ts` (bearer
  forward, status passthrough). The `cross_tenant` flag slice 479 derives from
  the user-list response shape is the signal for super-admin-vs-tenant-admin —
  reuse it to decide dropdown-vs-pinned (do NOT add a second authority probe).
- **shadcn `Select` is not yet in `web/components/ui/`** — slice 479 used a
  checkbox group, not a select. Add the standard shadcn `Select` primitive (it
  pulls `@radix-ui/react-select`, already transitively present via other Radix
  primitives — verify `web/package.json`) OR use a native `<select>` if adding the
  Radix dep is undesirable; record the choice + why in the decisions log. If you
  add a web dependency, that touches `web/package.json` (the ONE web-spine file —
  fine for a solo web slice).
- **Registration note (slice-382):** `_STATUS.md` row registered by the
  orchestrator on a `chore/status` branch, not this `docs/527` branch.
- Detection-tier: set both fields to `none` unless a bug surfaces during the
  build.
