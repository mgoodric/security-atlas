# 613 — Web settings control for the per-tenant bundle gate policy: decisions log

Slice type: code (JUDGMENT-light). The slice is web-only and drives the
already-shipped slice-608 `PATCH /v1/tenants/{id}` surface. One genuine
build-time JUDGMENT call surfaced — the READ path — recorded below. This file
does NOT block merge.

- detection_tier_actual: none
- detection_tier_target: none

No shipped-behavior defect surfaced during the build. The control's pure logic
(mode set, labels, AC-3 explanations, the fail-safe-toward-strict parser) is
unit-tested at the vitest tier (24 cases, 100% coverage). The rendered control,
the PATCH round-trip, and the persisted-value reflection are covered by a
hermetic Playwright spec. No bug escaped to a later tier.

## Design calls

### D1 — READ path: pre-select the documented default (`strict`), reflect the PATCH response on save

- **Problem.** AC-1 wants the control "pre-selected to the tenant's current
  value (read via the existing tenant GET-shape / the PATCH response shape)."
  But `main` exposes NO read path that returns a single tenant's
  `bundle_gate_mode` to the web layer:
  - `GET /v1/me/tenants` (the only per-caller tenant read the settings page
    already uses) is a JWT-claim-bounded directory returning `{id, name,
current}` ONLY (`internal/api/me/tenants.go`) — no gate-mode column.
  - The admin tenants LIST (`GET /v1/admin/tenants`) `SELECT`s `id, name, slug,
is_bootstrap_tenant, created_at, created_by_user_id` — also no gate-mode
    column (`internal/api/admintenants/handler.go`).
  - There is no `GET /v1/tenants/{id}` at all; the tenants handler ships only
    the PATCH mutator.
- **Options considered.**
  - (a) Add `bundle_gate_mode` to the `/v1/me/tenants` response (or the admin
    list) so the page can read it on load.
  - (b) Add a new `GET /v1/tenants/{id}` read endpoint.
  - (c) Pre-select the documented default (`strict`) and reflect the
    authoritative value from the PATCH response after a save.
- **Chosen:** (c).
- **Rationale.** (a) and (b) are both BACKEND changes — a new Go handler or a
  widened wire shape + the integration tests that go with it. This slice is
  scoped WEB-ONLY (parent 608 owns the API; the slice doc + brief both forbid an
  `internal/` Go change). (c) is the honest web-only read: slice 608 D2
  documents that an unchanged tenant (absent row OR unrecognised value) resolves
  to `strict` server-side, so `strict` is exactly what the gate enforces for a
  tenant that has never touched the policy — pre-selecting it is not a guess, it
  is the correct display for the overwhelmingly common case. The PATCH response
  carries the authoritative `bundle_gate_mode` (608's handler returns the
  post-update `tenant` row), so once the operator saves, the control reflects
  ground truth. The UI surfaces the distinction honestly: the helper copy says
  "Until you change it, this tenant uses the default (`strict`)", and the
  post-save confirmation names the committed mode. The spec's AC-1 wording
  explicitly admits the PATCH-response-shape read, so (c) is within the AC.
- **Follow-up (spillover-worthy, NOT filed unless asked).** If a future slice
  wants the control to show a tenant's _already-customised_ value on first load
  without a write, the minimal backend add is `bundle_gate_mode` on the
  `/v1/me/tenants` directory row (one column in the `SELECT` + the wire struct).
  That is a backend change and out of scope here; noted for the maintainer.

### D2 — Surface location: the existing admin-gated Settings → Tenant section

- **Chosen:** render the control INSIDE the slice-144 `TenantSection` on
  `/settings`, which already (1) renders only for `isAdmin` callers
  (`{isAdmin ? <TenantSection /> : null}`), (2) reads the current tenant id from
  `/api/me/tenants`, and (3) PATCHes `/api/tenants/[id]`.
- **Rationale.** The gate policy is a tenant-level admin setting and `608`'s
  PATCH is gated on per-tenant admin OR super_admin — exactly the authority the
  `TenantSection` is already shown under. Reusing the section means AC-4
  (admin/super_admin-only) rides the existing role gate with zero new gating
  code, and the control sits next to the only other tenant-level mutator
  (rename). The platform PATCH is the canonical authority gate; the
  hide-when-not-admin is UX-only defense-in-depth (the slice-097 D3 pattern this
  section already follows).

### D3 — No new BFF route: reuse the slice-144 passthrough

- **Chosen:** PATCH through the existing `/api/tenants/[id]` route unchanged.
- **Rationale.** That BFF route forwards the raw request body verbatim
  (`const body = await req.text()`) to the platform — it is a pure proxy that
  already carries any field the platform accepts, including the `bundle_gate_mode`
  608 added. No BFF edit is needed or made; the existing route test still covers
  the proxy behavior.

## Scope honored

- WEB-ONLY: no `internal/` Go change, no new backend endpoint, no BFF route
  change. The only files touched are under `web/` plus the CHANGELOG + this log.
- New pure helper `web/app/(authed)/settings/gate-mode.ts` is floored at 98% in
  `web/coverage-thresholds.json` (measured 100%).
- Hermetic Playwright spec route-mocks the `/api/me/tenants` GET and the PATCH
  response — no shared-DB seed dependency for the control's state (b219 lesson).
- No `_INDEX.md` / `_STATUS.md` edits (slice 382).
