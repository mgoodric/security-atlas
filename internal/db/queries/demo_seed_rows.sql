-- name: DemoSeededIDs :many
-- #2043 demo provenance: returns the subset of ids that the in-place demo
-- seed (#1765) added to table_name, for the caller's tenant (RLS). Read
-- surfaces and exports mark those rows "demo" so an auditor never mistakes
-- demo evidence for real evidence. The row_key of a single-uuid-key table is
-- {"id": "<uuid>"}, so the lookup matches whole keys and is served by the
-- demo_seed_rows_unique index.
SELECT (row_key->>'id')::uuid AS id
FROM demo_seed_rows
WHERE table_name = sqlc.arg(table_name)::text
  AND row_key IN (
      SELECT jsonb_build_object('id', x)
      FROM unnest(sqlc.arg(ids)::uuid[]) AS x
  );
