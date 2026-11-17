# 2315 — Carry declared crosswalk edges across an SCF release import: decisions log

Issue: mgoodric/security-atlas#2315, a follow-up from the review of #2308
(#2203). Requirement coverage now reads the current SCF release by default.
Once every crosswalk must declare `scf_release`, each SCF release import would
leave every crosswalk's edges on the previous release, and default coverage
would drop them until someone re-imported each crosswalk. Matt, 2026-11-15:
carry crosswalks forward.

## Detection-tier classification

- `detection_tier_actual`: none
- `detection_tier_target`: integration

No bug surfaced. The gap was found in review, before the declaration became
mandatory.

## D1 — Carry edges forward in the import, not by re-importing each crosswalk

**Decision.** `scfimport.QueueEdgeRemapProposals`, which `catalog import-scf`
already runs after `Import` and `RebindControls`, approves in the same
transaction every edge proposal whose legacy anchor has the same code and the
same title in the current release. It does not re-import the bundled
crosswalks with the new release declared.

**Why not re-import.** A re-import only reaches the crosswalks the platform
ships. A customer's own crosswalk, imported once from their own file, would
still drop out. A re-import would also have to rewrite each file's
`scf_release`, and it would bind every code in the new release whatever its
title, which is the silent re-point that #1971 D3 forbids.

**Why there.** The queue already lists every live catalog edge on a legacy
anchor with its lineage in the current release, and the approve path
(`scf_edge_remap_decide`) already copies an edge onto a successor anchor and
records it in the append-only `scf_edge_remap_decisions`. The carry is an
approval made by the import. It needs no new table, column, function or
migration. It also leaves an audit row a super-admin can read next to the
proposals that are still open.

## D2 — One rule: same code and same title, shared with RebindControls

`SameCodeAndTitle` is the D3 predicate extracted from `Rebindable`, and both
callers use it. It is deliberately not the `unchanged` remap class:

- `ClassifyLineage` calls an anchor `unchanged` when its single successor has
  the same title under any code, including a legacy-ID successor with a new
  code. Carrying that would move an edge to a different code without a person.
- A release that carries no legacy IDs makes every class `no_successor`, even
  for a code whose title did not change. The control rule still re-binds
  there, and the edge rule now matches it.

The #2321 stock-control exception (title-only change for a byte-identical
shipped manifest) does not apply. It is about the platform's own control
bundles, not crosswalk edges.

Everything the rule does not carry stays an open proposal: renamed, split,
reused code, withdrawn. It is flagged, never dropped. Controls on those
anchors are flagged by needs-reanchoring (#2285) as before.

## D3 — Copy, do not move: the legacy edge stays live

The approval copies the edge, as every super-admin approval does, and does not
retire the legacy edge. Two reasons:

- A read pinned to the old release (`?scf_release=<old>`) must still return
  what it returned before the upgrade. That is point-in-time replay, and an
  audit period frozen against the old release depends on it.
- A crosswalk that declares the old release on purpose still owns its edges
  there.

The carried edge is recorded under `crosswalk_source = scf-remap:<proposal>`,
as any approval is. So re-importing the crosswalk with its old declaration
leaves the carried edge in place: the crosswalk does not own it, so it does not
retire it. Re-importing it with no declaration (the boot path) finds the
carried pair and leaves it unchanged, then retires the crosswalk's own A edges
(#1971). `TestRequirementCoverage_DeclaredCrosswalkCarriedAcrossReleaseImport`
checks the declared re-import.

The cost is that a carried edge outlives a later edit of the crosswalk that
drops that mapping. The import reports it as unlisted, as it does for any
approved remap. That is no worse than a super-admin approval today.

## D4 — Idempotence and what a person already decided

- A proposal is carried only while it is `open`. A rejected or kept-legacy
  proposal stays as the person left it, and an approved one is not decided
  again. A re-run carries nothing.
- If the requirement already maps to the target anchor (a crosswalk re-import
  got there first, or a retired edge holds the pair), the carry is skipped and
  the proposal stays open, as bulk approval skips it with `ErrEdgeExists`.
- `decided_by` is `system:scf-import`, `bulk` is true, and the reason says
  which code carried into which release.

## D5 — Overlay edges are not carried

Tenant overlay edges (`fw_to_scf_overlay_edges`) belong to a tenant, and their
remap is decided by that tenant's writers (#1804). The import keeps queueing
them and decides none, so a tenant's own mapping is never changed across
tenants by a catalog import. The carry only inserts catalog edges and never
deletes one, so the #2240 catalog-delete guard has nothing to fire on.

## D6 — Web: document the limitation rather than forward `scf_release`

`web/app/api/requirements/[id]/coverage/route.ts` forwards no `?scf_release`,
so the UI always reads the current release. With D1, the current release now
includes every carried edge, which is what the ticket was worried about. Edges
waiting for a person are read only through the API with `?scf_release=<old>`.
Adding a release picker is a UI feature with no current user ask, so the route
documents the limitation instead. The upstream response's `scf_release` echo
already passes through to the client.

## D7 — The empty-catalog fallback, tested directly

The nit from the review: `resolveSCFRelease` now delegates to
`pinSCFRelease(ctx, scfrelease.Querier, label)`. `TestPinSCFRelease` drives it
with a fake release reader and no Postgres. It covers no release and only a
legacy release, which both read unpinned, the current release pinned, a named
release pinned, and an unloaded name, which is an error.

## Timing

The carry runs in `QueueEdgeRemapProposals`'s own transaction, the third of
the import's steps, not in `RebindControls`'s. The gap between the release
import committing and the carry committing is the same one a re-bound control
already has, within one `catalog import-scf` run. Folding all three steps into
one transaction would change `Import`'s contract for every caller, and nothing
here needs that.
