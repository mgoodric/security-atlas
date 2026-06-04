# 157 — Dashboard: re-point upcoming-panel to /v1/upcoming + top-risks-panel to ?sort=residual,age

**Cluster:** Frontend
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** #147 (Dashboard placeholder panels — slice 066 follow-on)
**Endpoint source:** #066 (Dashboard backend read endpoints)

## Narrative

Spillover filed 2026-05-18 from slice 147 per Amendment 2.

Slice 066 shipped four backend endpoints to close slice 040's four
placeholder gaps:

1. `GET /v1/frameworks/posture` — picked up by slice 147
2. `GET /v1/activity` — picked up by slice 147
3. `GET /v1/upcoming` — **not yet consumed by the frontend**
4. `?sort=residual,age` on `GET /v1/risks` — **not yet consumed by the frontend**

Slice 147 kept its scope to the two `MissingEndpointPanel`-rendering
panels (the literal "endpoint does not exist on main yet" copy the
v1.10.0 operator reported). The remaining two panels render real
bound data today but with honest "partial-data caveat" footers
(`top-risks-sort-gap`, `upcoming-gap`) that the slice 066 backend
endpoints now obviate.

This slice closes the remaining two:

- **`upcoming-panel`**: re-point the BFF `web/app/api/dashboard/upcoming/route.ts`
  from `getExpiringExceptions(bearer, "30d")` to `getUpcoming(bearer)`,
  add a `getUpcoming` server-side fn that hits `/v1/upcoming`, update
  the panel to consume the unified `{due_date, category, title,
resource_type, resource_id}` row shape (with cursor + limit), and
  remove the `upcoming-gap` caveat footer.
- **`top-risks-panel`**: re-point `getMitigateRisks` to also pass
  `sort=residual,age` (the slice-066-shipped server-side sort), update
  the panel header to drop "server order (residual/age ranking pending)"
  and the `top-risks-sort-gap` caveat footer.

## Acceptance criteria

- [ ] AC-1: `web/app/api/dashboard/upcoming/route.ts` proxies to
      `/v1/upcoming` (slice 066), not `/v1/exceptions/expiring`.
- [ ] AC-2: `upcoming-panel.tsx` renders the unified rollup row shape
      (category badge + title + due-date countdown). No
      `upcoming-gap` testid in the DOM.
- [ ] AC-3: `top-risks-panel` binds to `/v1/risks?treatment=mitigate&sort=residual,age`
      (slice 066 D2 extends `ListRisks` for this sort). No
      `top-risks-sort-gap` testid in the DOM.
- [ ] AC-4: Vitest covers the two new BFF route handlers (401 path +
      upstream-200 passthrough).
- [ ] AC-5: Playwright dashboard spec assertions for the two panels
      updated (drop the old `upcoming-gap` / `top-risks-sort-gap`
      contains-text assertions).
- [ ] AC-6: CHANGELOG entry: "Dashboard `upcoming` + `top risks`
      panels now consume the slice-066 unified rollup +
      residual/age sort (#148; slice 066 follow-on)".

## Dependencies

- **#066** Dashboard backend read endpoints (merged) — endpoint source.
- **#147** Dashboard placeholder panels (this is the spillover from 147).

## Anti-criteria (P0 — block merge)

- **P0-148-1** Does NOT touch backend `internal/api/` — both endpoints
  already exist and are tested at the backend integration layer
  (slice 066 ISC-18/19/20/21).
- **P0-148-2** Does NOT remove the slice-040 panel-degrades-independently
  contract (each panel still owns its TanStack Query).
- **P0-148-3** Does NOT fabricate data; an empty `/v1/upcoming`
  response renders the panel's empty-state copy.

## Notes for the implementing agent

Mechanical re-point following the exact slice-147 template. Should be
half a day max. The slice-066 wire shapes are documented in
`internal/api/dashboard/handler.go` (`upcomingWire`, the
`?sort=residual,age` extension to `riskWire`).
