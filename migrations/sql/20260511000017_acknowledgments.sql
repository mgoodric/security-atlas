-- security-atlas — policy acknowledgment workflow (slice 023).
--
-- A `policy_acknowledgment` is an affirmative, per-user attestation that
-- a published policy version has been read and accepted (canvas §2.6 +
-- §7.1; CONTEXT.md "Policy acknowledgment (slice 023)").
--
-- Schema layout:
--   - One row per (user, policy_version) ack instance. A user re-acks
--     after 365 days (annual recurrence per canvas §7.1; computed at
--     read time, no cron). A re-ack writes a fresh row -- prior rows
--     remain for audit lineage.
--   - `policy_version_id` references the policies row id directly. Each
--     publish creates a new policies row (slice 022 InsertPublishedPolicy),
--     so an ack of v1 (id=A) literally cannot satisfy v2 (id=B) -- the
--     FK target row id differs.
--   - `evidence_record_id` backreferences the slice-013 evidence row
--     produced when the ack was recorded. Set post-emission; NULL during
--     in-flight inserts so the row is still RLS-visible if the evidence
--     emission errors mid-flight (defensive: the row exists for replay).
--
-- Constitutional invariants honored:
--   #6  Tenant isolation at the DB layer. FORCE ROW LEVEL SECURITY +
--       four-policy split (tenant_read SELECT, tenant_write INSERT
--       WITH CHECK, tenant_update UPDATE USING + WITH CHECK,
--       tenant_delete DELETE).
--   #9  Manual evidence is first-class. Each ack emits one
--       policy.acknowledgment.v1 evidence record via slice 013 ingest
--       (handler concern; this migration only stores the backreference).
--   D3  Cross-tenant FK leakage blocked. Composite FK
--       (tenant_id, policy_version_id) -> policies(tenant_id, id)
--       cannot span tenants. Composite FK (tenant_id, user_id) ->
--       users(tenant_id, id) likewise; users gets a UNIQUE
--       (tenant_id, id) added here as the FK target. Adding UNIQUE on
--       a column-set whose subset (id) is already PK is redundant but
--       harmless and gives us the composite handle without rewriting
--       the slice-012 users migration.
--
-- Anti-criteria honored at the schema layer (P0):
--   - Anonymous ack rejected: column user_id is NOT NULL with FK to
--     users(tenant_id, id). An anonymous request never produces a
--     credential.UserID so the handler short-circuits to 401 before
--     attempting an insert.
--   - Stale-ack-counted: the rate query (handler) excludes rows whose
--     acknowledged_at is older than `now() - 365 days`. The DB stores
--     all rows; the freshness predicate lives at the query layer so
--     we can window-shift in tests via store.WithClock.
--   - Superseded-version ack: the FK targets a specific policies row.
--     The handler additionally rejects POST against rows whose
--     status != 'published' (defense in depth: 409 at the wire).
--
-- Migration is reversible via 20260511000017_acknowledgments.down.sql
-- which drops the table and removes the UNIQUE (tenant_id, id) from users
-- (returning users to its slice-012 shape).

-- ===== users gets UNIQUE (tenant_id, id) as FK target =====
--
-- Slice 012 created users with `id UUID PRIMARY KEY` (single-column PK).
-- The composite FK from policy_acknowledgments needs a composite UNIQUE
-- on the parent side. We add it here rather than amend slice 012 so
-- the migration history stays additive.
ALTER TABLE users
    ADD CONSTRAINT users_tenant_id_unique UNIQUE (tenant_id, id);

-- ===== policy_acknowledgments =====

CREATE TABLE policy_acknowledgments (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL,
    -- policy_id is the policy "logical id" for v1: today this equals
    -- policy_version_id because each version is its own row. The two are
    -- separated so slice 035's OPA-RBAC graduation (or any future
    -- logical-id introduction) does not require a column add later.
    policy_id           UUID NOT NULL,
    policy_version_id   UUID NOT NULL,
    user_id             UUID NOT NULL,
    -- acknowledged_at is when the user clicked attest (wall-clock the
    -- ingest service observes). Tests inject via store.WithClock; prod
    -- defaults to now().
    acknowledged_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- ack_token is the deterministic idempotency key the handler derives
    -- from (user_id, policy_version_id, day_bucket). Double-clicks
    -- within the same UTC day produce the same token and dedup at the
    -- UNIQUE constraint. A 365-day-later re-ack produces a fresh token
    -- (different day_bucket).
    ack_token           TEXT NOT NULL DEFAULT '',
    -- evidence_record_id backreferences the slice-013 evidence row.
    -- NULL during in-flight inserts so the row is still visible to
    -- audit queries if the evidence emission errors. The handler sets
    -- it via UPDATE in the same tx as the evidence Process call.
    evidence_record_id  UUID NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Composite FK enforces same-tenant policy reference. ON DELETE
    -- RESTRICT because acks are audit-grade -- a policy admin-delete
    -- must not silently destroy attestation history.
    CONSTRAINT policy_acknowledgments_policy_fk
        FOREIGN KEY (tenant_id, policy_version_id)
        REFERENCES policies (tenant_id, id)
        ON DELETE RESTRICT,
    -- Composite FK enforces same-tenant user reference. ON DELETE
    -- CASCADE because a deleted user's attestation lineage is no
    -- longer addressable (the user row backs cred.UserID); cascading
    -- keeps the table clean.
    CONSTRAINT policy_acknowledgments_user_fk
        FOREIGN KEY (tenant_id, user_id)
        REFERENCES users (tenant_id, id)
        ON DELETE CASCADE,
    -- Skew tolerance: a wall-clock injection that lands "in the future"
    -- by more than a minute is rejected. Keeps clock-skew bugs from
    -- silently corrupting the timeline.
    CONSTRAINT policy_acknowledgments_acknowledged_at_chk
        CHECK (acknowledged_at <= now() + interval '1 minute')
);

-- Idempotency dedup: partial UNIQUE because empty-token rows would
-- collide. The handler always sets ack_token, but defense-in-depth
-- (a future caller path that forgets) does not break the constraint.
CREATE UNIQUE INDEX policy_acknowledgments_ack_token_unique
    ON policy_acknowledgments (tenant_id, ack_token)
    WHERE ack_token <> '';

-- Hot path: "has user X ack'd version Y, and how fresh?" The DESC sort
-- on acknowledged_at lets the rate-window query stop at the first row.
CREATE INDEX idx_policy_acks_user_version
    ON policy_acknowledgments (tenant_id, user_id, policy_version_id, acknowledged_at DESC);

-- Hot path: rate denominator/numerator for a specific version.
CREATE INDEX idx_policy_acks_version_when
    ON policy_acknowledgments (tenant_id, policy_version_id, acknowledged_at DESC);

-- Secondary: cross-version analytics over policy lineage.
CREATE INDEX idx_policy_acks_policy_when
    ON policy_acknowledgments (tenant_id, policy_id, acknowledged_at DESC);

-- ===== Row-Level Security: four-policy split =====
--
-- Established pattern from slices 011/017/018/021/022/036.
-- current_tenant_matches() comes from slice 002.

ALTER TABLE policy_acknowledgments ENABLE ROW LEVEL SECURITY;
ALTER TABLE policy_acknowledgments FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON policy_acknowledgments
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON policy_acknowledgments
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON policy_acknowledgments
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON policy_acknowledgments
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- DML grants for atlas_app (RLS-enforced runtime) and atlas_migrate
-- (BYPASSRLS migrations + integration-test cleanup).
GRANT SELECT, INSERT, UPDATE, DELETE ON policy_acknowledgments TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON policy_acknowledgments TO atlas_migrate;
