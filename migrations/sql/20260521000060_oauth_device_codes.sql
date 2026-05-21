-- security-atlas — slice 191: RFC 8628 Device Authorization Grant codes.
--
-- Adds the `oauth_device_codes` table — the ephemeral handle issued by
-- `POST /oauth/device_authorization` (RFC 8628 §3.1) and consumed by the
-- `POST /oauth/token` device-code redemption grant (RFC 8628 §3.4 /
-- §3.5). The CLI's `atlas login` command is the primary consumer:
--
--   1. CLI POSTs to `/oauth/device_authorization` with the client_id —
--      handler INSERTs a new row here with a 15-minute TTL and returns
--      `device_code` + `user_code` + `verification_uri` to the CLI.
--
--   2. CLI prints the `user_code` and tells the user to visit the
--      verification URI. The CLI begins polling `/oauth/token` with
--      `grant_type=urn:ietf:params:oauth:grant-type:device_code` every
--      `interval` seconds (default 5s per RFC 8628 §3.5).
--
--   3. User authenticates via the slice-034 OIDC RP, lands on
--      `/oauth/device?user_code=XXXX-XXXX`, clicks Approve. The approve
--      handler UPDATEs the row's `approved_at` + the OIDC user's full
--      identity (idp_issuer, idp_subject, current_tenant_id,
--      available_tenants, roles, super_admin) — captured snapshot-at-
--      approval-time so a later identity change can't retroactively
--      change the minted JWT's scope.
--
--   4. Next CLI poll succeeds; `/oauth/token` runs a one-shot
--      `UPDATE ... SET consumed_at = now() WHERE device_code = $1
--      AND approved_at IS NOT NULL AND consumed_at IS NULL RETURNING ...`
--      — replay protection at the SQL layer (P0-191-8). The handler
--      then mints a JWT scoped to the approving user's identity.
--
-- TENANCY NOTE — INTENTIONAL DEVIATION FROM THE CANONICAL FOUR-POLICY
-- RLS PATTERN:
--
-- `oauth_device_codes` is NOT tenant-scoped. Same reasoning as slice
-- 188's `oauth_clients` and slice 190's `oauth_revoked_tokens` (see
-- migration headers): the device-code flow is a platform-level
-- credential-exchange protocol — the row is born tenant-less (no user
-- has approved yet) and remains identity-scoped (the approving user
-- identity is the scope, not a tenant ID). The eventual JWT carries
-- tenant scope via the `atlas:current_tenant_id` claim that was set at
-- approval time; the row itself does not need RLS isolation because:
--
--   - The `device_code` is a 64-byte random secret — guessing collides
--     with the security boundary of every OAuth-issued bearer token.
--   - The `user_code` is short but rate-limited at the poll path and
--     TTL-expires in 15 minutes; even a brute-force attempt has
--     ~10^12 combinations within a 15-minute window — infeasible.
--   - Lookups go by `device_code` PK or `user_code` UNIQUE — no
--     cross-row scan that could leak.
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation at the DB layer — preserved by oauth_device_codes
--      having NO tenant-scoped column. The approving user's
--      tenant grants are captured as snapshot data on the row (so the
--      minted JWT is deterministic) but the row itself is not gated by
--      tenant RLS.
--   #10 Audit-period freezing — N/A; this table holds ephemeral
--       credential-exchange state, not evidence.
--
-- Anti-criteria honored at the schema layer:
--
--   - P0-191-4 (unambiguous user_code alphabet): the schema enforces a
--     `length(user_code) > 0` CHECK; the application layer
--     (internal/api/oauth/device_authorization.go) is the source of
--     truth for the ABCDEFGHJKLMNPQRSTUVWXYZ23456789 alphabet. Adding
--     a CHECK ~ '^[A-Z2-9]+$' would be defense-in-depth but couples
--     the schema to a presentation-layer detail (the hyphen at
--     position 5); the application-layer check is sufficient.
--   - P0-191-8 (one-shot redemption): the table's `consumed_at` column
--     gives the application a deterministic flag that an `UPDATE ...
--     RETURNING` can atomically gate on. The application's redemption
--     query is `UPDATE oauth_device_codes SET consumed_at = now()
--     WHERE device_code = $1 AND approved_at IS NOT NULL
--     AND consumed_at IS NULL RETURNING ...` — only one transaction
--     can win the redemption race.
--
-- Idempotency / reversibility:
--
--   CREATE TABLE is a fresh schema; the down migration drops the table
--   cleanly. No data-preservation step required — device codes are
--   ephemeral by design (15-minute TTL).

CREATE TABLE oauth_device_codes (
    -- The 64-byte random base64url-encoded device_code RFC 8628 §3.2
    -- requires. The application layer (internal/api/oauth/device_authorization.go)
    -- generates this with `crypto/rand` — 512 bits of entropy means
    -- guessing collides with the security of the encryption keys
    -- protecting the JWT signing material. PK gives the redemption
    -- handler an O(1) lookup.
    device_code     TEXT PRIMARY KEY,

    -- The short user-facing code per RFC 8628 §3.2. Generated from the
    -- unambiguous alphabet ABCDEFGHJKLMNPQRSTUVWXYZ23456789 (no
    -- 0/O/1/I/L) and formatted as XXXX-XXXX (e.g., "ABCD-2345").
    -- 8 chars from a 32-char alphabet = 32^8 ~= 10^12 combinations;
    -- combined with the 15-minute TTL and per-client_id rate limit on
    -- /oauth/device_authorization, brute force is infeasible.
    user_code       TEXT NOT NULL UNIQUE,

    -- The OAuth client identity that initiated the device flow. NOT a
    -- formal FK to oauth_clients(client_id) because the redemption
    -- path validates the client_id at the application layer (against
    -- the oauth_clients store) — duplicating that check at the FK
    -- layer would not buy us anything since the row is short-lived
    -- and the worst case (client deleted mid-flow) is a benign 400
    -- on redemption.
    client_id       TEXT NOT NULL,

    -- RFC 8628 §3.2 `expires_in` is 15 minutes by default. The
    -- application sets `expires_at = now() + 15min` at INSERT. The
    -- redemption handler returns `expired_token` 400 when this is in
    -- the past. A background sweeper (future v3) can DELETE expired
    -- rows; for v1 the row stays put — the 15-minute TTL plus the
    -- UNIQUE constraint on user_code means dead rows have minimal
    -- footprint.
    expires_at      TIMESTAMPTZ NOT NULL,

    -- ===== Approval snapshot (NULL until user clicks Approve) =====
    --
    -- These columns capture the OIDC-authenticated user's identity at
    -- the moment of approval. The minted JWT inherits this snapshot —
    -- a later identity mutation (e.g., admin removes the user from a
    -- tenant) cannot retroactively change the JWT's scope. This is the
    -- standard OAuth eventual-eviction semantic (slice 190 R2 model).

    -- Wall-clock instant the approval was recorded. NULL = pending.
    approved_at                         TIMESTAMPTZ NULL,

    -- The atlas-internal user UUID of the approver. NULL when pending.
    -- NULL is also used for the deny path (where consumed_at is set
    -- without approved_at being set).
    approved_by_user_id                 UUID NULL,

    -- The OIDC issuer URL of the IdP that authenticated the approver.
    -- Matches the slice-187 `atlas:idp_issuer` JWT claim shape.
    approved_by_idp_issuer              TEXT NULL,

    -- The IdP-scoped subject identifier of the approver.
    approved_by_idp_subject             TEXT NULL,

    -- The current tenant the approver was operating in at the moment
    -- of approval. The minted JWT's `atlas:current_tenant_id` claim
    -- inherits this. NULL is permitted for super_admin approvers who
    -- approve outside any tenant context.
    approved_by_current_tenant_id       UUID NULL,

    -- The full set of tenants the approver has access to at the
    -- moment of approval. Captured as a snapshot so the minted JWT's
    -- `atlas:available_tenants` claim is deterministic.
    approved_by_available_tenants       UUID[] NULL,

    -- The per-tenant role map at the moment of approval. JSONB shape
    -- matches the slice-187 `atlas:roles` JWT claim:
    -- `{"<tenant_uuid>": ["role1", "role2"]}`.
    approved_by_roles                   JSONB NULL,

    -- Whether the approver holds platform-wide super_admin at the
    -- moment of approval. Inherits to the JWT's `atlas:super_admin`
    -- claim. P0-188-4 (no super_admin elevation via exchange) means
    -- this can only be true if the approver was ALREADY super_admin
    -- via their OIDC login.
    approved_by_super_admin             BOOLEAN NULL,

    -- ===== Lifecycle terminus =====
    --
    -- Set to now() by the redemption handler when the device_code is
    -- successfully exchanged for a JWT, or by the deny handler when
    -- the user clicks Deny. Either way, the row is dead after this
    -- column is non-NULL — neither path can mutate it again.
    consumed_at                         TIMESTAMPTZ NULL,

    created_at                          TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Defense-in-depth: NULL is already forbidden by NOT NULL; this
    -- guards against an empty-string slip on the free-text columns.
    CONSTRAINT oauth_device_codes_device_code_nonempty
        CHECK (length(device_code) > 0),
    CONSTRAINT oauth_device_codes_user_code_nonempty
        CHECK (length(user_code) > 0),
    CONSTRAINT oauth_device_codes_client_id_nonempty
        CHECK (length(client_id) > 0)
);

-- The redemption path looks up by device_code PK (covered by the
-- implicit PK index); the approve path looks up by user_code; the
-- sweeper (future v3) ranges over expires_at. UNIQUE on user_code
-- already creates an index — no extra DDL needed there.
CREATE INDEX idx_oauth_device_codes_expires_at
    ON oauth_device_codes (expires_at);

-- RLS: NOT enabled. The table is not tenant-scoped (see header
-- comment). The application layer's queries are gated by PK or
-- UNIQUE-column lookups; there is no cross-row scan that could
-- leak data across identities.

-- atlas_app holds the full CRUD set required by the device-code flow:
--   - INSERT at /oauth/device_authorization (initiate flow)
--   - UPDATE at /oauth/device_authorization/approve (set approval snapshot)
--   - UPDATE at /oauth/device_authorization/deny (set consumed_at)
--   - UPDATE at /oauth/token device-code grant (set consumed_at via one-shot)
--   - SELECT at every step for the lookup-then-decide pattern
--   - DELETE for the future sweeper of past-expiry rows
GRANT SELECT, INSERT, UPDATE, DELETE ON oauth_device_codes TO atlas_app;
