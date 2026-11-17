# 2554 — Carry exact-match tenant overlay edges across an SCF release import: decisions log

Issue: mgoodric/security-atlas#2554, from the review of #2552 (#2315). After
an SCF release import, every tenant overlay edge (#1797) dropped out of
default coverage (#2203) until its tenant decided the #1804 proposal, even on
an anchor whose code and title did not change. Matt, 2026-11-17: carry the
exact matches forward, per tenant.

## Detection-tier classification

- `detection_tier_actual`: none
- `detection_tier_target`: integration

No bug surfaced. The gap was found in review.

## D1 — One rule, the same as #2315

An overlay edge is carried when its legacy anchor has the same code and the
same title in the current release: `SameCodeAndTitle`, the predicate that
re-binds controls (#1971 D3) and carries catalog edges (#2315 D2). There is no
second matcher. The `unchanged` remap class is not used, for the reasons in
#2315 D2. Everything else (renamed, split, reused code, withdrawn) stays an
open proposal for the tenant's writers.

## D2 — Per tenant, under RLS, not as the migrate role

Queueing stays one global act as the migrate role (#1804). The carry does
not. `QueueOverlayRemapProposals` collects the qualifying edges by tenant
while it queues, commits the queue, then opens one transaction per tenant
that runs `SET LOCAL ROLE atlas_app` and sets `app.current_tenant` to that
tenant. `mappingoverlay.CarryRemap` does every read and write under that
context, so RLS binds it exactly as it binds a writer's approval. A carry for
one tenant cannot see or write another tenant's proposals, edges, audit rows
or decisions.
`TestOverlayCarry_UnderAnotherTenantsContextWritesNothing` calls it with
tenant A's ids under tenant B's context and checks nothing changes.

The migrate role is a member of `atlas_app` (`migrations/bootstrap/01-roles.sql`
grants `atlas_app TO atlas_migrate WITH ADMIN OPTION`), so `SET LOCAL ROLE`
needs no new grant. A cluster set up without that bootstrap fails the import
loudly at the role switch rather than carrying under BYPASSRLS.

**Why per-tenant transactions rather than one.** A failure in one tenant's
carry would otherwise roll back every tenant's. The queue has already
committed, and each proposal is open until carried, so a re-run picks up
any tenant whose carry failed. The gap between the queue commit and each
tenant's carry is the same kind #2315's Timing note accepts.

## D3 — Keep the legacy overlay edge

A writer's approval retires the legacy overlay edge with reason `remapped`
(#1804). The carry does not. It mirrors #2315 D3: a read pinned to the old
release (`?scf_release=<old>`) must return what it returned before the
upgrade. Default coverage reads only the current release, so the two edges do
not double-count. `approveRemap` takes a `retireLegacy` flag. `DecideRemap`
passes true and `CarryRemap` false. Nothing is deleted, and both edges stay
active, so neither the #2474 catalog-delete guard nor the #2484 lock has
anything to fire on.

## D4 — Recorded as a system decision in the tenant's trail

The carried edge's `created_by`, its `create` row in
`fw_to_scf_overlay_edge_audit_log`, and the `scf_overlay_remap_decisions` row
all record `system:scf-import` (`mappingoverlay.RemapCarryActor`, the same
string as the catalog carry's `CarryForwardDecider`). Every row carries the
tenant's own `tenant_id`. The reason names the code and release.

The author column (#2286/#2522) named only `system` as "System". It now
names any `system:<process>` actor "System" too, so the carried edge does not
read "Unknown". `system:catalog_delete` (the #2474 guard's actor) benefits as
well. No credential is minted with a `system:` prefix (users are uuids, keys
`key_`, sessions `jwt:`), so the prefix cannot be claimed by a person.

## D5 — Close a proposal whose target pair already exists

From the review of #2552: when a same-code proposal's requirement already maps
to the target anchor, approval is refused for good (`scf_edge_remap_decide`
raises `edge exists`, and an overlay approval returns `ErrDuplicateOverlay` or
`CatalogEdgeExistsError`). #2315 D4 skipped the carry and left the proposal
open, so a person faced a proposal no approval could resolve.

The import now closes it as `kept_legacy`, decided by `system:scf-import`,
with the reason "closed by the SCF import: the requirement already maps to
<code> in <release>". This applies to catalog proposals (through
`scf_edge_remap_decide`) and overlay proposals (through the same decide path
as a writer). The reports count it as `Closed` and the CLI prints
`closed=<n>`.

**Why `kept_legacy` and not a new status.** The literal meaning holds: the
legacy edge stays where it is, and the target pair already exists. A new
`superseded` status would need a migration on both proposal tables and both
decision tables, and every list filter, API enum and UI badge would have to
learn it. The reason column says why the import closed it.

**Scope.** Only open proposals that match the carry rule are closed. A
rejected proposal is a person's decision and stays as they left it (#2315 D4).
A renamed or split proposal whose single successor happens to be taken is
left open, since a person may still choose another anchor.

The case also arises on its own at the next release: after A to B, both the
legacy A edge and the carried B edge are active on anchors that are not
current. On B to C both are queued, the first carries, and the second finds
the pair taken and is closed.

## D6 — No migration

The carry, the close and the role switch use existing tables, functions,
grants and statuses. Migration `20261251000000` was reserved and not taken.
