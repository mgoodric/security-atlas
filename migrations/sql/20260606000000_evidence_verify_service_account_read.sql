-- security-atlas — slice 464: grant atlas_service_account read access to the
-- evidence ledger for the cross-tenant `atlas evidence verify` walk.
--
-- `atlas evidence verify --all-tenants` performs a READ-ONLY ledger-wide
-- integrity walk as a super-admin operation. Per canvas invariant #6
-- (tenant isolation at the DB layer) the cross-tenant walk must NOT use a
-- superuser connection; it uses the documented `SET LOCAL ROLE
-- atlas_service_account` path (BYPASSRLS, NOLOGIN, NOINHERIT; reachable
-- only from atlas_app per migrations/bootstrap/01-roles.sql).
--
-- atlas_service_account already holds SELECT on `tenants` (slice 192) which
-- the walk enumerates. It does NOT yet hold any grant on `evidence_records`,
-- so the BYPASSRLS read fails with "permission denied for table
-- evidence_records" (SQLSTATE 42501). This migration grants SELECT only —
-- the verify walk is read-only and never mutates the append-only ledger
-- (canvas invariant #2). No INSERT/UPDATE/DELETE is granted; the
-- service account cannot write evidence.

GRANT SELECT ON evidence_records TO atlas_service_account;
