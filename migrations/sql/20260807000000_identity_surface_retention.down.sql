-- Reverse OE-452 metadata/index additions. This is non-destructive: it drops
-- only purge-helper indexes and restores the original slice-162 geo comments.

DROP INDEX IF EXISTS idx_oauth_token_exchanges_exchanged_at;
DROP INDEX IF EXISTS idx_sessions_revoked_retention;

COMMENT ON COLUMN sessions.geo_country IS
    'Slice 162: ISO 3166-1 alpha-2 country code. Populated by a future enrichment slice; ships NULL.';
COMMENT ON COLUMN sessions.geo_city IS
    'Slice 162: city name. Populated by a future enrichment slice; ships NULL.';
