-- security-atlas -- #3096: the frozen-period SECURITY DEFINER guards fail
-- closed if their owner ever loses BYPASSRLS.
--
-- audit_periods_frozen_has_controls() (20261314000000, #3085 N-a) and
-- audit_period_frozen_no_truncate() (20261316000000, #3094) run as
-- atlas_migrate so they see every tenant's rows. They rely on the owner's
-- BYPASSRLS for that. Without it, FORCE RLS on the tables they read would
-- hand them the rows of the session's tenant context, or none: the commit
-- check would see no frozen period and let the commit through, and the
-- TRUNCATE guard would see no frozen rows and let the TRUNCATE through.
--
-- SET row_security = off on each function makes Postgres raise an error
-- instead ("query would be affected by row-level security policy") when a
-- query inside it would be filtered by RLS. With BYPASSRLS in place,
-- nothing changes. ALTER FUNCTION ... SET leaves the body, owner,
-- SECURITY DEFINER, search_path and grants as they are.
--
-- Constitutional invariants:
--   #2  nothing here writes a row.
--   #6  RLS stays enforced; a guard that cannot see past it refuses.
--   #10 the frozen-period guards cannot silently pass.
--
-- Reversible via 20261317000000_audit_period_frozen_guards_row_security.down.sql.

ALTER FUNCTION audit_periods_frozen_has_controls() SET row_security = off;
ALTER FUNCTION audit_period_frozen_no_truncate() SET row_security = off;
