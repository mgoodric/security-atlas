-- security-atlas — slice 162: augment sessions with user_agent, ip_address, geo.
--
-- Surfaced during slice 154 settings page audit (F6) and filed as a v2
-- backlog follow-on. The slice 034 sessions table carries only id,
-- user_id, tenant_id, issued_at, expires_at, last_seen_at — no
-- user-agent, no IP, no geo. Slice 154 deliberately did not fabricate
-- those fields client-side (slice-108 P0-A1 no-fabrication posture);
-- this migration closes the gap on the data-model side so the wire
-- shape can carry the fields honestly.
--
-- Decisions (see docs/audit-log/162-sessions-wire-shape-decisions.md):
--   D1: ip_address is TEXT not INET. The slice doc AC-1 reads INET, but
--       the codec story with sqlc + pgx v5 is cleaner with TEXT: no
--       netip.Addr / pgtype.Inet plumbing through sqlc-generated code,
--       no need to special-case the null-INET case in tests. The Go
--       layer normalises to canonical IPv4 / IPv6 form via
--       net.ParseIP before insert; the operational difference vs INET
--       is negligible for a column that v1 surfaces read-only on a
--       single settings page. If a future slice needs CIDR-containment
--       operators (e.g. block-on-anomaly), the schema swap is a
--       single ALTER TABLE.
--   D2: geo_country is CHAR(2) — ISO 3166-1 alpha-2. Matches the slice
--       doc verbatim. geo_city is TEXT (no canonical length cap; long
--       city names exist).
--   D3: All four columns are nullable. Geo enrichment population is
--       out of scope per slice doc P0-162-3; the columns ship empty
--       and a follow-up slice (or on-write hook) populates them. Pre-
--       migration rows backfill to NULL by virtue of the IF NOT EXISTS
--       additive ALTER (no DEFAULT).
--   D4: Migration is idempotent (ADD COLUMN IF NOT EXISTS on every
--       column). Reversible via the matching down.sql.
--
-- Constitutional invariants honored:
--   #6  Tenant isolation unchanged — sessions table already carries
--       RLS policies (slice 034 _012_users_sessions_api_keys.sql).
--       The four new columns inherit RLS by virtue of being on the
--       same table; no policy change needed (the columns are not
--       referenced in any USING clause).
--   §4.6.5  No new audit-log surface. The /v1/me/sessions read +
--           DELETE paths already log to me_audit_log; the additive
--           fields ride those existing entries.
--
-- Reversible via 20260518100000_sessions_augment_ua_ip_geo.down.sql.

ALTER TABLE sessions
    ADD COLUMN IF NOT EXISTS user_agent TEXT,
    ADD COLUMN IF NOT EXISTS ip_address TEXT,
    ADD COLUMN IF NOT EXISTS geo_country CHAR(2),
    ADD COLUMN IF NOT EXISTS geo_city TEXT;

COMMENT ON COLUMN sessions.user_agent IS
    'Slice 162: User-Agent header captured at session create. NULL for pre-migration rows. Truncated to 512 bytes at the application layer (DoS guard).';
COMMENT ON COLUMN sessions.ip_address IS
    'Slice 162: client IP captured at session create. TEXT not INET — see migration header decisions log. Honors X-Forwarded-For only when TRUST_FORWARDED_HEADERS=1.';
COMMENT ON COLUMN sessions.geo_country IS
    'Slice 162: ISO 3166-1 alpha-2 country code. Populated by a future enrichment slice; ships NULL.';
COMMENT ON COLUMN sessions.geo_city IS
    'Slice 162: city name. Populated by a future enrichment slice; ships NULL.';
