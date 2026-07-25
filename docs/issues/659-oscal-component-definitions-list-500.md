# 659 — OSCAL "Vendor Claims" (component-definitions) list returns 500

**Cluster:** OSCAL
**Estimate:** S-M (0.5-1.5d)
**Type:** JUDGMENT (root-cause path: deploy/migration vs query bug)
**Status:** `ready` — surfaced by the 2026-06-10 empty-tenant UI audit (ATLAS-001).

## Narrative

The Vendor Claims page (`/oscal/component-definitions`, slice 589) renders a red
"Could not load imported component-definitions." error instead of a list or a clean
empty state. `GET /api/oscal/component-definitions` → **HTTP 500**; the RSC fetch →
**503**. Re-verified on `main` build `2a3805b` (still open after the actor-fix redeploy).

**Orchestrator code triage (2026-06-10):** the handler (`internal/api/oscalcomponents/handler.go`
`ListDefinitions`) and the slice-589 migrations (`migrations/sql/20260608010000_oscal_component_definitions.sql`
et al.) are correct on `main`; `requireOscalRead` gates on the caller's **role**
(`IsAdmin`/`IsApprover`/owner) — `admin@example.com` passes it — so this is **not** a
flag/authz 403. The 500 originates from `h.store.ListDefinitions(ctx)` erroring. CI
(fully-migrated DB) is green, which points the prime suspect at the **edge DB missing
the slice-589 OSCAL migrations** (migrate-on-bringup lagged or a migration failed
fail-closed) rather than a code bug — but the bare 500 hides the real error.

## Threat model

Read-only, RLS-tenant-scoped endpoint; no new data/scope/wire. The only risk is a noisy
internal error reaching the user — this slice also improves that surface (clean empty
state / no internal detail leak, per slice 367 discipline).

## Acceptance criteria

- [ ] **AC-1.** Diagnose from the deployment: capture `atlas-edge` + `atlas-migrate-edge`
      logs and `\dt`/`schema_migrations` on the edge DB. Determine whether the slice-589
      OSCAL tables exist. Record the actual logged error behind the 500.
- [ ] **AC-2.** If the cause is **unapplied/failed migration**: fix is on the deploy/migrate
      robustness axis (does migrate-on-bringup actually apply 589 from the current
      `-bootstrap:edge` image? did an earlier migration fail and halt the chain?). Make
      migrate failures loud + observable; confirm the OSCAL tables apply on a clean bring-up.
- [ ] **AC-3.** If the tables exist and it still 500s: it's a genuine `ListDefinitions`
      query/RLS bug — reproduce in an integration test, fix, and add the regression test.
- [ ] **AC-4.** Regardless of cause, the page renders a **clean empty state** ("No vendor
      claims imported yet") on an empty tenant instead of a 500/red banner (no internal
      error detail surfaced).

## Anti-criteria

- Does NOT widen the OSCAL read surface or change tenant scoping (RLS stays).
- Does NOT mask the bug by catching-and-empty-stating a real query error without first
  root-causing it (AC-1 must establish the true cause).

## Dependencies

- Slice 589 (`internal/api/oscalcomponents`, the OSCAL imported-catalog/component read API) — on `main`.
- Composes with slice 660 (feature-flag gating) — gating the OSCAL route when `oscal.export`
  is off would also remove the user-facing exposure, but does not fix the underlying 500.

## Notes

Source: 2026-06-10 empty-tenant browser audit, item **ATLAS-001** (priority high /
severity critical). Re-tested open on build `2a3805b`. Pairs with **ATLAS-008** (slice 660).
