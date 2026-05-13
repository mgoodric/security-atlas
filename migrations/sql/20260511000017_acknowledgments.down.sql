-- Reverse 20260511000017_acknowledgments.sql.
--
-- Drops the policy_acknowledgments table (cascades indexes + RLS policies +
-- grants) and removes the UNIQUE (tenant_id, id) constraint added to users.

DROP TABLE IF EXISTS policy_acknowledgments;

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_tenant_id_unique;
