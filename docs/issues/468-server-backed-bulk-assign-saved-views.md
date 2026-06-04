# 468 — Server-backed bulk-assign-owner + saved filter-views (controls)

**Cluster:** Frontend
**Estimate:** L (3-5d)
**Type:** JUDGMENT (authz-amplifier defense shape + saved-views schema)
**Status:** `ready`

## Parent

Spillover from **slice 448** (bulk operations + saved filter-views,
operator ergonomics — controls list). Slice 448 shipped the frontend
ergonomics shell (multi-select machinery + cap + client-side saved
filter-views) and **deferred the entire backend surface to this slice**
after discovering the spec's stated dependency does not exist on `main`.
See `docs/audit-log/448-bulk-ops-saved-views-decisions.md` D1/D3/D5.

## Why this is a separate slice

Slice 448's spec (AC-6…AC-13) presumed "reuse the existing single-item
assign-owner authz as the per-item check." **No such path exists:**

- `controls.owner_role` is a read-only TEXT role string
  (`migrations/sql/20260511000000_init.sql:176`); there is no owner-USER
  FK, no single-item assign mutation, and no assign affordance in the UI.
- The only control-mutation endpoint is `POST /v1/controls:upload-bundle`
  (whole-bundle replace).

Building the bulk-assign endpoint faithfully therefore requires inventing
the single-item owner-assign path FIRST, then the bulk path with the
load-bearing per-item authz-amplifier defense, plus a saved-views table
with RLS — a large `internal/api` + migration surface that would have
ballooned slice 448 well past its M estimate. The slice directive's
explicit guidance ("if it pulls you into internal/api significantly,
scope saved-views to client-side persistence for v1 and FILE the
server-backed version as spillover") routed it here.

## Scope

Two backend capabilities + the BFF/UI rewiring that swaps slice 448's
client-side stores for server-backed ones:

1. **Single-item owner-assign** prerequisite — the per-control
   owner-assign mutation (model + endpoint + authz + audit) the bulk path
   reuses as its per-item check. (May itself split out first if product
   direction wants per-control assignment sooner.)
2. **Bulk-assign-owner endpoint** — operates on a set of control IDs +
   a target owner; the load-bearing authz-amplifier defense.
3. **Saved-views table + endpoint** — RLS-scoped to (tenant, user);
   filter-payload validated against the known filter schema.

Then: rewire slice 448's `controls-toolbar.tsx` + `page.tsx` to call the
new BFF routes (the `SavedViewStore` seam makes the saved-views swap a
one-place change), flip `bulk-assign-future.ts` to a working trigger and
delete the disclosure module, and turn on the quarantined slice-448
Playwright assertions once the seed harness can establish the
preconditions.

## Acceptance criteria

Inherits slice 448 **AC-6 / AC-7 / AC-8 / AC-9 / AC-10 / AC-11 / AC-12 /
AC-13** verbatim (the backend + integration-test ACs). The load-bearing
one is **AC-11** (per-item authz: a caller cannot bulk-assign a control
outside their tenant/role via the bulk path; the bulk path is not weaker
than the single-item path). Plus:

- [ ] **AC-467-1.** The single-item owner-assign mutation exists and is
      authz-gated; the bulk path reuses it as the per-item check (no
      parallel authz path that could drift — slice 448 Notes).
- [ ] **AC-467-2.** Slice 448's `bulk-assign-future.ts` disclosure is
      replaced by a working trigger and the module is deleted.
- [ ] **AC-467-3.** Saved views migrate from client-side localStorage to
      the server-backed store (decide the one-time migration story for
      views saved during the slice-448 interim — see 448 decisions D-revisit-4).

## Constitutional invariants honored

- **#6 — Tenant isolation via RLS.** Bulk mutations + saved views are
  tenant- (and, for views, user-) scoped; per-item tenant re-check on the
  bulk path.
- **Repudiation discipline.** Bulk actions are audited (the bulk event
  references the affected set).

## Anti-criteria (P0 — block merge)

Inherits slice 448 **P0-448-1 … P0-448-5** (per-item authz; no silent
apply; capped/chunked; audited; no cross-user saved-view read). Plus:

- **P0-467-1.** The bulk path MUST reuse the single-item authz, never
  re-implement it (drift risk — slice 448 Notes + AC-11).

## Dependencies

- **#448** (this slice's parent) — `merged`. Ships the frontend shell +
  the injected-store seam this slice rewires.
- **#190** (OAuth-AS JWT validation) — `merged`. The role gate.

## Notes

- The authz-amplifier is the whole point: the bulk endpoint MUST be
  exactly as strict as the single-item path, re-checked per item. AC-11
  is the test that proves it. Desired detection-tier:
  `target=integration, actual=integration`.
