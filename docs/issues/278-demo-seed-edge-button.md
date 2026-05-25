# 278 — Demo-seed UI button (edge-only via `ATLAS_ENABLE_DEMO_SEED`)

**Cluster:** Frontend + Backend (thin admin endpoint)
**Estimate:** 1-1.5d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Slice 205 (merged at `072f9a7`) shipped `atlas-cli demo seed` +
`atlas-cli demo teardown` plus the `internal/demoseed/` package
that builds the curated demo dataset. Both commands are gated by
the `ATLAS_ENABLE_DEMO_SEED` env var — without it, the CLI
refuses to invoke. That gating is exactly the right primitive
for an edge-only UI button.

Today the only way to reseed the demo tenant is to SSH into the
edge box and run the CLI. That works for the maintainer but is
operationally annoying when demoing — every "let's reset and
walk through again" requires shell access. The atlas-edge
deployment exists specifically for demo / showcase use; surfacing
the existing seed + teardown actions as buttons in the admin UI
removes one of the last manual steps in the demo loop.

### What ships in this slice

**Backend (`internal/api/admindemo/`)** — new package with two
HTTP routes:

- `POST /v1/admin/demo/seed` — invokes the existing
  `demoseed.Seed(ctx, opts)` flow. Returns 200 + a small JSON
  summary `{tenant_id, controls, risks, evidence, audit_periods, samples}`
  on success; 503 with `{error: "demo seed not enabled"}` if the
  env-var gate is unset.
- `POST /v1/admin/demo/teardown` — invokes `demoseed.Teardown(ctx, opts)`.
  Same gating + response shape.

**Gating (defense in depth)**:

1. **Env-var precondition**: the handler reads
   `ATLAS_ENABLE_DEMO_SEED` (same name the CLI uses, slice 205's
   `demoEnableEnvVar`). Empty / unset → 503. On atlas-edge the
   docker-compose sets it to `true`; on production deployments
   it's never set.
2. **Admin role admit** via existing slice 035 OPA + slice 062
   admin gate. The endpoint is mounted under `/v1/admin/*` so
   the existing admin middleware applies. Non-admin → 403.
3. **Meta-audit row** per invocation via existing slice 030
   `meta_audit_log` pattern. Action values:
   `demo_seed` + `demo_teardown` (new CHECK-extension migration —
   see migration discipline below).

**Frontend (settings page `/admin/demo` route)**:

- New route `web/app/(authed)/admin/demo/page.tsx`. Two buttons:
  "Reseed demo dataset" + "Tear down demo tenant". Both gated
  by a new `/api/admin/demo/status` BFF route that returns
  `{enabled: boolean}` so the page renders a friendly
  "demo tools not enabled on this deployment" banner instead
  of broken-button affordances when `ATLAS_ENABLE_DEMO_SEED`
  is unset.
- Confirmation dialog before each action (shadcn `<AlertDialog>`).
- After action completes: toast with the summary counts.
- Loading state during the action (these can take 5-10s).

**Migration**: `20260524NNNNNN_demo_seed_meta_audit.sql` extends
`meta_audit_log.action` CHECK constraint to permit `demo_seed`
and `demo_teardown` values. Pattern mirrors slice 175 / 269 /
many others — single ALTER + CHECK rewrite, idempotent.

### Scope discipline (deliberately OUT)

- **Multi-tenant seed selection**. Slice 205's CLI accepts
  `--tenant-slug=<slug>`. The UI button uses the default slug
  (`demo`). Multi-tenant demo seeds are a v2 follow-on.
- **Custom seed scale**. Slice 205's CLI accepts `--scale=<float>`.
  The UI button uses the default (1.0). Custom scale is a v2
  follow-on (would need a slider + validation).
- **Other admin operations**. This slice ships ONLY demo seed +
  teardown. Other dangerous-but-edge-useful operations (DB reset,
  bulk-delete tenants, etc.) are explicitly out.
- **Auto-reseed on schedule**. The buttons are operator-driven.
  No cron, no auto-reseed-on-boot. A future slice could add a
  scheduled reseed at midnight for atlas-edge specifically.

## Threat model

| STRIDE                       | Threat                                                        | Mitigation                                                                                                                                                                                                                        |
| ---------------------------- | ------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | A non-authenticated caller hits `/v1/admin/demo/*`.           | Inherits slice-190 jwtmw + slice-035 OPA admit (`role=admin` required). Unauthenticated → 401.                                                                                                                                    |
| **T** Tampering              | Caller injects shell metacharacters via tenant slug or scale. | The buttons hard-code defaults — no per-invocation user input flows to the backend handler. The CLI parses + validates these; the UI bypasses them entirely.                                                                      |
| **R** Repudiation            | Admin reseeds the demo tenant then denies doing so.           | Per-invocation meta-audit row captures `actor_id`, action enum (`demo_seed` or `demo_teardown`), `ip`, timestamp.                                                                                                                 |
| **I** Information disclosure | Seed summary leaks data from non-demo tenants.                | The seed handler only operates on the demo tenant slug; cross-tenant data is RLS-isolated. The response summary contains only the demo tenant's counts.                                                                           |
| **D** Denial of service      | Repeated reseed/teardown drains DB CPU.                       | Per-IP rate limit via slice-188's token-bucket primitive: 1 reseed per 60s. Confirmation dialog adds operator friction; this is opt-in not auto.                                                                                  |
| **E** Elevation of privilege | A non-admin caller bypasses the env-var gate.                 | Defense in depth: (1) env-var gate returns 503 to ALL callers regardless of role; (2) admin role gate enforces 403 for any callable surface; (3) docker-compose sets the env var only on atlas-edge profile, never on production. |

**Verdict.** **has-mitigations** — the env var + admin role +
meta-audit triple is sufficient. The threat surface is real
because the action mutates demo data, but every mutation path
flows through the same `internal/demoseed/` package which slice
205 already pinned with tests + RLS-aware integration coverage.

## Acceptance criteria

### Backend (`internal/api/admindemo/`)

- [ ] **AC-1.** New package `internal/api/admindemo/` with two
      chi handlers `Seed(w, r)` + `Teardown(w, r)`.
- [ ] **AC-2.** `POST /v1/admin/demo/seed` mounted in
      `internal/api/httpserver.go` under the admin admit chain
      (chi `Mount("/v1/admin", ...)` or equivalent).
- [ ] **AC-3.** `POST /v1/admin/demo/teardown` mounted likewise.
- [ ] **AC-4.** Env-var precondition: both handlers read
      `ATLAS_ENABLE_DEMO_SEED`. Empty / unset →
      `503 {error: "demo seed not enabled on this deployment"}`.
- [ ] **AC-5.** Admin role admit: both handlers require
      `role=admin` per slice-035 OPA matrix. Non-admin → 403.
- [ ] **AC-6.** Both handlers delegate to the existing
      `internal/demoseed/` package (`Seed` / `Teardown` functions)
      with the default tenant slug + scale.
- [ ] **AC-7.** Per-invocation `meta_audit_log` row written with
      `action='demo_seed'` or `'demo_teardown'`, `actor_id` from
      the JWT, `ip` from the request, and a small JSON `details`
      blob carrying the result summary.
- [ ] **AC-8.** Rate limit: 1 invocation per 60 seconds per IP.
      429 with `Retry-After` header on exceeded.
- [ ] **AC-9.** New BFF route `web/app/api/admin/demo/status/route.ts`
      returns `{enabled: boolean}` by probing the backend for
      503-vs-other. Frontend uses this to render the
      banner-vs-buttons split.

### Migration

- [ ] **AC-10.** New migration
      `migrations/sql/20260524NNNNNN_demo_seed_meta_audit.sql`
      extends `meta_audit_log.action` CHECK to permit
      `demo_seed` + `demo_teardown`. Idempotent + reversible
      (`.down.sql` ships with it). Mirrors slice 175 / 269 pattern.

### Frontend

- [ ] **AC-11.** New route `web/app/(authed)/admin/demo/page.tsx`.
      Renders two buttons (Reseed + Teardown) when the
      `/api/admin/demo/status` BFF reports `{enabled: true}`;
      renders a polite "Demo tools are not enabled on this
      deployment. Set `ATLAS_ENABLE_DEMO_SEED=true` in the
      docker-compose env to expose these actions." banner when
      `{enabled: false}`.
- [ ] **AC-12.** Each button opens a shadcn `<AlertDialog>`
      confirmation before firing. Cancel reverts; confirm calls
      the POST + shows a loading spinner.
- [ ] **AC-13.** On success, a toast shows the summary counts
      (e.g., "50 controls · 20 risks · 200 evidence · 1 audit
      period · 12 samples seeded"). On error (e.g., timeout),
      a destructive toast shows the error message.
- [ ] **AC-14.** Sidebar nav surfaces "Admin → Demo tools"
      ONLY for admins AND only when `/api/admin/demo/status`
      reports enabled. Composes with slice 186's role-gated
      sidebar pattern.

### Tests

- [ ] **AC-15.** Integration test
      `internal/api/admindemo/integration_test.go` covers: - happy path: admin caller + env var set → 200 + seed runs + meta-audit row written - env var unset → 503 + NO seed runs + NO meta-audit row - non-admin caller + env var set → 403 + NO seed runs - rate limit: second invocation within 60s → 429
- [ ] **AC-16.** OPA matrix test
      `internal/authz/slice278_test.go` pins the admit set:
      admin admit; auditor / grc_engineer / control_owner /
      viewer / no-role deny.
- [ ] **AC-17.** Vitest for the BFF status route
      `web/app/api/admin/demo/status/route.test.ts` covers
      the 503-detection branch + the 200-passthrough branch.
- [ ] **AC-18.** Playwright spec
      `web/e2e/admin-demo.spec.ts` covers: button renders when
      enabled, banner renders when disabled, confirmation
      dialog blocks accidental click, toast appears on success.
      Uses `page.route` to mock both endpoint responses (no
      live seed during e2e).

### Polish

- [ ] **AC-19.** CHANGELOG bullet under `### Added`.
- [ ] **AC-20.** Decisions log
      `docs/audit-log/278-demo-seed-button-decisions.md`
      captures: D1 (env-var name reuse vs new name), D2 (default
      tenant slug + scale), D3 (rate limit window 60s rationale),
      D4 (confirmation dialog vs second-click-confirms vs
      no-confirm), D5 (any deviations from slice 205's CLI
      behavior).

## Constitutional invariants honored

- **Invariant 6 (tenant isolation via RLS).** The seed handler
  operates only on the demo tenant slug; RLS enforces the
  boundary on all writes through `internal/demoseed/`.
- **Audit-log integrity (#2).** Per-invocation meta-audit row;
  CHECK-constraint migration matches existing pattern.
- **AI-assist boundary.** No LLM in the loop.
- **No fabrication.** The seed dataset is slice 205's, unchanged.
  This slice does NOT alter the dataset.

## Canvas references

- `Plans/canvas/01-vision.md` — atlas-edge as the demo showcase
  surface.
- `Plans/canvas/10-roadmap.md` — demo-readiness milestones.

## Dependencies

- **#205** (atlas-cli demo seed + teardown CLI + `internal/demoseed/`
  package) — `merged` at `072f9a7`. This slice consumes the
  package directly.
- **#035** (OPA middleware) — `merged`. Admin role admit.
- **#062** (admin credentials API gate pattern) — `merged`.
  Reference for the `/v1/admin/*` mount pattern.
- **#175** (meta_audit_log CHECK extension precedent) — `merged`.
  Reference for the migration.
- **#186** (role-conditional sidebar pattern) — `merged`.
  Reference for the nav entry gating.
- **#188** (rate-limit token-bucket) — `merged`. Reference for
  the 1-per-60s gate.

## Anti-criteria (P0 — block merge)

- **P0-278-1.** Does NOT auto-enable the demo endpoint on any
  deployment. The env var must be explicitly set; the docker-
  compose `compose.edge.yml` is the only file that sets it.
- **P0-278-2.** Does NOT accept user-supplied tenant slug or
  scale from the UI. The buttons hard-code the defaults; only
  the CLI accepts user-supplied flags.
- **P0-278-3.** Does NOT skip the admin role check. Even with
  the env var set, non-admins MUST get 403.
- **P0-278-4.** Does NOT skip the meta-audit row write. Every
  invocation creates exactly one row, regardless of seed outcome.
- **P0-278-5.** Does NOT alter slice 205's `internal/demoseed/`
  package. The UI is a thin wrapper.
- **P0-278-6.** Does NOT bypass slice 205's existing safeguards
  (the package's idempotency, tenant-slug binding, RLS handling).
- **P0-278-7.** Does NOT introduce a new authz role. Admin is
  the only gate.
- **P0-278-8.** Does NOT log the seed dataset contents anywhere
  except the meta-audit summary blob (which is the small JSON
  counts, NOT the actual seeded rows).

## Skill mix (3-5)

1. Go chi HTTP handler + admin admit chain composition
2. Go integration test with real Postgres + RLS roles
3. Next.js App Router route + shadcn `<AlertDialog>` composition
4. OPA admit-set extension + Rego policy mirror
5. Migration discipline (CHECK constraint extension + .down.sql)

## Notes for the implementing agent

### Phase 2 grill output (self-grill)

- **Domain model**: "demo seed", "teardown", and "ATLAS_ENABLE_DEMO_SEED"
  are all canonical per slice 205. No drift.
- **Scope creep**: User picked "Seed + Teardown pair" — two buttons.
  Resist temptation to add Status panel (deferred) or schedule
  controls (deferred). The slice is intentionally minimal because
  the underlying machinery (slice 205) is already done.
- **Constitutional invariants**: touches admin authz + meta-audit
  (both established surfaces). RLS preserved via slice 205's
  package. No new tenancy concerns.
- **Already-built check**: slice 205 covers the CLI side. No prior
  slice has filed a UI button for demo seed. Greenfield for the
  UI surface; thin wrapper around merged backend.

### Phase 3 threat model summary

Verdict: **has-mitigations**. Triple gating (env var + admin role +
meta-audit) is appropriate for a data-mutating action exposed in
UI. Anti-criteria P0-278-1..278-8 codify the mitigations.

### User-confirmed design decisions (from `/idea-to-slice` Q&A)

- **Gating**: env var on the backend (D1 = `ATLAS_ENABLE_DEMO_SEED`,
  same name slice 205's CLI uses).
- **Actions**: Seed + Teardown pair (both buttons, both
  confirmation-dialog-gated, no Status panel).

### Implementation order (recommended)

1. **Migration first** (AC-10) — extend `meta_audit_log.action`
   CHECK. Smallest reversible change; ships standalone.
2. **Backend package** (AC-1 → AC-8) — wire the two handlers,
   the env-var precondition, the admin admit, the rate limit,
   the meta-audit row. Integration tests against real Postgres
   (AC-15).
3. **OPA admit** (AC-16) — slice278_test pinning the matrix.
4. **BFF status route** (AC-9) + frontend status detection
   (AC-11 banner-vs-buttons split). Vitest covers the BFF
   (AC-17).
5. **UI buttons + AlertDialog** (AC-12 → AC-13). Playwright
   (AC-18) uses `page.route` to mock the endpoints.
6. **Sidebar nav** (AC-14) — last; composes with slice 186.

### Edge-deployment wiring

The docker-compose change is small but load-bearing. Slice 205
already added `ATLAS_ENABLE_DEMO_SEED=true` to the
`atlas-bootstrap` container's env. This slice extends that to
the `atlas-edge` service's env in `deploy/docker/docker-compose.edge.yml`.
Verify with:

```
docker compose -f deploy/docker/docker-compose.edge.yml config | grep -A2 ATLAS_ENABLE_DEMO_SEED
```

### Rate-limit rationale (D3)

60 seconds matches operator pace ("reset between demo runs"
typically 5+ minutes apart). 1-per-second would be too lax
(accidental double-click); 1-per-5-minutes would be too strict
for the "let me reset and try again" loop. Slice 188's
token-bucket primitive supports per-IP keys; the demo endpoint
keys on remote IP because the endpoint is admin-gated and the
single-tenant single-admin atlas-edge deployment means IP is
a reasonable per-actor proxy.

### Why a confirmation dialog, not a "type DELETE" pattern (D4)

Teardown is reversible (re-seed restores). The cost of an
accidental click is one re-seed cycle, not data loss. A simple
confirmation dialog is appropriate friction; a "type DELETE"
pattern would over-index on irreversibility that doesn't apply.

Provenance: filed 2026-05-24 via `/idea-to-slice` from Matt's
request "easily seed the demo data … Maybe a button in a
settings page or something we encode to our edge setup only".
User clarifications: gating = env-var on the backend; actions
= Seed + Teardown pair. Slice 205 (merged 2026-05-22)
provides the underlying machinery.
