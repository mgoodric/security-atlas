-- #1870: index the cross-tenant identity join.
--
-- superadmin.DBChecker (and DBUserResolver.lookupSuperAdmin) match
-- users on (idp_issuer, idp_subject) across tenants, to find the
-- super_admins row held by the same human's users row in another
-- tenant. users_idp_principal_unique leads with tenant_id, so it
-- cannot serve that join; without this index the re-check that runs
-- on every request carrying atlas:super_admin scans users.
--
-- Additive and non-unique: one human legitimately has a users row per
-- tenant. The predicate matches the identity join's own guard.

CREATE INDEX IF NOT EXISTS users_idp_identity_idx
    ON users (idp_issuer, idp_subject)
    WHERE idp_issuer <> '' AND idp_subject <> '';
