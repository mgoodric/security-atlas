# 463 — `demoseed.Seeder.Teardown` leaves the `tenants` row behind on a passing run

**Cluster:** Infra / Product correctness
**Estimate:** S (0.25–0.5d)
**Type:** JUDGMENT

**Status:** `ready`

> Surfaced during slice 462 (admindemo test-suite self-cleanup). Out of scope
> for that slice — 462 fixed the TEST harness's defense against a leaked
> `demo` tenant; this slice is the DISTINCT, underlying PRODUCT bug: the demo
> seeder's own teardown does not fully remove what it created. Filed for
> follow-up. Parent: slice 462 (grandparent: slice 461).

## Narrative

While building slice 462, the engineer observed that the leaked `demo` tenant
was not only a consequence of an _aborted_ prior run (462's framing) — it also
occurred on a **fully-passing baseline run**: `demoseed.Seeder.Teardown`
removes the demo-seeded child rows but leaves the parent `tenants` row behind.
That stale row is what then false-redded
`TestSeed_EnvUnsetGets503AndDoesNotSeed` on the next run.

Slice 462 hardened the _test_ (`cleanupDemoTenant` now ensures the slug is
absent at setup AND on teardown, with an unconditional FK-ordered raw sweep),
so the integration suite is now robust regardless. But the test hardening
_masks_ a real product gap: an operator (or any caller) who invokes the demo
seeder's `Teardown` to "undo" a demo seed is left with a dangling, empty
`demo` tenant. `Teardown` should be the inverse of `Seed` — if `Seed` creates
the tenant row, `Teardown` should remove it (or `Seed` should adopt an
existing tenant rather than create one).

The exact fix is a judgment call (see Open questions) and lives in
`internal/demoseed` (the production seeder), NOT in test code.

## Why this matters

- **Correctness:** `Teardown` advertises an inverse of `Seed`; leaving the
  tenant row violates that contract. "Reset my demo data" leaving a ghost
  tenant is a latent operator-visible bug.
- **Defense-in-depth restored:** with 462's test masking the symptom, this is
  the only remaining tracker for the underlying behavior. Without this slice
  the product bug is invisible until an operator hits it.

## Scope

- **In scope:** make `demoseed.Seeder.Teardown` leave no orphaned `tenants`
  row for a tenant it (or `Seed`) created — either by removing the row on
  teardown, or by making `Seed` adopt/guard an existing tenant so teardown's
  scope is unambiguous. Add/extend a `demoseed` unit or integration test that
  asserts `Seed` then `Teardown` returns the DB to its pre-seed tenant state.
- **Out of scope:** the admindemo test harness (slice 462 owns it — do not
  re-touch `cleanupDemoTenant`); broader demo-data redesign; any change to the
  `/v1/admin/demo` API surface or RLS policies.

## Acceptance criteria

- [ ] **AC-1.** After `Seeder.Seed(...)` followed by `Seeder.Teardown(...)`
      for the same tenant, no `tenants` row created by that `Seed` remains
      (verified by a `demoseed` test against a real DB).
- [ ] **AC-2.** Teardown is idempotent — calling it twice, or against a
      never-seeded tenant, does not error and leaves no partial state.
- [ ] **AC-3.** FK ordering is honored (children before parent); teardown does
      not violate any `ON DELETE RESTRICT` constraint (cf. slice 461's
      `controls.scf_anchor_id` finding).
- [ ] **AC-4.** RLS / tenancy boundary respected — teardown only removes rows
      for the tenant in its context, never another tenant's data (canvas
      invariant #6).
- [ ] **AC-5.** Slice 462's admindemo suite still passes unchanged (the test
      hardening is belt-and-suspenders; this fix removes the belt's _need_,
      not the belt).
- [ ] **AC-6.** Decisions log records the Seed-creates-vs-adopts choice and
      its confidence.

## Threat model (STRIDE)

- **S — Spoofing.** N/A — teardown operates within an established tenant
  context; no identity assertion changes.
- **T — Tampering.** A teardown that over-deletes (wrong tenant) would be data
  tampering. AC-4 constrains deletion to the in-context tenant via RLS; a
  cross-tenant delete must be impossible by construction (FORCE RLS), not by
  the seeder's own care.
- **R — Repudiation.** Demo seed/teardown is an admin action; ensure it
  remains on the existing admin audit surface (no new audit gap introduced).
- **I — Information disclosure.** N/A — teardown removes the actor's own demo
  data; no cross-boundary read.
- **D — Denial of service.** A teardown that errors on a partially-seeded or
  already-torn-down tenant (AC-2 violation) could wedge an operator's
  reset-demo workflow. Idempotency is the mitigation.
- **E — Elevation of privilege.** N/A — no privilege change; teardown is
  gated by the same admin authz as seed.

## Open questions

- **Seed creates vs. adopts the tenant.** If `Seed` should create the tenant,
  `Teardown` must delete it. If the tenant is expected to pre-exist (operator
  picks an existing tenant to fill with demo data), `Seed` should NOT create
  it and `Teardown` should NOT delete it — only the demo _content_. The
  current code creates it; the cleaner contract is the judgment call this
  slice resolves. (Resolve in the decisions log, not by blocking the merge.)

## Notes

- Surfaced in `docs/audit-log/462-admindemo-tenant-leak-cleanup-decisions.md`
  (the engineer recorded it there rather than filing a slice; the orchestrator
  filed this slice at batch-191 reconcile to keep it on the backlog).
- Lives in `internal/demoseed`. No dependency on other unmerged work →
  `ready`.
