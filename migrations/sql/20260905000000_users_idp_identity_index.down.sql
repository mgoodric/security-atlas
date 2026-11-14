-- Reverse #1870: drop the cross-tenant identity-join index.

DROP INDEX IF EXISTS users_idp_identity_idx;
