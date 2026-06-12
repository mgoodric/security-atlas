-- Down migration for slice 509 — drop the IdP group-to-role mapping surface
-- for a clean up->down->up round-trip.

DROP TABLE IF EXISTS group_role_audit_log;
DROP TABLE IF EXISTS oidc_idp_group_mappings;

ALTER TABLE user_roles DROP CONSTRAINT IF EXISTS user_roles_origin_chk;
ALTER TABLE user_roles DROP COLUMN IF EXISTS origin;
