# Slice 278 — Demo-seed UI button — decisions log

**Slice spec:** [`docs/issues/278-demo-seed-edge-button.md`](../issues/278-demo-seed-edge-button.md)
**Branch:** `feature/278-demo-seed-edge-button`
**Status:** in-review

This log captures the JUDGMENT calls the implementing agent made while
building slice 278. Slice 278 is `Type: JUDGMENT`; the spec carries
five decision slots (D1-D5) plus three more that emerged during the
build (D6-D8). This file is the record.

---

## D1 — Env-var name: **reuse `ATLAS_ENABLE_DEMO_SEED`**

The spec offers "env-var name reuse vs new name" as the explicit
decision slot. Reuse won.

**Rationale:**

- Slice 205's CLI already established the env var. Introducing a
  parallel `ATLAS_ENABLE_DEMO_BUTTON` would split the gating story
  and require operators to flip two flags to enable both surfaces.
- Operationally simpler: one env var in
  `deploy/docker/docker-compose.edge.yml` enables both the CLI and
  the UI button. Slice 205's docs already document the env var.
- No semantic drift: both surfaces gate the same package (`internal/
demoseed`). The env var represents "this deployment opts in to
  demo-seed functionality" — neutral of which interface invokes it.

**Trade-off accepted:** an operator who wants the CLI but NOT the UI
button cannot achieve that with a single env var. Acceptable: the UI
button is also admin-gated and rate-limited, so the marginal exposure
is small.

---

## D2 — Default tenant slug + scale: **hard-coded `demo` slug + `1.0` scale**

P0-278-2 forbids user-supplied tenant slug or scale on the UI path.
The handler uses hard-coded constants at package scope:

```go
const demoTenantSlug = "demo"
```

Scale defaults to `demoseed.DefaultScale` (`1.0`).

**Rationale:**

- Surface-area discipline. Every additional user-supplied parameter
  expands the threat-model row count linearly (validation, injection,
  authz parity with the CLI). The button is a tracer-bullet "easily
  seed the demo data" tool; custom slugs are a v2 follow-on if
  demand surfaces.
- Slug "demo" is conventional (slice 205 docs use it in every
  example) and aligns with the atlas-edge URL pattern
  `https://atlas-edge.example.com/demo`.
- The operator who needs multi-tenant or custom-scale demo seeds
  still has the CLI; the button is the 80% path, not the 100% path.

---

## D3 — Rate-limit window: **60 seconds per IP**

The spec narrative explicitly justifies 60s ("matches operator pace
'reset between demo runs' typically 5+ minutes apart"). The
implementation uses an in-memory per-IP token bucket: one token
replenished after the 60-second window.

**Rationale:**

- 1-per-second too lax: an accidental double-click would seed twice.
- 1-per-5-minutes too strict: the "let me reset and try again" loop
  during a demo dress rehearsal would hit the limit.
- Per-Handler instance (not package-level) so tests don't interfere
  with each other.
- The Status endpoint is intentionally NOT rate-limited — the
  frontend polls it on every admin page load, and the cost is
  constant.

**Memory note:** the bucket map grows unbounded on unique IPs. Not
a concern at v1 because the upstream OPA gate requires admin role
first, so only authenticated admins can populate the map. A future
slice could swap for a TTL map if attacker creativity escalates.

---

## D4 — Confirmation UX: **shadcn `<Dialog>`, not `<AlertDialog>`, not "type DELETE"**

The spec mentions shadcn `<AlertDialog>` as the recommended
confirmation primitive. The codebase doesn't carry `<AlertDialog>` —
the slice 142 super-admins management surface uses the plain
`<Dialog>` for the same purpose.

**Options considered:**

- (a) `npx shadcn add alert-dialog` + accept the new dep. Adds an
  install step + a dep we don't otherwise need.
- (b) Use the existing `<Dialog>` primitive. Same UX, no new dep.
- (c) "Type DELETE to confirm" pattern. Over-indexes on
  irreversibility — teardown is reversible via reseed, so the cost
  of an accidental click is one reseed cycle, not data loss.

**Chosen: (b)** — `<Dialog>` mirrors the slice 142 pattern, no new
dep, same user friction.

---

## D5 — CHECK constraint regression repair

While inspecting the `me_audit_log.action` CHECK constraint to plan
the migration, I discovered that slice 269 (the dashboard-export
meta-audit, migration `20260524000000_dashboard_export_meta_audit.sql`)
had inadvertently dropped slice 205's `demo_seed_apply` and
`demo_seed_teardown` values when it rebuilt the constraint.

**Impact:** any new `atlas-cli demo seed` invocation against a
freshly migrated DB would fail with a CHECK violation. Slice 205's
integration test would also fail on a fresh DB.

**Decision:** the slice 278 forward migration RESTORES the four
demo-seed values (`demo_seed_apply`, `demo_seed_teardown`,
`demo_seed`, `demo_teardown`) and the down migration reverts to the
slice 269 baseline (no demo-seed values).

**This is a fix-forward of a latent regression. The slice doc did
NOT scope this work; documenting here so reviewers can trace the
decision.** A spillover slice was considered but rejected — the
repair is a single-line addition to a CHECK constraint that we are
already touching for the new values, so splitting it into a separate
slice would add merge friction without proportionate benefit.

---

## D6 — Route placement: **`web/app/admin/demo/page.tsx`** (no `(authed)` route group)

The slice doc specifies `web/app/(authed)/admin/demo/page.tsx`. The
actual codebase places admin routes under `web/app/admin/`, NOT
under the `(authed)` route group. (The `(authed)` group exists for
some surfaces, but the admin section has its own `web/app/admin/
layout.tsx` that handles the auth gate.)

**Decision:** honor the codebase convention. Place the page at
`web/app/admin/demo/page.tsx`.

**Trade-off:** small doc-vs-code drift. The `admin/layout.tsx`
already runs the `isAdmin` gate + redirects, so the functional
behavior is identical to a route under `(authed)`.

---

## D7 — HTTP-invocation action vs seeder-run action: **two-row forensic separation**

Slice 205's seeder writes its own `demo_seed_apply` / `demo_seed_teardown`
rows for the SEEDER RUN event. The slice 278 spec wants the HTTP
handler to write a `demo_seed` / `demo_teardown` row for the
INVOCATION event.

**Decision:** write BOTH rows.

- The HTTP handler writes `demo_seed` BEFORE invoking the seeder.
  Payload: `{slug, scale, ip, status: "invoked"}`.
- The seeder writes `demo_seed_apply` during its transaction.
  Payload: `{slug, demo_seed_v: "205"}`.

**Why two rows:**

- The HTTP-invocation row captures who clicked the button (actor +
  IP + click timestamp). The seeder row captures what the seeder
  did (tenant ID + slice 205 version stamp).
- If the audit row write succeeds but the seeder later fails (DB
  error mid-transaction), the invocation row remains as a forensic
  record. The seeder rolls back; only the HTTP-invocation row
  persists. Forensic queries can distinguish "click happened, seed
  rolled back" from "click happened, seed succeeded".
- Querying for `action IN ('demo_seed', 'demo_teardown')` cleanly
  enumerates click events without including seeder-run noise.

---

## D8 — No toast library: **use inline `<Alert>` for post-action feedback**

The slice doc references "destructive toast" but the codebase has
no toast library (`sonner` is not in `web/package.json`). The slice
142 pattern uses inline `<Alert>` components in a fixed position on
the page.

**Decision:** mirror slice 142. The post-action result lives in an
`<Alert>` rendered below the action buttons. `variant="destructive"`
applies for error cases; default variant for success.

**Trade-off:** less ergonomic than a toast (no auto-dismiss; user
must scroll or refresh to clear). Acceptable because the demo page
is a single-screen surface; the operator sees the result without
ambiguity.

---

## Anti-criteria honored

All P0-278-1 through P0-278-8 (slice doc) are enforced. The implementation
specifically:

- **P0-278-1**: env-var unset → 503 (handler line ~165). No deployment
  auto-enables.
- **P0-278-2**: hard-coded `demoTenantSlug = "demo"` + `demoseed.DefaultScale`
  passed to `Seeder.Apply`. The HTTP body is ignored (POST takes no body
  fields).
- **P0-278-3**: `requireAdmin` is the first gate; non-admins 403 before
  the env-var check. OPA admit at the middleware layer is the load-bearing
  duplicate gate.
- **P0-278-4**: `writeAuditRows` runs before `seeder.Apply` / `seeder.Teardown`;
  audit failure short-circuits to 500.
- **P0-278-5**: `internal/demoseed/` is untouched. The handler is a thin
  wrapper that imports the package.
- **P0-278-6**: the seeder's idempotency-on-slug + tenant-forensic-mark
  guard fire unchanged; the handler just invokes `Apply` / `Teardown`.
- **P0-278-7**: only admin role admits. Super_admin alone denies (see
  `internal/authz/slice278_test.go::TestSlice278_SuperAdminAloneDenies`).
- **P0-278-8**: response shape excludes admin password + dataset row
  contents. Only counts surface in the JSON body.

## Threat-model verdict

**has-mitigations** — the triple gate (env var + admin role + audit row)
is the right shape for an edge-only data-mutating button. All eight
anti-criteria are enforced. The OPA matrix test pins the admit set so
a future regression in admin.rego would be caught at the unit-test layer.
