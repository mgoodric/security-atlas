# 528 — Admin user-assign dialog: searchable combobox for large user/tenant lists

**Cluster:** Frontend
**Estimate:** S (0.5–1d)
**Type:** JUDGMENT (combobox primitive + filter UX)

**Status:** `deferred` (no operator-reported scale pain yet; pick up when a
deployment hits a user/tenant count where the plain `<select>` scroll is hostile)

> Filed 2026-06-07 as a spillover of slice 527 (admin user-assign dropdowns).
> Parent: slice 527. The slice-527 v0 deliberately shipped a plain native
> `<select>` ("a plain select is the v0; a searchable combobox for very large
> user/tenant lists is a follow-on if the operator hits a scale limit").

## Narrative

**Why.** Slice 527 replaced the assign dialog's raw-UUID `Input`s with native
`<select>` dropdowns (user from the already-loaded list, tenant from
`GET /api/admin/tenants`). A native `<select>` is correct and accessible for the
solo-operator scale the v1 persona targets (tens of tenants, hundreds of users).
At thousands of users/tenants — a large multi-tenant deployment — the
scroll-to-find becomes hostile: a native `<select>` has no type-to-filter beyond
single-keystroke prefix matching, so finding one person among thousands means
scrolling.

**What.** Replace the two native `<select>`s with a **searchable combobox**
(type-to-filter, keyboard-navigable, ARIA `combobox`/`listbox`) for the user and
(super-admin) tenant fields. Keep the slice-527 contract intact:

- User options still come from the **already-loaded** user list (no new fetch).
- Tenant options still come from `GET /api/admin/tenants`, fetched **only** when
  `cross_tenant` is true (the tenant-admin pinning + fetch-gate from slice 527
  is preserved verbatim — P0-527-1 / P0-479-2 stay load-bearing).
- The selected ids still POST to `/v1/admin/users/assign` unchanged; the server
  (478) stays the sole authority.

**Primitive note.** The project's headless-component family is `@base-ui/react`
(NOT Radix — verified in slice 527). base-ui ships a Combobox/Autocomplete; that
is the natural choice over adding a Radix dep. The slice 527 decisions-log D1
rationale (no-new-dep, base-ui-family) carries forward — this slice picks the
base-ui combobox vs. a hand-rolled `<input list>`/`<datalist>` and records the
call.

**Scope discipline.** UI-only. NO backend change, NO new endpoint, NO change to
the assign/revoke API or the authz model. Does NOT add server-side
user/tenant search (the lists are already bounded admin reads; client-side
filter over the loaded list is sufficient until a list outgrows the loaded
page — at which point server-side paginated search is its own larger slice, not
this one).

## Threat model

STRIDE — verdict **has-mitigations** (a client-side filter over data the actor
is already authorized to read; identical authority surface to slice 527).

- **E — Elevation of privilege.** Unchanged from slice 527: the combobox is a
  filter over the _same_ option sets. The tenant combobox renders ONLY for a
  super-admin (gated on `cross_tenant`); a tenant-admin stays pinned. The server
  (478) re-checks authority on the assign call regardless of UI input.
- **I — Information disclosure.** No new field is surfaced — the combobox shows
  the same labels (user display-name/email, tenant name) the slice-527 `<select>`
  already showed, from the same admin-gated endpoints. The tenant-admin path
  still never fetches the cross-tenant list.
- **S — Spoofing.** No new auth surface; no new endpoint.
- **T — Tampering.** The selected ids are still UUID-validated client-side and
  the server is the gate; a combobox narrows the input space exactly as a
  `<select>` does.
- **R — Repudiation.** Unchanged — 478 audit-logs every assign/revoke.
- **D — Denial of service.** A client-side filter over an already-loaded bounded
  list is O(n) per keystroke over a bounded n — no unbounded work. If a list
  genuinely outgrows the loaded page, that is the server-side-search slice noted
  under scope discipline, not this one.

## Acceptance criteria

- [ ] **AC-1.** The user field is a searchable combobox (type-to-filter over the
      already-loaded list; no new fetch) — value = user id, label = display-name + email.
- [ ] **AC-2.** The super-admin tenant field is a searchable combobox over
      `GET /api/admin/tenants` (fetched only when `cross_tenant` is true).
- [ ] **AC-3.** The tenant-admin pinning (read-only session tenant, no chooser,
      no cross-tenant fetch) from slice 527 is preserved verbatim.
- [ ] **AC-4.** Accessible: ARIA `combobox`/`listbox`, keyboard navigation
      (arrow keys + Enter + Escape), labelled — slice-331/363 a11y lineage.
- [ ] **AC-5.** vitest covers the filter/option-mapping logic (node-only);
      Playwright `admin-users.spec.ts` updated to drive the combobox filter +
      select flows.
- [ ] **AC-6.** Decisions log (combobox primitive choice + filter UX) +
      changelog entry.

## Anti-criteria (P0 — block merge)

- **P0-528-1.** Does NOT weaken the slice-527 tenant-admin pinning or its
  fetch-gate (the cross-tenant tenant list still fetched only when
  `cross_tenant` is true).
- **P0-528-2.** Does NOT add backend behavior, a new endpoint, or server-side
  user/tenant search — client-side filter over the loaded lists only.
- **P0-528-3.** Does NOT add a Radix dependency — use the existing
  `@base-ui/react` combobox family (slice-277 no-new-dep P0).
- **P0-528-4.** Does NOT remove the self-assign affordance, the role picker, or
  the revoke-confirm.

## Dependencies

- **#527** (admin user-assign dropdowns) — the v0 `<select>` this replaces.
- **#479 / #478 / #142 / #143** — the underlying UI + API + tenants list
  (all merged).

## Notes for the implementing agent

- The slice-527 pure logic module `web/lib/admin/assign-options.ts` already maps
  - de-dupes options; extend it with a `filterOptions(query, options)` helper and
    keep it node-testable (no JSX) per the slice-069 / slice-363 pattern.
- Detection-tier: set both fields to `none` unless a bug surfaces during the
  build.
