-- OE-452: bounded retention for identity/session security surfaces.
--
-- This migration is intentionally non-destructive. It adds indexes used by
-- the retention sweeper and replaces the dormant geo-column comments with a
-- concrete, bounded purpose. The sweeper ships in Go; operators choose when
-- to run the first real purge in their deployment.

CREATE INDEX IF NOT EXISTS idx_sessions_revoked_retention
    ON sessions (revoked_at)
    WHERE revoked_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_oauth_token_exchanges_exchanged_at
    ON oauth_token_exchanges (exchanged_at);

COMMENT ON COLUMN sessions.geo_country IS
    'OE-452: optional ISO 3166-1 alpha-2 country code for tenant-enabled login anomaly triage and incident response. Not populated by default; future enrichment must be explicitly configured by the operator.';
COMMENT ON COLUMN sessions.geo_city IS
    'OE-452: optional city label for tenant-enabled login anomaly triage and incident response. Not populated by default; future enrichment must be explicitly configured by the operator.';
