-- security-atlas — slice 188: OAuth token-exchange audit log.
--
-- Adds the `oauth_token_exchanges` table — the append-only forensic
-- log of every successful RFC 8693 token-exchange the AS performs.
-- Slice 192's frontend tenant-switcher will be the highest-volume
-- writer of this table; slice 188 ships the schema + the audit-write
-- on every successful exchange.
--
-- APPEND-ONLY by construction: SELECT + INSERT policies only, NO
-- UPDATE or DELETE policy, under FORCE ROW LEVEL SECURITY. atlas_app
-- can read rows it owns (target tenant) and append new rows; nothing
-- short of a `BYPASSRLS` admin connection can mutate or remove audit
-- rows. Mirrors slice 030's `decisions_audit`, slice 021's
-- `exception_audit_log`, slice 036's `artifact_access_log`.
--
-- Tenancy: every row is tenant-scoped via `tenant_id NOT NULL`, where
-- tenant_id is the TARGET tenant of the exchange (the tenant the
-- caller switched INTO). RLS isolation gives the target tenant's
-- admins visibility of the inbound switch; an operator who switches
-- A -> B sees ONE row (under tenant B). When the operator switches
-- back B -> A, a second row writes under tenant A. The from_tenant_id
-- column is informational and not used for RLS gating.
--
-- Constitutional invariants honored:
--
--   #2 Ingestion + evaluation separated; this is an audit-grade
--      append-only table — bugs in evaluation cannot corrupt the
--      record.
--   #6 Tenant isolation enforced at the DB layer. The target tenant's
--      admin sees inbound exchanges; the from-tenant's admin sees a
--      mirror row (written under the from-tenant's scope at the
--      handler) — symmetry comes from two writes, not from RLS
--      cross-pollination. Slice 188 v1 writes only the
--      target-tenant-scoped row to keep the audit story simple.
--   #10 Audit-period freezing — N/A; OAuth tokens are not evidence
--       and the audit log is not subject to the slice-008 freeze
--       semantics.
--
-- Anti-criteria honored at the schema layer:
--
--   - Append-only: NO UPDATE/DELETE policy under FORCE RLS (P0-188-8).
--   - subject_token_jti is required so cross-token correlation is
--     possible during incident investigation; the JWT's jti uniquely
--     identifies the exchanged-FROM token.
--
-- Idempotency / reversibility:
--
--   CREATE TABLE is a fresh schema; the down-migration drops the
--   table cleanly. v1 has no in-flight audit rows to migrate.

CREATE TABLE oauth_token_exchanges (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The TARGET tenant of the exchange. Scopes RLS. NEVER NULL —
    -- every exchange has a destination; a "no-op" exchange to the
    -- same tenant is still logged.
    tenant_id           UUID NOT NULL,

    -- The `jti` of the subject_token presented at the exchange. Lets
    -- forensic queries correlate "which incoming session produced
    -- this exchange?" Required (every JWT atlas mints carries a jti
    -- per slice 187's claim shape).
    subject_token_jti   TEXT NOT NULL,

    -- The `atlas:current_tenant_id` of the subject_token, if any.
    -- NULL for client_credentials-issued tokens that did not yet have
    -- a current tenant set (slice 191 SDK migration path).
    from_tenant_id      UUID NULL,

    -- The `atlas:current_tenant_id` of the minted (exchanged-INTO)
    -- token. Matches tenant_id; kept as a separate column so a
    -- future schema evolution that allows cross-tenant log views can
    -- distinguish "row scope" (tenant_id) from "exchange destination"
    -- (to_tenant_id). v1 always has tenant_id = to_tenant_id.
    to_tenant_id        UUID NOT NULL,

    -- The `iss` claim of the subject_token. For exchanges where the
    -- caller presents an atlas-issued token this matches the
    -- configured Issuer; in v3 when external token formats are
    -- accepted (RFC 8693 §4.2 §iss-not-required-to-match) this lets
    -- the operator see who issued the subject.
    subject_token_iss   TEXT NOT NULL,

    -- The `sub` claim of the subject_token. Identifies the principal
    -- (human OIDC subject or machine client) whose session was
    -- exchanged.
    subject_token_sub   TEXT NOT NULL,

    exchanged_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Best-effort source IP. NULL when the handler runs outside an
    -- HTTP context (unit tests) or behind a proxy that strips the
    -- X-Forwarded-For chain. Stored as INET for index-friendly
    -- containment queries (e.g. "all exchanges from CIDR X" in an
    -- incident).
    ip_address          INET NULL,

    -- Defense-in-depth checks: required free-text columns must be
    -- non-empty. NULL is already forbidden by NOT NULL; this guards
    -- against an empty-string bypass on the high-cardinality columns.
    CONSTRAINT ote_jti_nonempty
        CHECK (length(subject_token_jti) > 0),
    CONSTRAINT ote_iss_nonempty
        CHECK (length(subject_token_iss) > 0),
    CONSTRAINT ote_sub_nonempty
        CHECK (length(subject_token_sub) > 0)
);

-- Hot-path index for the tenant audit-log view: most-recent exchanges
-- first. AC-16 calls this out explicitly.
CREATE INDEX idx_oauth_token_exchanges_tenant_time
    ON oauth_token_exchanges (tenant_id, exchanged_at DESC);

-- Forensic lookup-by-jti: "which exchange did THIS token produce?"
-- Not partial; jti is high-cardinality across all tenants.
CREATE INDEX idx_oauth_token_exchanges_jti
    ON oauth_token_exchanges (subject_token_jti);

-- ===== Row-Level Security — APPEND-ONLY two-policy pattern =====
--
-- oauth_token_exchanges is append-only by construction: SELECT +
-- INSERT policies only. No UPDATE/DELETE policy under FORCE ROW LEVEL
-- SECURITY means atlas_app cannot mutate audit rows. Mirrors slice
-- 030's decisions_audit, slice 021's exception_audit_log, slice 036's
-- artifact_access_log. Constitutional invariant #2: bugs in the OAuth
-- handler cannot corrupt the audit record.

ALTER TABLE oauth_token_exchanges ENABLE ROW LEVEL SECURITY;
ALTER TABLE oauth_token_exchanges FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON oauth_token_exchanges
    FOR SELECT
    USING (current_tenant_matches(tenant_id));

CREATE POLICY tenant_write ON oauth_token_exchanges
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));

-- Grants: SELECT + INSERT only. No UPDATE / DELETE — the absence of
-- the grant is the second line of defense after the absence of the
-- policy.
GRANT SELECT, INSERT ON oauth_token_exchanges TO atlas_app;
