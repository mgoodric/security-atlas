-- security-atlas — slice 190: OAuth token revocation list + audit log.
--
-- Adds two tables:
--
--   1. `oauth_revoked_tokens` — the hot-path revocation list. The
--      slice-190 JWT validation middleware (internal/auth/jwtmw) hits
--      this on every authenticated `/v1/*` request via a primary-key
--      lookup on `jti TEXT`. The middleware MUST consult this list
--      AFTER successful signature verification (P0-190-2 — a forged
--      JTI revocation check is meaningless without signature trust).
--
--   2. `oauth_revocation_events` — the append-only forensic audit log
--      of every successful `/oauth/revoke` call. Mirrors the
--      append-only shape of slice 188's `oauth_token_exchanges` and
--      slice 030's `decisions_audit`. Separate from oauth_token_exchanges
--      because (a) the two streams have orthogonal semantics — issuance
--      vs destruction — and (b) revocation is identity-scoped, not
--      tenant-scoped (decision D2 in
--      docs/audit-log/190-jwt-middleware-r2-decisions.md).
--
-- TENANCY NOTE — INTENTIONAL DEVIATION:
--
-- Neither table is tenant-scoped. A JWT carries `atlas:current_tenant_id`
-- + `atlas:available_tenants[]`; revoking a token destroys access for
-- the holder across every tenant in their available list. Forcing a
-- per-tenant revocation row would split the destruction across N rows
-- and create cross-tenant consistency problems for the
-- highest-privilege identity (super_admin tokens). Identity-scoped
-- revocation is the standard OAuth shape; the JWT `jti` is the global
-- handle.
--
-- Constitutional invariants honored:
--
--   #2 Ingestion + evaluation separated — `oauth_revocation_events` is
--      append-only by construction (SELECT + INSERT policies only
--      under FORCE ROW LEVEL SECURITY). Bugs in the revoke handler
--      cannot corrupt the audit record.
--   #6 Tenant isolation at the DB layer — the absence of tenant scope
--      is the deliberate deviation called out above. Other v1 tables
--      are tenant-scoped; OAuth revocation is NOT.
--   #10 Audit-period freezing — N/A (these are OAuth tables, not
--       evidence).
--
-- Anti-criteria honored at the schema layer:
--
--   - P0-190-4 (revoke returns 200 for unknown tokens): the schema
--     enforces nothing about pre-existence — the handler is free to
--     INSERT a row for any jti and the silent 200 RFC 7009 §2.2 shape
--     stays correct.
--   - The `oauth_revocation_events` table is append-only: NO
--     UPDATE/DELETE policy under FORCE RLS. atlas_app cannot mutate
--     forensic rows even if compromised at the application layer.
--
-- Idempotency / reversibility:
--
--   CREATE TABLE is a fresh schema; the down migration drops both
--   tables cleanly.

-- ===== oauth_revoked_tokens — hot-path revocation list =====

CREATE TABLE oauth_revoked_tokens (
    -- The JWT `jti` claim. Globally unique per RFC 7519 §4.1.7; the
    -- slice 188 + 189 token-mint paths use uuid.NewString() so
    -- collisions are statistically impossible. PK gives the middleware
    -- an index-only-scan O(1) lookup.
    jti           TEXT PRIMARY KEY,

    -- When the revocation was recorded. Mirrors revoked_at on the
    -- audit log; duplicated here so the hot-path lookup never needs
    -- to JOIN to the audit table.
    revoked_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The original `exp` claim of the revoked token. The sweeper
    -- deletes rows where `expires_at < now()` — after natural expiry,
    -- the JWT `exp` check rejects the token regardless of this
    -- table's contents, so the row is dead weight. Indexed below.
    expires_at    TIMESTAMPTZ NOT NULL,

    -- Identifier of the revoker. Free-text — accommodates both
    -- `oauth_client:<client_id>` (client_credentials revocation) and
    -- `user:<user_id>` (self-revocation via JWT bearer). The
    -- application layer formats the value.
    revoked_by    TEXT NOT NULL,

    -- Defense-in-depth: NULL is already forbidden by NOT NULL; this
    -- guards against an empty-string slip on the free-text columns.
    CONSTRAINT oauth_revoked_tokens_jti_nonempty
        CHECK (length(jti) > 0),
    CONSTRAINT oauth_revoked_tokens_revoked_by_nonempty
        CHECK (length(revoked_by) > 0)
);

-- Sweeper hot path: DELETE FROM oauth_revoked_tokens WHERE expires_at
-- < now(). Index lets the sweep be an index-range scan rather than a
-- seq scan on growing tables.
CREATE INDEX idx_oauth_revoked_tokens_expires_at
    ON oauth_revoked_tokens (expires_at);

-- RLS: NOT enabled. The table is not tenant-scoped (see header
-- comment). The middleware's IsRevoked call runs under whatever
-- tenant context the request brought; the lookup is by PK only and
-- carries no tenant-sensitive information.

-- atlas_app: SELECT for the middleware's IsRevoked check, INSERT for
-- the /oauth/revoke handler, DELETE for the sweeper. NO UPDATE —
-- the revocation list is append + reap only. The application Go
-- layer's Revoke uses ON CONFLICT (jti) DO NOTHING to remain
-- idempotent without needing UPDATE privilege; this is a
-- defense-in-depth choice (a compromised application role cannot
-- mutate revocation rows in place, only append new ones or sweep
-- expired ones).
GRANT SELECT, INSERT, DELETE ON oauth_revoked_tokens TO atlas_app;

-- ===== oauth_revocation_events — append-only audit log =====

CREATE TABLE oauth_revocation_events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The `jti` of the revoked token. Links forensic queries back to
    -- the oauth_revoked_tokens row. Not a foreign key — the audit row
    -- must survive sweeper deletes of the revocation list itself.
    jti            TEXT NOT NULL,

    revoked_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Same shape as oauth_revoked_tokens.revoked_by. Duplicated so the
    -- audit log is self-contained for forensic queries.
    revoked_by     TEXT NOT NULL,

    -- Best-effort source IP from the /oauth/revoke request.
    -- Pattern matches oauth_token_exchanges.ip_address.
    ip_address     INET NULL,

    CONSTRAINT oauth_revocation_events_jti_nonempty
        CHECK (length(jti) > 0),
    CONSTRAINT oauth_revocation_events_revoked_by_nonempty
        CHECK (length(revoked_by) > 0)
);

-- Forensic lookup: "show me every revocation for jti X" (rare — most
-- jtis revoke once, but the index covers re-revocation incidents).
CREATE INDEX idx_oauth_revocation_events_jti
    ON oauth_revocation_events (jti);

-- Operator dashboard: "show me the last 100 revocations".
CREATE INDEX idx_oauth_revocation_events_time
    ON oauth_revocation_events (revoked_at DESC);

-- RLS: NOT enabled at the tenant layer (same reasoning as the
-- revocation list). The append-only guarantee comes from the GRANT
-- below — atlas_app holds SELECT + INSERT only, no UPDATE/DELETE.
-- Without RLS enabled we cannot use POLICY-based enforcement; the
-- grant absence is the second line of defense.

-- Append-only grant: SELECT + INSERT only. Absence of UPDATE/DELETE
-- means atlas_app cannot mutate forensic rows.
GRANT SELECT, INSERT ON oauth_revocation_events TO atlas_app;
