# #2481 — overlay guards: reseed audit and the parent-row lock (decisions log)

Follow-ups from the review of #2474 (#2240). Migration
`20261237000000_overlay_edges_catalog_delete_lock.sql`.

- detection_tier_actual: manual_review
- detection_tier_target: integration

## D1 — `fullReseedSCF` audits each overlay edge it deletes

The reseed deletes every overlay edge before wiping the catalog, which goes
around the #2240 guard. It now inserts one `cascade_removed` row per edge
first, under the edge's own `tenant_id`, actor `system:catalog_reseed`, with
the same `before` shape the guard trigger writes and `after.removed_with =
'catalog_reseed'`. The wipe statements now run in one transaction so the
audit rows and the delete land together.

Active edges are audited as well as retired ones, rather than refusing the
reseed: `fullReseedSCF` is a test helper that only runs when the shared
catalog is already broken, and blocking on a leftover active edge from an
earlier package would make the suite order-dependent again (slice 461).

`TestEnsureFullCatalog_ReseedAuditsOverlayEdges` failed with the audit
statement removed (0 rows, want 2) and passes with it.

## D2 — lock the parent row before counting

The guard now runs `SELECT ... FOR UPDATE` on `OLD` before `count(*)`. An
overlay insert's FK check holds `FOR KEY SHARE` on the requirement and
anchor, which conflicts with it.

Finding: the race the review described does not reproduce on PostgreSQL 16.
A `DELETE` locks its target row (`LockTupleExclusive`) before BEFORE ROW
triggers fire, so the guard already ran under that lock, and each plpgsql
statement takes a fresh READ COMMITTED snapshot. The two new tests
(`TestOverlay_CatalogDeleteRefusedWhenInsertCommitsMidDelete`,
`TestOverlay_InsertFailsWhenCatalogDeleteCommitsFirst`) pass both with and
without the explicit lock: they were run against the down-migrated function
and passed. So they are regression guards, not a red-then-green proof.

The explicit lock stays anyway: it costs nothing (the transaction already
holds it), and it makes the guard's correctness a property of the function
rather than of executor internals.
