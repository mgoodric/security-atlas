-- security-atlas — slice 189: OAuth authorization-code store.
--
-- Adds the `oauth_auth_codes` table — the short-lived, one-shot
-- store for RFC 6749 §4.1 authorization codes issued by `GET
-- /oauth/authorize` and redeemed by `POST /oauth/token` with
-- `grant_type=authorization_code`. Codes carry the full PKCE
-- challenge (RFC 7636) plus the authenticated user's identity
-- snapshot so the redemption path can mint a JWT without re-reading
-- the session.
--
-- TENANCY NOTE — INTENTIONAL DEVIATION FROM THE CANONICAL FOUR-POLICY
-- RLS PATTERN:
--
-- `oauth_auth_codes` is NOT tenant-scoped. The table holds platform-
-- global, short-lived (≤60 s) authorization codes. The code itself is
-- the access path; if an attacker has the 32-byte random code, the
-- one-shot `UPDATE ... WHERE consumed_at IS NULL RETURNING` semantics
-- + the PKCE verifier check are the access controls. Adding tenant_id
-- here would force the authorize handler to know the tenant context
-- at code-issuance time, which conflicts with the OIDC-callback shape
-- (tenant comes from the user's session, but the code redemption
-- happens via a different HTTP call entirely).
--
-- The current_tenant_id is captured AS A COLUMN VALUE (the tenant the
-- user is currently scoped to in their session), but NOT as the RLS
-- pivot. This mirrors slice 187's `oauth_clients` and slice 188's
-- `oauth_clients` tenancy decision.
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation at the DB layer — preserved by the JWT minted
--      AT REDEMPTION carrying the tenant scope. The auth code itself
--      is platform-global; the user identity it captures (including
--      current_tenant_id + available_tenants) is the source of truth
--      for the minted token's tenant claims.
--   #10 Audit-period freezing — N/A; auth codes are not evidence.
--
-- Anti-criteria honored at the schema layer:
--
--   - No `code_challenge_method=plain` (P0-189-1): CHECK constraint
--     restricts the column to ('S256') only.
--   - One-shot redemption (P0-189-3): the `consumed_at` column +
--     `UPDATE WHERE consumed_at IS NULL RETURNING` pattern at the
--     application layer enforce single-use semantics. Re-use → 0 rows
--     returned → invalid_grant.
--   - Short TTL: `expires_at` defaults to created_at + 60 seconds at
--     the application layer; the DB enforces NOT NULL only.
--
-- Idempotency / reversibility:
--
--   CREATE TABLE is a fresh schema; the down-migration drops the
--   table cleanly. v1 has no in-flight auth codes to migrate (they
--   live for at most 60 seconds).

CREATE TABLE oauth_auth_codes (
    -- The 32-byte random base64url-encoded authorization code. The
    -- PRIMARY KEY is the code itself (TEXT not UUID) because it is
    -- the lookup pivot and the natural identity.
    code                    TEXT PRIMARY KEY,

    -- The client_id that issued the authorize request. NOT a foreign
    -- key to oauth_clients because client revocation should not
    -- cascade-delete pending codes (let them expire naturally).
    client_id               TEXT NOT NULL,

    -- The redirect URI the client requested. Stored at issuance so
    -- the redemption path can validate exact-match against the
    -- redemption-time `redirect_uri` form param (RFC 6749 §10.6
    -- attacker-can-swap mitigation).
    redirect_uri            TEXT NOT NULL,

    -- PKCE challenge (RFC 7636 §4.2). Stored as base64url(sha256(verifier)).
    code_challenge          TEXT NOT NULL,

    -- PKCE method. CHECK constrains to `S256` only — `plain` is
    -- forbidden at the schema layer per P0-189-1. Future OAuth 2.1
    -- evolutions may extend this set; for now S256 is the only
    -- accepted value.
    code_challenge_method   TEXT NOT NULL DEFAULT 'S256',

    -- Identity snapshot — captured at authorize-time, used to mint
    -- the JWT at redemption-time. Storing the snapshot lets the
    -- redemption be a single-table read; no JOIN to users/sessions
    -- in the hot path.
    user_id                 UUID NOT NULL,
    idp_issuer              TEXT NOT NULL,
    idp_subject             TEXT NOT NULL,
    current_tenant_id       UUID NOT NULL,
    available_tenants       UUID[] NOT NULL DEFAULT '{}',
    roles                   JSONB NOT NULL DEFAULT '{}',
    super_admin             BOOLEAN NOT NULL DEFAULT FALSE,

    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- One-shot enforcement: NULL = unconsumed; non-NULL = consumed.
    -- The redemption path issues:
    --
    --   UPDATE oauth_auth_codes
    --   SET consumed_at = now()
    --   WHERE code = $1 AND consumed_at IS NULL
    --   RETURNING *;
    --
    -- A second redemption attempt returns 0 rows → invalid_grant.
    consumed_at             TIMESTAMPTZ NULL,

    -- TTL pivot. The authorize handler sets this to created_at + 60s.
    -- The sweeper goroutine DELETEs rows with `created_at < now() -
    -- interval '1 hour'` every 5 minutes (1-hour grace beyond the
    -- 60s TTL avoids races with in-flight redemptions).
    expires_at              TIMESTAMPTZ NOT NULL,

    -- Defense-in-depth: only S256 accepted at the DB layer.
    CONSTRAINT oauth_auth_codes_method_s256_only
        CHECK (code_challenge_method = 'S256'),

    -- Defense-in-depth: the code, challenge, redirect_uri must all
    -- be non-empty. NOT NULL is already required; this guards against
    -- empty-string bypass.
    CONSTRAINT oauth_auth_codes_code_nonempty
        CHECK (length(code) > 0),
    CONSTRAINT oauth_auth_codes_challenge_nonempty
        CHECK (length(code_challenge) > 0),
    CONSTRAINT oauth_auth_codes_redirect_nonempty
        CHECK (length(redirect_uri) > 0)
);

-- Sweeper index: the cleanup goroutine scans for expired codes by
-- created_at + interval. Partial-index this for unconsumed rows only;
-- consumed rows can be GC'd by the same sweeper but the hot path
-- (redemption) only ever reads unconsumed rows.
CREATE INDEX idx_oauth_auth_codes_created_at
    ON oauth_auth_codes (created_at);

-- RLS: NOT enabled (see header comment). The table holds platform-
-- global, short-lived codes. The application enforces access via the
-- code value itself + PKCE verifier + redirect_uri exact-match.

-- atlas_app: SELECT for the redemption path's lookup, INSERT for the
-- authorize path's code issuance, UPDATE for the one-shot
-- `consumed_at` mark. DELETE for the sweeper goroutine.
GRANT SELECT, INSERT, UPDATE, DELETE ON oauth_auth_codes TO atlas_app;
