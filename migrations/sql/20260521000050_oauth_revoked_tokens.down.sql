-- Reverse slice 190's oauth_revoked_tokens + oauth_revocation_events
-- migration. Drops both tables in dependency-safe order (revocation
-- events references jti from the revoked tokens conceptually but not
-- via FK, so order does not actually matter — DROP IF EXISTS for
-- idempotency).

DROP TABLE IF EXISTS oauth_revocation_events;
DROP TABLE IF EXISTS oauth_revoked_tokens;
