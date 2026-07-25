# Slice 527 — decisions log (admin user-assign dialog: user + tenant dropdowns)

**Parent:** slice 479 (admin user-management UI), slice 478 (assign/revoke API),
slice 142/143 (admin tenants list). Maintainer UX request 2026-06-07.

**Type:** JUDGMENT (dropdown data-source + tenant-admin pinning UX).

**detection_tier_actual:** none
**detection_tier_target:** none

(No bug surfaced during the build. The slice is a UI input-shape change over
already-shipped, already-tested backend + BFF surfaces.)

---

## Decisions

### D1 — Dropdown primitive: native `<select>`, not base-ui Select, not Radix

**Decision.** Use a native HTML `<select>` element, wrapped in a thin
`web/components/ui/select.tsx` styled to match `web/components/ui/input.tsx`.
NOT the shadcn `@radix-ui/react-select` the slice doc mentions, and NOT
`@base-ui/react`'s Select.

**Why.**

1. **No-new-dep norm (slice 277 P0, reaffirmed by the slice-363 checkbox
   comment).** The project's form-primitive family is `@base-ui/react`, not
   `@radix-ui/*`. Adding `@radix-ui/react-select` would introduce a second,
   redundant headless-component family for one dropdown — exactly the kind of
   dependency sprawl the project's "no new top-level deps" P0 exists to prevent.
   The slice doc's "@radix-ui/react-select … already transitively present via
   other Radix primitives" premise is **false for this codebase** — there are no
   Radix deps (verified: `grep -i radix web/package.json` → empty;
   `web/components/ui/*` all wrap `@base-ui/react`). The slice doc offered the
   native `<select>` alternative explicitly; this is that path.

2. **base-ui Select would be the "matching family" choice, but it is heavier
   than the problem.** base-ui's Select is a portalled, popup-driven combobox-
   style component. The slice is explicitly a _plain select, not a bespoke
   combobox_ (skill mix: `simplify`; AC notes: "a plain select is the v0"). A
   native `<select>` is the minimum machinery that satisfies AC-2/AC-4/AC-8.

3. **Accessibility is free and correct.** A native `<select>` with an
   associated `<label htmlFor>` is keyboard-navigable and screen-reader-labelled
   by the platform — no ARIA wiring to get wrong (AC-8, slice-331/363 lineage).
   The visual focus ring matches the other form primitives via the same Tailwind
   `focus-visible:ring-3 focus-visible:ring-ring/50` shape.

**Cost accepted.** A native `<select>`'s open-menu visual differs slightly from
the shadcn popup look of the mockups. Per CLAUDE.md, mockup-vs-`web/` divergence
is expected and NOT fileable drift. For very large user/tenant lists a native
`<select>` becomes a long scroll — covered by the searchable-combobox spillover
note (filed as slice 528).

### D2 — Option-mapping + pinning logic lives in a pure `.ts` lib module

**Decision.** All non-JSX logic — mapping the loaded user list to
`{value,label}` options, mapping the tenants list to options, and computing the
pinned-vs-chooser tenant-field mode from the `cross_tenant` signal + the session
tenant — lives in `web/lib/admin/assign-options.ts`, a pure module with no React
import.

**Why.** vitest is node-only (slice 069 P0-A3): it cannot render JSX. The
project's established idiom for covering presentational logic under that
constraint is to extract the pure logic into a `.ts` module and unit-test it
there, leaving component-render coverage to Playwright (slice 363's
`checkbox-class.ts`, `web/testing.md`). This keeps the new logic on the fast
vitest loop and the wiring on the e2e loop.

### D3 — Tenant-admin pinning UX: read-only pinned field, reuse the existing signals

**Decision.** For a tenant-admin (cross_tenant=false), the tenant field renders
as a **read-only, pre-selected display** of the session tenant id (sourced from
the already-fetched `/api/me` `tenant_id`), NOT a `<select>`. No cross-tenant
tenant list is fetched or rendered. For a super-admin (cross_tenant=true), the
tenant field is the populated `<select>` from `GET /api/admin/tenants`.

**Why.**

- **P0-527-1 / P0-479-2 (the load-bearing authz-honesty check).** A tenant-admin
  must never see a cross-tenant chooser. Pinning — not a disabled dropdown
  containing other tenants — is the honest UI: the only tenant they can act in
  is their own, so the field shows exactly that and nothing else. The
  cross-tenant tenant list (`GET /v1/admin/tenants`) is super_admin-gated
  upstream and is fetched ONLY when cross_tenant=true, so a tenant-admin's
  browser never even receives the other-tenant names (closes the STRIDE-I
  information-disclosure leg at the fetch boundary, not just the render boundary).
- **No second authority probe (P0-527-2).** The `cross_tenant` flag the page
  already derives from the user-list response shape is the sole super-admin-vs-
  tenant-admin signal. The TanStack query for the tenant list is gated on
  `enabled: crossTenant` so it never fires for a tenant-admin.
- **Session tenant source.** The page already fetches `/api/me` (for the
  within-tenant revoke fallback). The pinned tenant id reuses that value — no new
  fetch. If `/api/me` has not resolved yet, the field shows a "resolving session
  tenant…" placeholder and the submit is disabled (the same defensive shape the
  revoke button already uses).

### D4 — User dropdown reuses the already-loaded list (AC-1, no second fetch)

**Decision.** The user `<select>` options are mapped from `data.items` (the
existing `["admin","users"]` query). No new fetch. For a super-admin the
cross-tenant list may contain the same user id under multiple tenants — the user
option list is de-duplicated by user id (label = display-name + email), since
the assign dialog targets a user identity, and the tenant is chosen separately.

**Why.** AC-1 is explicit: reuse the loaded list, no second fetch. De-duping by
id keeps the dropdown one-entry-per-person even when the cross-tenant list has
N membership rows for one person.

### D5 — Self-assign keeps no user dropdown

**Decision.** In self-assign mode the user field stays hidden (the caller is the
target) — unchanged from slice 479. Only the tenant field changes shape (it
becomes the populated `<select>` for the super-admin, who is the only actor with
the self-assign affordance anyway).

---

## Revisit-once-in-use

- **R1 — Searchable combobox for large lists.** A native `<select>` is fine for
  the solo-operator scale (tens of tenants, hundreds of users). At thousands of
  users/tenants the scroll-to-find becomes hostile. Filed as spillover slice 528
  (docs-only). Revisit when an operator reports the scale pain.
- **R2 — Tenant-name display for the pinned field.** The pinned tenant field
  shows the session tenant _id_ (what `/api/me` returns). A tenant-admin would
  read a name more easily, but the within-tenant `/api/me` payload carries only
  the id, and fetching the tenant name would require a within-tenant tenant-self
  read endpoint that does not exist at v1. Showing the id is honest and
  unambiguous; the name lookup is a future nicety, not a v0 requirement.

## Confidence

**High.** The change is additive UI over stable, tested surfaces (478 API + 479
BFF + 143 tenants list + 142/143 super-admin gate). No backend behavior changes;
the server remains the sole authority (P0-527-2). The load-bearing authz-honesty
property (P0-527-1) is enforced at the _fetch_ boundary (tenant list gated on
cross_tenant), which is strictly stronger than a render-time gate.
