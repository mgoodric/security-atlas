-- name: DemoSeededIDs :many
-- #2043 demo provenance: returns the subset of ids that the in-place demo
-- seed (#1765) added to table_name, for the caller's tenant. Read
-- surfaces and exports mark those rows "demo" so an auditor never mistakes
-- demo evidence for real evidence. The row_key of a single-uuid-key table is
-- {"id": "<uuid>"}, so the lookup matches whole keys and is served by the
-- demo_seed_rows_unique index. The tenant_id predicate (#2153) scopes the
-- read alongside RLS, so a connection RLS does not bind still reads one
-- tenant's marks.
SELECT (row_key->>'id')::uuid AS id
FROM demo_seed_rows
WHERE tenant_id = sqlc.arg(tenant_id)::uuid
  AND table_name = sqlc.arg(table_name)::text
  AND row_key IN (
      SELECT jsonb_build_object('id', x)
      FROM unnest(sqlc.arg(ids)::uuid[]) AS x
  );
