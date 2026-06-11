# Slice 660 — feature-flag nav + route gating — decisions log

**Type:** JUDGMENT
**Slice doc:** `docs/issues/660-feature-flag-route-nav-gating.md`
**Source:** 2026-06-10 empty-tenant UI audit, item ATLAS-008 (medium/major). Pairs with ATLAS-001/659/683.

The build-time subjective calls are recorded here per the JUDGMENT-slice
process. None of these touch the runtime AI-assist boundary (no LLM is
involved); they are product/engineering shape decisions.

---

## D1 — GATE the flags, do not re-copy (resolution choice)

**Decision:** Option (a) — make `oscal.export` and `board.reporting` actually
gate BOTH the nav entry and the route/handler. NOT option (b) (rewrite the
Features-page copy + flip the defaults to match what ships GA).

**Rationale:**

- The stated contract ("Disabling a module hides its routes") becomes TRUE
  rather than being weakened to match a cosmetic reality.
- The pre-GA `oscal.export` and `board.reporting` surfaces stop being exposed.
  In particular the OSCAL component-definitions page is broken on edge
  (ATLAS-001 / slice 659; 683 is the follow-on) — gating the route when
  `oscal.export` is off removes the user-facing exposure regardless of 659's
  outcome.
- The flag DEFAULTS are unchanged (both stay OFF pending GA). Nothing is
  auto-enabled. The anti-criterion (do not change the defaults' intent) holds.

**detection_tier_actual:** `manual_review` (the false contract was found by the
empty-tenant UI audit, not a test).
**detection_tier_target:** `playwright` — a nav-omission e2e + a gated-route
integration test are exactly the tier that should have caught "nav renders a
module whose flag is off". Both now exist (see D5).

---

## D2 — non-admin enabled-modules read shape (new endpoint vs fold-in)

**Decision:** A new dedicated `GET /v1/features/enabled` (authed, NOT
admin-only), returning `{"modules":{"oscal.export":bool,"board.reporting":bool}}`
for the caller's own tenant. NOT folded into `/v1/me` or install-state.

**Rationale:**

- The admin toggle surface (`GET/PATCH /v1/admin/features`) must stay
  admin-only — it carries the full flag inventory + per-flag audit metadata +
  the write path. Widening it to non-admins would leak attack-surface signal
  and the toggle path (explicit anti-criterion). A SEPARATE read keeps that
  boundary intact: the new endpoint has no write path and exposes ONLY the
  slice 660 gating booleans (`featureflag.GatingKeys`).
- `/v1/me` is role/identity-focused; install-state is an unauthed bootstrap
  probe. Folding gating booleans into either would conflate concerns and (for
  `/v1/me`) grow a frequently-fetched payload with module-shape data. A small
  purpose-built endpoint reads cleaner and is independently cacheable.
- Tenant scope comes from the credential + RLS via the Store (invariant #6);
  a caller cannot read another tenant's flag state.

The canonical gating set lives in `internal/featureflag.GatingKeys` (+
`IsGatingKey`) so the route-guard wiring and the enabled-modules handler share
one source of truth. The FE mirror is `NAV_FEATURE_GATES` in
`web/lib/feature-nav.ts`.

---

## D3 — gated-route status/shape (404 vs 403)

**Decision:** **404** + `{"error":"feature disabled"}` + an `X-Feature-Disabled:
<key>` header, applied to the WHOLE module's routes. Consistent across OSCAL and
board.

**Rationale:**

- This reuses the EXISTING `featureflag.Gate` middleware (slice 059), which
  already returns exactly this shape — no new response surface, no divergence.
- 404 (vs 403) makes a disabled capability indistinguishable from a
  non-existent route to a caller: a disabled module "does not exist" for this
  tenant. The `X-Feature-Disabled` header is observable to operators (which gate
  fired, in logs) without leaking to the caller which keys exist.
- No internal detail leaks (slice 367): the body is exactly one `error` key;
  the integration test asserts `len(body) == 1`.
- The gate is applied via `root.Group(...) { r.Use(Gate(store, key)); ... }`
  wrapping every route of the module (list + get + disposition verbs +
  scf-anchor map for OSCAL; briefs + packs for board) — not one route — so the
  gate is not cosmetic.

---

## D4 — server-side nav-gating mechanism

**Decision:** Gate the nav in `getAuthedNav()` (the existing single source of
truth the desktop sidebar AND the authed layout's mobile-drawer feed read from)
by applying a pure `gateNavItems(NAV_BASE, modules)` helper, with the
enabled-modules map fetched server-side via `fetchEnabledModules()`.

**Rationale:**

- `getAuthedNav()` already resolves the slice 186 admin gate once per request;
  adding the feature gate there keeps a single resolution point. The two probes
  (admin/me + features/enabled) are independent and run in parallel.
- The gating logic is a PURE function (`gateNavItems`) so it is unit-testable
  in vitest with no fetch, and both nav surfaces consume the same gated list (no
  duplicated gating logic). The href→flag-key binding (`NAV_FEATURE_GATES`)
  is the one place to add a future gated module.
- Fail-closed: a missing key (DB error, BFF failure) reads as "off" → the nav
  entry hides. Rendering a pre-GA nav link the route would 404 on is worse than
  a brief absence (mirrors the slice 186 admin-probe fail-closed convention).
- Module split: `feature-nav.ts` is pure/client-safe (imported by the gated
  client pages for `isFeatureDisabledError`); `feature-nav.server.ts` holds the
  `next/headers` fetch so it never reaches the client bundle.

**Server-side-read caveat (honest):** because the enabled-modules read happens
in the RSC (`getAuthedNav` → server-side fetch), Playwright `page.route()` (which
intercepts only browser requests) cannot mock it. The default-OFF nav-omission
e2e therefore leans on the real backend's Seed default (both flags off), which
is exactly the pre-GA state the gate protects. The flag-ON nav case is covered
at the Go-integration tier (route serves) + the vitest tier (`gateNavItems`
renders the item when the flag is true).

---

## D5 — coverage

| Tier           | What it proves                                                                                                                                  | File                                                                           |
| -------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| Go integration | gated route returns 404 (not 200) when flag OFF; serves when ON; no internal leak; **RLS isolation** (tenant A's ON does not unlock tenant B)   | `internal/api/features/gate_integration_test.go`                               |
| Go unit        | non-admin `/v1/features/enabled` read (correct booleans, 401 without cred, admin also allowed); `GatingKeys` all seeded + default OFF           | `internal/api/features/enabled_test.go`, `internal/featureflag/gating_test.go` |
| vitest         | `gateNavItems` hides the right items (fail-closed); `isFeatureDisabledError`; `/api/features/enabled` BFF proxy fail-closed + bearer forwarding | `web/lib/feature-nav.test.ts`, `web/app/api/features/enabled/route.test.ts`    |
| Playwright     | default sidebar omits Vendor Claims + Board Packs; direct-nav to a gated route shows the clean disabled panel (no raw error)                    | `web/e2e/feature-flag-gating.spec.ts`                                          |

The LOAD-BEARING claim — "the gated route is actually unreachable, not just
nav-hidden" — is proved by `TestGate_OSCALExport_OffReturns404` /
`TestGate_BoardReporting_OffReturns404`: they assert the sentinel downstream
handler NEVER runs and the status is 404. Without the `Gate` middleware these
would return 200 (the sentinel would run), so the test fails without the guard.

Per-file vitest floors added for `lib/feature-nav.ts` (98) and
`app/api/features/enabled/route.ts` (98) in the same PR. `internal/featureflag`
integration-tier coverage rose to 85.0% (floor 82) — no floor lift needed.

**detection_tier_actual:** `manual_review` · **detection_tier_target:**
`playwright` / `integration` (now covered at both).
