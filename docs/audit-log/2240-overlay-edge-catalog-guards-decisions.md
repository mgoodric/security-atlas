# #2240 — overlay edges: catalog delete guard, requirement tenant check, tier and no_relationship follow-ups (decisions log)

Follow-ups from the reviews of #2220 (#1797) and #2242 (#1798). Parent:
#1735. Migration `20261234000000_overlay_edges_catalog_delete_guard.sql`.

- detection_tier_actual: manual_review
- detection_tier_target: integration

Each gap was found in code review, not by a test. Each now has an
integration test that was run against the schema and code without the fix
and failed (23001 not raised, no `cascade_removed` row, a cross-tenant
insert accepted, a 409 on the `no_relationship` pair, `mapping_tier:
verified` on overlay rows), then passed with it.

## D1 — refuse a catalog delete while an active overlay edge references it

A BEFORE DELETE row trigger on `framework_requirements` and on `scf_anchors`
raises `restrict_violation` (23001) while an active overlay edge points at
the row. A framework version delete cascades to its requirements, so the
requirement trigger refuses that too. Retiring the overlay edges first is
the way through.

Why a trigger, not FK `RESTRICT`: a `RESTRICT` FK would also be held by
retired edges, which are history. Every catalog-wipe path (`fullReseedSCF`,
`scfreleasetest`, test cleanups) would then have to delete tenant history
first, and a retired edge would block a real catalog delete for no reason
the tenant can act on.

## D2 — a retired edge still cascades, with a `cascade_removed` audit row

The same trigger writes one `fw_to_scf_overlay_edge_audit_log` row per
retired edge the delete will cascade, action `cascade_removed`, before the
parent row goes, so the requirement and anchor codes are still readable.
`before` is the edge's state (codes, relationship, strength, rationale,
source, status, retirement reason); `after` is `{"status": "removed",
"removed_with": <table>, "removed_row_id", "framework_requirement_id",
"scf_anchor_id"}`. Actor `system:catalog_delete`: the trigger cannot know
which person or process issued the catalog delete. The codes are LEFT
JOINed: on a framework version delete the version row is already gone when
the requirement trigger fires, so `framework_version` can be null.

The action CHECK gains `cascade_removed`. The unified audit log picks the
row up as kind `mapping_overlay` with no change (it reads every action).

## D3 — the requirement tenant check is a trigger on INSERT and UPDATE

`BEFORE INSERT OR UPDATE OF tenant_id, framework_requirement_id` on
`fw_to_scf_overlay_edges`: the requirement's framework version must have
`tenant_id IS NULL` or the overlay's tenant, else `check_violation`
(23514). A CHECK cannot read another table. The trigger is SECURITY
DEFINER so it sees the version's real owner: as `atlas_app`, another
tenant's private version is invisible under RLS, and "invisible" must read
as refused rather than skipped. A requirement that does not exist is left
to the FK's error. On UPDATE it fires before the immutability guard
(trigger names sort `guard_requirement_tenant` before `guard_update`), so
the error names the tenant mismatch.

Existing rows are not re-checked by the trigger.
`scripts/dry-run-2240-overlay-requirement-tenant.sql` lists any violation,
read-only. None is expected: the API resolves requirements in the global
catalog only (#1797 D5). Any row found is a decision for Matt, not a fix in
the script.

## D4 — overlay rows report no `mapping_tier`

The view keeps `'verified'` on the overlay arm (#1798 D3): consumers drop
`rejected` rows and the column is a non-null enum. The API stops reporting
it: `tierFor` blanks the tier for `origin = overlay` on requirement
coverage, control coverage and anchor traversal, and `mapping_tier` is
`omitempty` on the requirement-row wire shape too. Catalog rows are
unchanged. This matches #1797 ("no mapping tier") and #1800, which already
said no surface shows a tier for an overlay row. A future verified-only
metric reads `origin = 'catalog'`.

## D5 — a `no_relationship` catalog edge does not block an overlay edge

#2242 D4 found create and coverage disagreeing: create refused an overlay on
a pair whose catalog edge was `no_relationship`, while coverage counted an
overlay on such a pair. The catalog asserting "no relationship" credits
nothing, and the organisation's attested mapping is the thing the overlay
exists to record, so create now follows coverage: `refuseCatalogDuplicate`
(create, bulk import and remap approval) treats a `no_relationship` catalog
edge like a rejected one. The alternative (coverage dropping the overlay
when the catalog edge becomes `no_relationship`) would silently stop
counting a live attested edge.

## D6 — the stale comments

`scf_anchors.sql`'s `anchor_frameworks` CTEs and header said "catalog-global
crosswalk"; they read `effective_fw_to_scf_edges`, which includes the
caller's overlay edges (#1798). Reworded and regenerated with the pinned
sqlc. `controls.sql`'s CTEs carry no such comment.

## D7 — fixture cleanups delete overlay edges first

`fullReseedSCF`, `scfreleasetest.removeAB`, the ucfcoverage benchmark wipe,
soc2import's `resetISO` and the mappingoverlays fixture now delete overlay
edges before the catalog rows they reference, since an active edge refuses
those deletes.
